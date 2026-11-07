package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify"
)

type testToastClient struct {
	values []notify.Toast
	result notify.ToastResult
	err    error
}

func (c *testToastClient) Show(_ context.Context, value notify.Toast) (notify.ToastResult, error) {
	c.values = append(c.values, value)
	return c.result, c.err
}

func TestToastQueuePersistsSubmissionOrSkipAndDoesNotReplayIt(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(map[bool]string{false: "submitted", true: "skipped"}[skipped], func(t *testing.T) {
			f := newRuntimeFixture(t)
			f.files["notifiers.yaml"].Data = []byte("notifiers: [{id: test-channel, type: toast}]\n")
			if err := f.opts.Config.(*config.Manager).Reload(); err != nil {
				t.Fatal(err)
			}
			alert := seedUnsent(t, f, false)
			client := &testToastClient{result: notify.ToastResult{Skipped: skipped}}
			if skipped {
				client.result.Reason = "没有登录用户"
			}
			q := policyQueue(t, f)
			q.toast = client
			if err := q.Enqueue(context.Background(), alert); err != nil {
				t.Fatal(err)
			}
			close(q.jobs)
			if err := q.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			attempt := receiveRuntime(t, f.db.attempts)
			if attempt.OK == skipped || attempt.Skipped != skipped || attempt.MessageID != "" || len(client.values) != 1 || f.factories.Load() != 0 || !strings.Contains(client.values[0].Title, "严重告警") {
				t.Fatalf("toast audit=%+v values=%+v factories=%d", attempt, client.values, f.factories.Load())
			}
			restored := policyQueue(t, f)
			restored.toast = client
			if err := restored.replay(context.Background()); err != nil || len(restored.jobs) != 0 {
				t.Fatalf("already handled toast replayed: %d, %v", len(restored.jobs), err)
			}
		})
	}
}

func TestToastHTTPTestUsesConfigScopeAndReportsSkip(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(map[bool]string{false: "submitted", true: "skipped"}[skipped], func(t *testing.T) {
			f := newAPIFixture(t)
			f.files.MapFS["notifiers.yaml"].Data = []byte("notifiers: [{id: fake-channel, type: toast}]\n")
			if err := f.manager.Reload(); err != nil {
				t.Fatal(err)
			}
			if skipped {
				f.toast.result = notify.ToastResult{Skipped: true, Reason: "没有登录用户"}
			}
			path := "/api/v1/notifiers/fake-channel/test"
			f.request(t, "POST", path, "read", `{}`, 403)
			response := f.request(t, "POST", path, "config", `{}`, 200)
			var result api.Confirmation
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.OK == skipped || len(f.toast.values) != 1 || len(f.client.sent) != 0 || strings.Contains(result.Message, "实际收到") {
				t.Fatalf("toast test=%+v,%v values=%+v", result, err, f.toast.values)
			}
			if skipped && !strings.Contains(result.Message, "已跳过") {
				t.Fatalf("skip pretended to display: %+v", result)
			}
			f.request(t, "POST", path, "config", `{}`, 429)
		})
	}
}
