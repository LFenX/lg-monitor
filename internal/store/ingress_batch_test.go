package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestBatchInboxSurvivesCrashAndAcknowledgesWholePrefixOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "batch.db")
	s := openStore(t, Options{Path: path})
	messages := make([]pipeline.Message, 256)
	for i := range messages {
		messages[i] = ingressMessage(uint64(i + 1))
	}
	batch, err := s.IngestBatch(ctx, messages)
	if err != nil || len(batch.Messages) != 256 || batch.Through != 256 || count(t, s, "probe_results") != 256 || count(t, s, "collector_inbox") != 256 {
		t.Fatalf("store batch: count=%d through=%d %v", len(batch.Messages), batch.Through, err)
	}
	if ack, err := s.LastCommitted(ctx, "fake-ingress"); err != nil || ack != 0 {
		t.Fatal("batch acknowledged before application", ack, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, Options{Path: path})
	pending, err := s.PendingIngressBatch(ctx, "fake-ingress")
	if err != nil || len(pending.Messages) != 256 || pending.Messages[0].Seq != 1 || pending.Through != 256 {
		t.Fatalf("recover batch: %+v %v", pending, err)
	}
	messages[0].ProbeResult.OK = false
	batch, err = s.IngestBatch(ctx, messages)
	if err != nil || !batch.Messages[0].ProbeResult.OK || count(t, s, "probe_results") != 256 {
		t.Fatalf("stored replay changed content: %+v %v", batch, err)
	}
	if _, err := s.IngestBatch(ctx, []pipeline.Message{ingressMessage(257)}); err == nil {
		t.Fatal("next batch accepted before previous batch application")
	}
	if err := s.CompleteIngressBatch(ctx, "fake-ingress", 256); err != nil {
		t.Fatal(err)
	}
	batch, err = s.IngestBatch(ctx, messages)
	if err != nil || len(batch.Messages) != 0 || count(t, s, "collector_inbox") != 0 || count(t, s, "probe_results") != 256 {
		t.Fatal("acknowledged batch was written again", err)
	}
	if ack, err := s.LastCommitted(ctx, "fake-ingress"); err != nil || ack != 256 {
		t.Fatal("batch prefix was not acknowledged", ack, err)
	}
}

func TestBatchGapLateDropAndNormalMessagesRemainAtomic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	s := openStore(t, Options{Now: func() time.Time { return now }})
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	makeProbe := func(seq uint64, late bool) pipeline.Message {
		message := ingressMessage(seq)
		message.TS = now.UnixMilli()
		if late {
			message.TS = now.Add(-48 * time.Hour).UnixMilli()
		}
		return message
	}
	gap := pipeline.Message{CollectorID: "fake-ingress", Seq: 4, TS: now.UnixMilli(), Kind: pipeline.KindDataGap, DataGap: &pipeline.DataGap{FromSeq: 1, ToSeq: 4, Dropped: 4, Reason: "虚构缺口"}}
	input := []pipeline.Message{gap, makeProbe(5, true), makeProbe(6, false), makeProbe(7, true)}
	batch, err := s.IngestBatch(ctx, input)
	if err != nil || len(batch.Messages) != 2 || batch.Messages[0].Seq != 4 || batch.Messages[1].Seq != 6 || batch.Through != 7 || count(t, s, "probe_results") != 1 {
		t.Fatalf("mixed batch: %+v %v", batch, err)
	}
	if err := s.CompleteIngressBatch(ctx, "fake-ingress", 7); err != nil {
		t.Fatal(err)
	}
	var dropped int
	if err := s.db.QueryRow(`SELECT sum(dropped) FROM collector_events WHERE kind='late_dropped'`).Scan(&dropped); err != nil || dropped != 2 {
		t.Fatal("late batch audit wrong", dropped, err)
	}
	if _, err := s.IngestBatch(ctx, input); err != nil || count(t, s, "collector_events") != 2 {
		t.Fatal("replayed gap or late audit duplicated", err)
	}
	bad := []pipeline.Message{makeProbe(8, false), makeProbe(10, false)}
	if _, err := s.IngestBatch(ctx, bad); err == nil || count(t, s, "probe_results") != 1 {
		t.Fatal("invalid sequence batch partially committed")
	}
	if _, err := s.IngestBatch(ctx, nil); err == nil {
		t.Fatal("empty batch accepted")
	}
}

func TestBatchStorageAndCompletionRollbackAsSingleTransactions(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{})
	if err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_batch_insert BEFORE INSERT ON probe_results WHEN NEW.ts=2000 BEGIN SELECT RAISE(ABORT,'fake batch insert failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	input := []pipeline.Message{ingressMessage(1), ingressMessage(2)}
	if _, err := s.IngestBatch(ctx, input); err == nil || count(t, s, "probe_results") != 0 || count(t, s, "collector_inbox") != 0 || count(t, s, "collector_offsets") != 0 {
		t.Fatal("part of a failed batch storage transaction was committed")
	}
	if err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TRIGGER fail_batch_insert; CREATE TRIGGER fail_batch_delete BEFORE DELETE ON collector_inbox WHEN OLD.seq=2 BEGIN SELECT RAISE(ABORT,'fake batch completion failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestBatch(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIngressBatch(ctx, "fake-ingress", 2); err == nil || count(t, s, "collector_inbox") != 2 {
		t.Fatal("part of a failed batch completion transaction was committed")
	}
	if ack, err := s.LastCommitted(ctx, "fake-ingress"); err != nil || ack != 0 {
		t.Fatalf("failed batch advanced the applied watermark: %d %v", ack, err)
	}
}
