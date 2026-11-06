package app

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/transport"
)

func TestRuntimeReportsServiceReadyAfterInitialization(t *testing.T) {
	f := newRuntimeFixture(t)
	f.opts.Source = transport.NewReceiver(nil)
	ready := make(chan struct{}, 2)
	f.opts.Ready = func() { ready <- struct{}{} }
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("ready runtime did not stop")
		}
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("initialized runtime did not report service ready")
	}
	select {
	case <-ready:
		t.Fatal("runtime reported readiness twice")
	default:
	}
}
