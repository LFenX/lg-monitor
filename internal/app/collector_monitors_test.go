package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func TestCollectorMonitorsLocalFailureDoesNotCancelOtherPipeline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	healthy := make(chan struct{})
	failed := make(chan struct{})
	done := make(chan error, 1)
	var runs atomic.Int32
	go func() {
		done <- superviseCollectorMonitors(ctx, []collectorMonitorLink{{Name: "healthy"}, {Name: "broken"}}, slog.New(slog.NewTextHandler(io.Discard, nil)), func(ctx context.Context, link collectorMonitorLink) error {
			if link.Name == "healthy" {
				close(healthy)
				<-ctx.Done()
				return ctx.Err()
			}
			if runs.Add(1) == 2 {
				close(failed)
			}
			return errors.New("fixture disk failure")
		}, func(ctx context.Context, delay time.Duration) error {
			if delay != 10*time.Second {
				t.Errorf("unbounded retry: %s", delay)
			}
			if runs.Load() == 1 {
				return nil
			}
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-healthy:
	case <-time.After(time.Second):
		t.Fatal("healthy pipeline did not start")
	}
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("failed pipeline did not retry")
	}
	select {
	case err := <-done:
		t.Fatalf("other monitor cancelled: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("collector pipelines leaked")
	}
	policy, err := (disabledMonitorOperations{}).ReadOperationPolicy(context.Background())
	if err != nil || policy.Enabled || len(policy.Services) != 0 {
		t.Fatal("secondary monitor inherited grants")
	}
}

func TestHTTPSettingsCannotExpandLocalMonitorTrustOrOperationGrants(t *testing.T) {
	f := newAPIFixture(t)
	original := []byte("collectorMonitors: [{id: company}]\n")
	f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: original}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"collectorMonitors":[{"id":"company","allowOperations":true}]}`,
		`{"collectorMonitors":[{"id":"company","enabled":false}]}`,
		`{"collectorMonitors":[{"id":"different"}]}`,
		`{"collectorMonitors":[]}`,
	} {
		f.request(t, "PUT", "/api/v1/config/settings", "admin", body, 400)
		if !bytes.Equal(f.files.MapFS["settings.yaml"].Data, original) {
			t.Fatal("denied HTTP update changed local trust settings")
		}
	}
	f.request(t, "PUT", "/api/v1/config/settings", "admin", `{"collectorMonitors":[{"id":"company","enabled":true}],"cache":{"maxSize":"64MB"}}`, 200)
}
