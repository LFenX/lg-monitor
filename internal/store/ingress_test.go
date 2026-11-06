package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func ingressMessage(seq uint64) pipeline.Message {
	return pipeline.Message{CollectorID: "fake-ingress", Seq: seq, TS: int64(seq) * 1000, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "demo", Probe: 0, TS: int64(seq) * 1000, OK: true, StatusCode: 200}}
}

func TestIngressDedupAndCrashBetweenStorageAndApplication(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "monitor.db")
	s := openStore(t, Options{Path: path})
	message := ingressMessage(1)
	canonical, needed, err := s.Ingest(ctx, message)
	if err != nil || !needed || canonical.Seq != 1 || count(t, s, "probe_results") != 1 {
		t.Fatalf("first ingest: %+v %v %v", canonical, needed, err)
	}
	if ack, err := s.LastCommitted(ctx, message.CollectorID); err != nil || ack != 0 {
		t.Fatalf("premature acknowledgement: %d %v", ack, err)
	}
	s.Close()
	s = openStore(t, Options{Path: path})
	pending, exists, err := s.PendingIngress(ctx, message.CollectorID)
	if err != nil || !exists || pending.Seq != 1 {
		t.Fatalf("pending after crash: %+v %v", pending, err)
	}
	message.ProbeResult.OK = false
	canonical, needed, err = s.Ingest(ctx, message)
	if err != nil || !needed || !canonical.ProbeResult.OK || count(t, s, "probe_results") != 1 {
		t.Fatalf("replay did not preserve original payload: %+v %v", canonical, err)
	}
	if _, _, err := s.Ingest(ctx, ingressMessage(2)); err == nil {
		t.Fatal("accepted next record before pending application")
	}
	if err := s.CompleteIngress(ctx, pending.CollectorID, pending.Seq); err != nil {
		t.Fatal(err)
	}
	if _, needed, err := s.Ingest(ctx, message); err != nil || needed || count(t, s, "probe_results") != 1 {
		t.Fatalf("applied duplicate: %v %v", needed, err)
	}
	if _, needed, err := s.Ingest(ctx, ingressMessage(2)); err != nil || !needed {
		t.Fatalf("second ingest: %v %v", needed, err)
	}
	if err := s.CompleteIngress(ctx, message.CollectorID, 2); err != nil {
		t.Fatal(err)
	}
	if ack, err := s.LastCommitted(ctx, message.CollectorID); err != nil || ack != 2 || count(t, s, "collector_inbox") != 0 || count(t, s, "probe_results") != 2 {
		t.Fatalf("complete watermark: %d %v", ack, err)
	}
}

func TestIngressGapCannotSkipUnstoredDataAndTrimsAlreadyStoredPrefix(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{})
	if _, _, err := s.Ingest(ctx, ingressMessage(2)); err == nil {
		t.Fatal("normal sequence gap accepted")
	}
	gap := pipeline.Message{CollectorID: "fake-ingress", Seq: 5, TS: 6000, Kind: pipeline.KindDataGap, DataGap: &pipeline.DataGap{FromSeq: 1, ToSeq: 5, Dropped: 5, Reason: "虚构缓存上限"}}
	if _, _, err := s.Ingest(ctx, gap); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIngress(ctx, gap.CollectorID, gap.Seq); err != nil {
		t.Fatal(err)
	}
	gap.Seq = 8
	gap.DataGap.ToSeq = 8
	gap.DataGap.Dropped = 8
	canonical, needed, err := s.Ingest(ctx, gap)
	if err != nil || !needed || canonical.DataGap.FromSeq != 6 || canonical.DataGap.Dropped != 3 {
		t.Fatalf("expanded gap overlap: %+v %v", canonical, err)
	}
	if err := s.CompleteIngress(ctx, gap.CollectorID, 8); err != nil {
		t.Fatal(err)
	}
	var dropped int64
	if err := s.db.QueryRow(`SELECT sum(dropped) FROM collector_events WHERE collector_id=?`, gap.CollectorID).Scan(&dropped); err != nil || dropped != 8 {
		t.Fatalf("gap counted twice: %d %v", dropped, err)
	}
	if _, _, err := s.Ingest(ctx, ingressMessage(9)); err != nil {
		t.Fatal(err)
	}
}
