package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/feishu"
)

type scriptedClient struct {
	failures int
	calls    []feishu.CardMessage
	err      error
}

func (c *scriptedClient) SendCard(_ context.Context, message feishu.CardMessage) (feishu.SendResult, error) {
	c.calls = append(c.calls, message)
	if len(c.calls) <= c.failures {
		return feishu.SendResult{}, c.err
	}
	return feishu.SendResult{MessageID: "om_fake_not_real"}, nil
}

type memoryRecorder struct {
	attempts []Attempt
	err      error
}

func (r *memoryRecorder) RecordNotification(_ context.Context, attempt Attempt) error {
	r.attempts = append(r.attempts, attempt)
	return r.err
}

type fakeWaiter struct {
	waits []time.Duration
	err   error
}

func (w *fakeWaiter) Wait(_ context.Context, delay time.Duration) error {
	w.waits = append(w.waits, delay)
	return w.err
}
func testRequest() Request {
	return Request{NotifierID: "fake-notifier", Template: "standard", Message: feishu.CardMessage{AlertID: "fake-alert", State: "firing", ChatID: "oc_fake_not_real", Card: json.RawMessage(`{"schema":"2.0"}`)}}
}

func TestRetriesUseConfiguredDelaysAndRecordEveryAttempt(t *testing.T) {
	client := &scriptedClient{failures: 3, err: &feishu.Error{Kind: feishu.RateLimited, Code: 99991400, Message: "请求过于频繁"}}
	recorder, waiter := &memoryRecorder{}, &fakeWaiter{}
	var ids int
	sender, err := NewSender(Options{Client: client, Recorder: recorder, Waiter: waiter, Now: func() time.Time { return time.UnixMilli(123456) }, NewID: func() string { ids++; return strings.Repeat("x", ids) }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := sender.Send(context.Background(), testRequest())
	if err != nil || result.MessageID != "om_fake_not_real" {
		t.Fatalf("send=%+v,%v", result, err)
	}
	if !reflect.DeepEqual(waiter.waits, []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}) || len(recorder.attempts) != 4 || len(client.calls) != 4 {
		t.Fatalf("attempts=%d waits=%v", len(recorder.attempts), waiter.waits)
	}
	for i, attempt := range recorder.attempts {
		if attempt.TS != 123456 || attempt.OK != (i == 3) || attempt.AlertID != "fake-alert" {
			t.Fatalf("incorrect audit attempt: %+v", attempt)
		}
		if feishu.MessageUUID(client.calls[i].AlertID, client.calls[i].State, client.calls[i].ChatID) != feishu.MessageUUID(client.calls[0].AlertID, client.calls[0].State, client.calls[0].ChatID) {
			t.Fatal("retry changed idempotency identity")
		}
	}
}

func TestRetryLimitPermanentErrorsAndCancellation(t *testing.T) {
	for _, kind := range []feishu.ErrorKind{feishu.Network, feishu.MissingScopes} {
		client := &scriptedClient{failures: 100, err: &feishu.Error{Kind: kind, Message: "测试错误"}}
		recorder, waiter := &memoryRecorder{}, &fakeWaiter{}
		sender, err := NewSender(Options{Client: client, Recorder: recorder, Waiter: waiter})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sender.Send(context.Background(), testRequest()); err == nil {
			t.Fatal("failed sends reported success")
		}
		want := 4
		if kind == feishu.MissingScopes {
			want = 1
		}
		if len(client.calls) != want || len(recorder.attempts) != want {
			t.Fatalf("%s sent %d attempts, want %d", kind, len(client.calls), want)
		}
	}
	client := &scriptedClient{failures: 100, err: &feishu.Error{Kind: feishu.Network, Message: "测试网络错误"}}
	waiter := &fakeWaiter{err: context.Canceled}
	recorder := &memoryRecorder{}
	sender, err := NewSender(Options{Client: client, Recorder: recorder, Waiter: waiter})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Send(context.Background(), testRequest()); !errors.Is(err, context.Canceled) || len(client.calls) != 1 || len(recorder.attempts) != 1 {
		t.Fatalf("retry cancellation=%v", err)
	}
}

func TestUnknownClientAndAuditErrorsDoNotLeakSensitiveText(t *testing.T) {
	const secret = "fake-private-value-in-upstream-error"
	var logs bytes.Buffer
	client := &scriptedClient{failures: 1, err: errors.New(secret)}
	recorder := &memoryRecorder{}
	sender, err := NewSender(Options{Client: client, Recorder: recorder, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sender.Send(context.Background(), testRequest())
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(recorder.attempts[0].Error, secret) || strings.Contains(logs.String(), secret) {
		t.Fatal("unknown upstream error was echoed")
	}
	recorder.err = errors.New(secret)
	_, err = sender.Send(context.Background(), testRequest())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("audit error was echoed")
	}
}
