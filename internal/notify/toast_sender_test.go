package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeToastClient struct {
	values []Toast
	result ToastResult
	err    error
}

func (c *fakeToastClient) Show(_ context.Context, value Toast) (ToastResult, error) {
	c.values = append(c.values, value)
	return c.result, c.err
}

func TestFilteredToastDoesNotDisplayOrCreateFailureAudit(t *testing.T) {
	client, recorder := &fakeToastClient{}, &memoryRecorder{}
	sender, err := NewToastSender(ToastSenderOptions{Client: client, Recorder: recorder, Allow: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Send(context.Background(), ToastRequest{NotifierID: "desktop", AlertID: "fake-alert", State: "firing", Value: Toast{Title: "filtered"}}); err != nil || len(client.values) != 0 || len(recorder.attempts) != 0 {
		t.Fatalf("filtered toast was submitted/audited: %v", err)
	}
}

func TestToastSubmissionSkipAndFailureAreAuditedTruthfully(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client fakeToastClient
		ok     bool
		skip   bool
	}{
		{"submitted", fakeToastClient{}, true, false},
		{"skipped", fakeToastClient{result: ToastResult{Skipped: true, Reason: "没有登录用户"}}, false, true},
		{"failed", fakeToastClient{err: errors.New("fake-private-error")}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &memoryRecorder{}
			sender, err := NewToastSender(ToastSenderOptions{Client: &tc.client, Recorder: recorder, Now: func() time.Time { return time.UnixMilli(123456) }, NewID: func() string { return "fake-attempt" }})
			if err != nil {
				t.Fatal(err)
			}
			result, err := sender.Send(context.Background(), ToastRequest{NotifierID: "desktop", AlertID: "fake-alert", State: "firing", EventKey: "fake-event", Value: Toast{Title: "标题", Message: "正文"}})
			if result.Skipped != tc.skip || (err != nil) != (tc.client.err != nil) || len(recorder.attempts) != 1 || len(tc.client.values) != 1 {
				t.Fatalf("send=%+v,%v attempts=%+v", result, err, recorder.attempts)
			}
			attempt := recorder.attempts[0]
			if attempt.OK != tc.ok || attempt.Skipped != tc.skip || attempt.TS != 123456 || attempt.EventKey != "fake-event" || attempt.MessageID != "" || strings.Contains(attempt.Error, "fake-private-error") {
				t.Fatalf("untruthful audit: %+v", attempt)
			}
			if tc.client.err != nil && (!errors.Is(err, tc.client.err) || strings.Contains(err.Error(), "fake-private-error")) {
				t.Fatalf("unsafe or lost failure: %v", err)
			}
		})
	}
}
