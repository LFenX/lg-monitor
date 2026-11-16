//go:build windows && integration

package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

func TestNativeEventLogPullAndBookmarkWithoutRegisteringSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider := fmt.Sprintf("lgmon-fake-event-%d", time.Now().UnixNano())
	registryPath := `SYSTEM\CurrentControlSet\Services\EventLog\Application\` + provider
	assertUnregistered := func() {
		t.Helper()
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, registryPath, registry.READ)
		if err == nil {
			key.Close()
			t.Fatal("owned test source was unexpectedly registered")
		}
		if !errors.Is(err, registry.ErrNotExist) {
			t.Fatalf("check source registration: %v", err)
		}
	}
	assertUnregistered()
	source := NewEventLogSource(nil)
	request := EventLogQuery{Channel: "Application", Filters: []EventLogFilter{{Provider: provider, IDs: []uint16{6060}}}}
	reader, err := source.Subscribe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := eventlog.Open(provider)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Info(6060, "first owned fake event"); err != nil {
		t.Fatal(err)
	}
	first, err := reader.Next(ctx)
	if err != nil || first.Event.Provider != provider || first.Event.EventID != 6060 || first.Bookmark == "" || !strings.Contains(first.Event.Message, "first owned fake event") {
		t.Fatalf("first event: %+v %v", first, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Info(6060, "second owned fake event"); err != nil {
		t.Fatal(err)
	}
	request.Bookmark = first.Bookmark
	resumed, err := source.Subscribe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	second, err := resumed.Next(ctx)
	if err != nil || second.Event.RecordID <= first.Event.RecordID || second.Bookmark == first.Bookmark || !strings.Contains(second.Event.Message, "second owned fake event") {
		t.Fatalf("bookmark resume: first=%+v second=%+v err=%v", first, second, err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := resumed.Next(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("next did not honor cancellation: %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	anchor, err := source.Anchor(ctx, "Application")
	if err != nil || anchor == "" {
		t.Fatalf("initial channel anchor: %q %v", anchor, err)
	}
	if err := writer.Info(6060, "third owned fake event after anchor"); err != nil {
		t.Fatal(err)
	}
	request.Bookmark = anchor
	anchored, err := source.Subscribe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer anchored.Close()
	third, err := anchored.Next(ctx)
	if err != nil || third.Event.RecordID <= second.Event.RecordID || !strings.Contains(third.Event.Message, "third owned fake event after anchor") {
		t.Fatalf("initial anchor lost the event before subscription opened: %+v %v", third, err)
	}
	if err := anchored.Close(); err != nil {
		t.Fatal(err)
	}
	assertUnregistered()
}

func TestNativeEventLogSubscriptionSurvivesIdleWaits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider := fmt.Sprintf("lgmon-fake-idle-%d", time.Now().UnixNano())
	source := NewEventLogSource(nil)
	reader, err := source.Subscribe(ctx, EventLogQuery{Channel: "Application", Filters: []EventLogFilter{{Provider: provider, IDs: []uint16{6061}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	// Several 250 ms waits pass with nothing to read. Calling EvtNext again without a signal used to break
	// the subscription with "The operation identifier is not valid" and send the collector into a retry loop.
	idle, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
	_, err = reader.Next(idle)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("idle subscription returned %v instead of waiting", err)
	}
	writer, err := eventlog.Open(provider)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Info(6061, "owned fake event after idle waits"); err != nil {
		t.Fatal(err)
	}
	item, err := reader.Next(ctx)
	if err != nil || item.Event.Provider != provider || item.Event.EventID != 6061 || !strings.Contains(item.Event.Message, "after idle waits") {
		t.Fatalf("event after idle waits: %+v %v", item, err)
	}
}
