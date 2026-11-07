package transport

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

func TestClockEstimateUsesMinimumOfLatestSixSamples(t *testing.T) {
	receiver := NewReceiver(nil)
	defer receiver.Close()
	at := int64(2000000000000)
	for _, offset := range []int64{40000, 300000, -300000} {
		receiver.observeClock("fake-clock", at-offset, at, true)
		for _, delay := range []int64{0, 2000, 7000, 1000, 0, 9000} {
			receiver.observeClock("fake-clock", at-offset-delay, at, false)
			if got := receiver.ClockOffset("fake-clock"); got != offset {
				t.Fatalf("queued heartbeat changed estimate: got=%d want=%d", got, offset)
			}
		}
		for range 6 {
			receiver.observeClock("fake-clock", at-offset-1000, at, false)
		}
		if got := receiver.ClockOffset("fake-clock"); got != offset+1000 {
			t.Fatal("old clock samples never expired")
		}
	}
	receiver.observeClock("fake-clock", 0, at, true)
	if receiver.ClockOffset("fake-clock") != 0 {
		t.Fatal("legacy hello reused a prior connection's estimate")
	}
}

func TestLegacyHelloUsesFirstHeartbeatForClockEstimate(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	at := int64(2000000000000)
	receiver.now = func() time.Time { return time.UnixMilli(at) }
	beats := make(chan Hello, 1)
	receiver.SetHeartbeat(func(_ context.Context, hello Hello) error { beats <- hello; return nil })
	stream, err := lgmonv1.NewCollectorLinkClient(connection).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.CloseSend()
	if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Hello{Hello: &lgmonv1.Hello{CollectorId: "fake-collector"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	if receiver.ClockOffset("fake-collector") != 0 {
		t.Fatal("legacy hello guessed an offset")
	}
	if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Heartbeat{Heartbeat: &lgmonv1.Heartbeat{Ts: at - 40000}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case hello := <-beats:
		if !hello.ClockKnown || hello.ClockOffsetMS != 40000 {
			t.Fatal("legacy heartbeat did not seed clock")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestHelloSeedsClockAndHeartbeatUsesReadTimeBeforeBacklogCommit(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	var monitor atomic.Int64
	monitor.Store(2000000000000)
	readTimes := make(chan int64, 8)
	receiver.now = func() time.Time {
		at := monitor.Load()
		select {
		case readTimes <- at:
		default:
		}
		return time.UnixMilli(at)
	}
	registered, heartbeats := make(chan Hello, 1), make(chan Hello, 1)
	receiver.register = func(_ context.Context, identity Hello) error { registered <- identity; return nil }
	receiver.SetHeartbeat(func(_ context.Context, identity Hello) error { heartbeats <- identity; return nil })
	writer, err := NewWriterWithProcesses(ctx, connection, Hello{CollectorID: "fake-collector"}, nil, nil, func() time.Time { return time.UnixMilli(monitor.Load() - 40000) })
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	select {
	case hello := <-registered:
		if !hello.ClockKnown || hello.ClockOffsetMS != 40000 || hello.CurrentTime != monitor.Load()-40000 {
			t.Fatalf("hello clock=%+v", hello)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	<-readTimes
	done := make(chan error, 1)
	go func() { done <- writer.Write(ctx, wireMessage(1)) }()
	message, err := receiver.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	<-readTimes
	if err := writer.Heartbeat(ctx, monitor.Load()-40000); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readTimes:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	monitor.Add(5000)
	receiver.Acknowledge(message, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case beat := <-heartbeats:
		if beat.ClockOffsetMS != 40000 {
			t.Fatalf("heartbeat queue wait became clock skew: %+v", beat)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	receiver.mu.Lock()
	samples := receiver.clocks["fake-collector"].samples
	got := samples[len(samples)-1]
	receiver.mu.Unlock()
	if got != 40000 || message.TS != 123456 {
		t.Fatalf("read timestamp or raw data changed: sample=%d raw=%d", got, message.TS)
	}
}
