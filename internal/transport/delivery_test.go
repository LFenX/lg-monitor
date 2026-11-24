// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package transport

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
)

type failingConnector struct{}

func (failingConnector) Connect(context.Context, string, *tls.Config) (RPCConnection, error) {
	return nil, errors.New("fake offline")
}

type captureWaiter struct {
	delays  []time.Duration
	cancel  context.CancelFunc
	maximum int
}

func (w *captureWaiter) Wait(ctx context.Context, delay time.Duration) error {
	w.delays = append(w.delays, delay)
	if len(w.delays) == w.maximum {
		w.cancel()
		return ctx.Err()
	}
	return nil
}

func TestDeliveryRetriesWithExponentialBackoffAndBoundedJitter(t *testing.T) {
	for _, jitter := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "jitter bounds"}[jitter], func(t *testing.T) {
			f := newPairingFixture(t)
			state, keys, _, err := f.join(t, "fake-collector", f.code(t, time.Minute), f.authority.Fingerprint())
			if err != nil {
				t.Fatal(err)
			}
			identity, err := LoadClient(f.ctx, state, keys, f.now)
			if err != nil {
				t.Fatal(err)
			}
			box, err := OpenOutbox(f.ctx, OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: identity.ID, Locker: fakeCacheLocker{}, Now: f.now})
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			waiter := &captureWaiter{cancel: cancel, maximum: 8}
			opts := DeliveryOptions{Outbox: box, Identity: identity, Connector: failingConnector{}, Waiter: waiter, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if !jitter {
				opts.Jitter = func(base time.Duration) time.Duration { return base }
			}
			delivery, err := NewDelivery(opts)
			if err != nil {
				t.Fatal(err)
			}
			if err := delivery.Run(ctx); err != nil {
				t.Fatal(err)
			}
			bases := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
			for i, seconds := range bases {
				base := seconds * time.Second
				high := min(time.Minute, base+base/5)
				if waiter.delays[i] < base || waiter.delays[i] > high || (!jitter && waiter.delays[i] != base) {
					t.Fatalf("backoff %d: %s", i, waiter.delays[i])
				}
			}
		})
	}
}

