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
