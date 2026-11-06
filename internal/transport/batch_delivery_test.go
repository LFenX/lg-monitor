package transport

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type delayedBatchAcks struct {
	grpc.ServerStream
	delay time.Duration
	count *atomic.Int32
}

func (s delayedBatchAcks) SendMsg(value any) error {
	if message, ok := value.(*lgmonv1.MonitorMessage); ok && message.GetAck() != nil {
		s.count.Add(1)
		if s.delay > 0 {
			timer := time.NewTimer(s.delay)
			defer timer.Stop()
			select {
			case <-s.Context().Done():
				return s.Context().Err()
			case <-timer.C:
			}
		}
	}
	return s.ServerStream.SendMsg(value)
}

type noBatchNotifications struct{}

func (noBatchNotifications) Enqueue(context.Context, rules.Alert) error { return nil }

func seedBatchOutbox(t *testing.T, box *Outbox, count int, makeMessage func(uint64) pipeline.Message) {
	t.Helper()
	tx, err := box.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`INSERT INTO outbox_records(seq,queued_at,payload,size) VALUES(?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	total := 0
	for i := 1; i <= count; i++ {
		record, err := Encode(makeMessage(uint64(i)))
		if err != nil {
			t.Fatal(err)
		}
		payload, err := proto.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := statement.Exec(i, box.options.Now().UnixMilli(), payload, len(payload)); err != nil {
			t.Fatal(err)
		}
		total += len(payload)
	}
	if _, err := tx.Exec(`UPDATE outbox_meta SET allocated=?,bytes=? WHERE singleton=1`, count, total); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestTwentyThousandBacklogUsesBatchTransactionsAndBoundedRTT(t *testing.T) {
	for _, delay := range []time.Duration{0, 20 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			now := time.Now()
			path := filepath.Join(t.TempDir(), "monitor.db")
			backend, err := store.Open(ctx, store.Options{Path: path, Now: func() time.Time { return now }, DisableMaintenance: true})
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			engine, err := rules.New(ctx, rules.Options{Config: &config.Config{}, Repository: backend, Notifications: noBatchNotifications{}, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			receiver := NewReceiver(nil)
			receiver.resume = backend.LastCommitted
			defer receiver.Close()
			listener := bufconn.Listen(MaxMessageSize)
			defer listener.Close()
			var acks atomic.Int32
			server := NewServer(receiver, grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
				return handler(srv, delayedBatchAcks{ServerStream: stream, delay: delay, count: &acks})
			}))
			go server.Serve(listener)
			defer server.Stop()
			connection, err := grpc.NewClient("passthrough:///batch-memory", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			box, err := OpenOutbox(ctx, OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Now: func() time.Time { return now }, Locker: fakeCacheLocker{}})
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			seedBatchOutbox(t, box, 20000, func(seq uint64) pipeline.Message {
				return pipeline.Message{CollectorID: "fake-collector", Seq: seq, TS: now.UnixMilli() + int64(seq), Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "fake-target", OK: true, LatencyMS: 1}}
			})
			writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			worker := make(chan error, 1)
			go func() {
				for {
					packet, err := receiver.ReadBatch(ctx)
					if err != nil {
						worker <- nil
						return
					}
					batch, err := backend.IngestBatch(ctx, packet)
					if err == nil {
						for _, message := range batch.Messages {
							if err = engine.Handle(ctx, message); err != nil {
								break
							}
						}
						if err == nil {
							err = backend.CompleteIngressBatch(ctx, packet[0].CollectorID, batch.Through)
						}
					}
					receiver.AcknowledgeBatch(packet, err)
					if err != nil {
						worker <- err
						return
					}
				}
			}()
			delivery, err := NewDelivery(DeliveryOptions{Outbox: box, Identity: &ClientIdentity{ID: "fake-collector"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			delivered := make(chan error, 1)
			started := time.Now()
			go func() { _, err := delivery.deliver(ctx, writer); delivered <- err }()
			for {
				state, err := box.State(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if state.Acked == 20000 {
					break
				}
				select {
				case err := <-worker:
					t.Fatalf("batch worker stopped: %v", err)
				case err := <-delivered:
					t.Fatalf("delivery stopped: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			elapsed := time.Since(started)
			t.Logf("20000 records, RTT=%s, elapsed=%s, rate=%.0f/s, batch ACKs=%d", delay, elapsed, 20000/elapsed.Seconds(), acks.Load())
			if elapsed >= 20*time.Second || acks.Load() != 79 {
				t.Fatalf("batch delivery limit exceeded: %s, ACKs=%d", elapsed, acks.Load())
			}
			cancel()
			<-delivered
			<-worker
			view, err := store.OpenView(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			result, err := view.Query(context.Background(), `SELECT count(*) AS total FROM probe_results`)
			if err != nil || result.Rows[0][0] != int64(20000) {
				t.Fatalf("raw batch records lost or duplicated: %+v %v", result, err)
			}
		})
	}
}

func TestOutboxBatchHonorsRecordAndByteLimits(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprint(large), func(t *testing.T) {
			box, err := OpenOutbox(context.Background(), OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Locker: fakeCacheLocker{}})
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			count, want := 260, 256
			if large {
				count, want = 3, 1
			}
			seedBatchOutbox(t, box, count, func(seq uint64) pipeline.Message {
				message := wireMessage(seq)
				if large {
					message.TargetStatus.Reason = strings.Repeat("x", 600000)
				}
				return message
			})
			batch, err := box.NextBatch(context.Background())
			if err != nil || len(batch) != want || batch[0].Seq != 1 {
				t.Fatalf("batch bounds: count=%d %v", len(batch), err)
			}
			if err := box.Acknowledge(context.Background(), batch[len(batch)-1].Seq); err != nil {
				t.Fatal(err)
			}
			next, err := box.NextBatch(context.Background())
			if err != nil || next[0].Seq != uint64(want+1) {
				t.Fatalf("batch acknowledgement skipped data: %+v %v", next, err)
			}
		})
	}
}

func TestBatchAcknowledgementWaitsForEveryCommittedRecord(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		done <- writer.WriteBatch(ctx, []pipeline.Message{wireMessage(1), wireMessage(2), wireMessage(3)})
	}()
	packet, err := receiver.ReadBatch(ctx)
	if err != nil || len(packet) != 3 {
		t.Fatalf("batch boundary lost: %+v %v", packet, err)
	}
	receiver.Acknowledge(packet[0], nil)
	receiver.Acknowledge(packet[1], nil)
	select {
	case err := <-done:
		t.Fatalf("batch acknowledged before last commit: %v", err)
	default:
	}
	receiver.Acknowledge(packet[2], nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