func TestDeliveryRecoversLostAckAndUnappliedInboxWithoutDuplicateRows(t *testing.T) {
	for _, applied := range []bool{true, false} {
		t.Run(map[bool]string{true: "lost ack", false: "stored not applied"}[applied], func(t *testing.T) {
			f := newPairingFixture(t)
			state, keys, _, err := f.join(t, "fake-collector", f.code(t, time.Minute), f.authority.Fingerprint())
			if err != nil {
				t.Fatal(err)
			}
			identity, err := LoadClient(f.ctx, state, keys, f.now)
			if err != nil {
				t.Fatal(err)
			}
			backendPath := filepath.Join(t.TempDir(), "monitor.db")
			backend, err := store.Open(f.ctx, store.Options{Path: backendPath, Now: f.now})
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			f.receiver.resume = backend.LastCommitted
			makeMessage := func(seq uint64) pipeline.Message {
				return pipeline.Message{CollectorID: identity.ID, Seq: seq, TS: int64(seq) * 1000, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "demo", Probe: 0, TS: int64(seq) * 1000, OK: true, StatusCode: 200}}
			}
			if _, _, err := backend.Ingest(f.ctx, makeMessage(1)); err != nil {
				t.Fatal(err)
			}
			if applied {
				if err := backend.CompleteIngress(f.ctx, identity.ID, 1); err != nil {
					t.Fatal(err)
				}
			}
			box, err := OpenOutbox(f.ctx, OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: identity.ID, Locker: fakeCacheLocker{}, Now: f.now})
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			for seq := uint64(1); seq <= 2; seq++ {
				if err := box.Write(f.ctx, makeMessage(seq)); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			consumed := make(chan error, 1)
			go func() {
				for {
					message, err := f.receiver.Read(ctx)
					if err != nil {
						consumed <- nil
						return
					}
					_, needed, err := backend.Ingest(ctx, message)
					if err == nil && needed {
						err = backend.CompleteIngress(ctx, message.CollectorID, message.Seq)
					}
					f.receiver.Acknowledge(message, err)
				}
			}()
			delivery, err := NewDelivery(DeliveryOptions{Outbox: box, Identity: identity, Connector: f.connector, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- delivery.Run(ctx) }()
			for {
				snapshot, err := box.State(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.Acked == 2 {
					break
				}
				select {
				case err := <-finished:
					t.Fatalf("delivery stopped: %v", err)
				case <-ctx.Done():
					t.Fatal("delivery timeout")
				case <-time.After(time.Millisecond):
				}
			}
			cancel()
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			<-consumed
			if watermark, err := backend.LastCommitted(f.ctx, identity.ID); err != nil || watermark != 2 {
				t.Fatalf("resume watermark: %d %v", watermark, err)
			}
			uriPath := filepath.ToSlash(backendPath)
			if filepath.VolumeName(backendPath) != "" {
				uriPath = "/" + uriPath
			}
			uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
			reader, err := sql.Open("sqlite", uri.String())
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var count int
			if err := reader.QueryRowContext(f.ctx, `SELECT count(*) FROM probe_results`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("duplicate rows after replay: %d %v", count, err)
			}
		})
	}
}

func TestDeliveryRealignsOutboxOnDivergenceInBothDirections(t *testing.T) {
	cases := []struct {
		name           string
		monitorSeq     uint64
		outboxAcked    uint64
		outboxWriteCnt int
		wantFinalSeq   uint64
	}{
		{name: "monitor ahead of outbox", monitorSeq: 50, outboxAcked: 0, outboxWriteCnt: 2, wantFinalSeq: 51},
		{name: "monitor reset to zero", monitorSeq: 0, outboxAcked: 50, outboxWriteCnt: 52, wantFinalSeq: 1},
		{name: "monitor rolled back behind outbox", monitorSeq: 40, outboxAcked: 50, outboxWriteCnt: 50, wantFinalSeq: 41},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPairingFixture(t)
			state, keys, _, err := f.join(t, "fake-collector", f.code(t, time.Minute), f.authority.Fingerprint())
			if err != nil {
				t.Fatal(err)
			}
			identity, err := LoadClient(f.ctx, state, keys, f.now)
			if err != nil {
				t.Fatal(err)
			}
			backendPath := filepath.Join(t.TempDir(), "monitor.db")
			backend, err := store.Open(f.ctx, store.Options{Path: backendPath, Now: f.now})
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			if tc.monitorSeq > 0 {
				seedDB, err := sql.Open("sqlite", backendPath)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := seedDB.ExecContext(f.ctx, `INSERT INTO collector_offsets(collector_id,stored_seq,applied_seq) VALUES(?,?,?)`, identity.ID, tc.monitorSeq, tc.monitorSeq); err != nil {
					seedDB.Close()
					t.Fatal(err)
				}
				seedDB.Close()
			}
			f.receiver.resume = backend.LastCommitted

			box, err := OpenOutbox(f.ctx, OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: identity.ID, Locker: fakeCacheLocker{}, Now: f.now})
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			makeMessage := func(ts int64) pipeline.Message {
				return pipeline.Message{CollectorID: identity.ID, Seq: 1, TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "demo", Probe: 0, TS: ts, OK: true, StatusCode: 200}}
			}
			for i := 1; i <= tc.outboxWriteCnt; i++ {
				if err := box.Write(f.ctx, makeMessage(int64(i)*1000)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.outboxAcked > 0 {
				if err := box.Acknowledge(f.ctx, tc.outboxAcked); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			consumed := make(chan error, 1)
			go func() {
				for {
					message, err := f.receiver.Read(ctx)
					if err != nil {
						consumed <- nil
						return
					}
					_, needed, err := backend.Ingest(ctx, message)
					if err == nil && needed {
						err = backend.CompleteIngress(ctx, message.CollectorID, message.Seq)
					}
					f.receiver.Acknowledge(message, err)
				}
			}()
			delivery, err := NewDelivery(DeliveryOptions{
				Outbox:              box,
				Identity:            identity,
				Connector:           f.connector,
				Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
				RealignOnDivergence: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- delivery.Run(ctx) }()

			// Wait for outbox to realign to monitorSeq, then write a new sample and verify it commits cleanly.
			for {
				snapshot, err := box.State(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.Acked == tc.monitorSeq && snapshot.Allocated == tc.monitorSeq {
					break
				}
				select {
				case err := <-finished:
					t.Fatalf("delivery stopped before realign: %v", err)
				case <-ctx.Done():
					t.Fatalf("timeout waiting for realign: state=%+v want=%d", snapshot, tc.monitorSeq)
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err := box.Write(ctx, makeMessage(999_000)); err != nil {
				t.Fatal(err)
			}
			for {
				snapshot, err := box.State(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.Acked == tc.wantFinalSeq {
					break
				}
				select {
				case err := <-finished:
					t.Fatalf("delivery stopped after realign: %v", err)
				case <-ctx.Done():
					t.Fatalf("timeout waiting for post-realign commit: state=%+v want=%d", snapshot, tc.wantFinalSeq)
				case <-time.After(5 * time.Millisecond):
				}
			}
			cancel()
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			<-consumed
			if watermark, err := backend.LastCommitted(f.ctx, identity.ID); err != nil || watermark != tc.wantFinalSeq {
				t.Fatalf("monitor watermark = %d, err=%v; want %d", watermark, err, tc.wantFinalSeq)
			}
		})
	}
}
