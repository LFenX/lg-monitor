package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type serviceSwitchQueryFunc func(context.Context, string) (config.ServiceSwitchSnapshot, error)

func (f serviceSwitchQueryFunc) QueryServiceSwitch(ctx context.Context, id string) (config.ServiceSwitchSnapshot, error) {
	return f(ctx, id)
}

func fakeServiceSwitch(t *testing.T, now time.Time, port int) config.ServiceSwitchSnapshot {
	t.Helper()
	summary := config.ServiceSwitchSummary{Apps: []config.ServiceSwitchApp{{ID: "fake-import", Name: "虚构导入", Ports: []int{port}, HealthPath: "/"}}, Skipped: []config.ServiceSwitchSkip{}}
	sha, _, err := config.ServiceSwitchFingerprint(summary)
	if err != nil {
		t.Fatal(err)
	}
	return config.ServiceSwitchSnapshot{Summary: summary, SHA256: sha, ReadAt: now.UnixMilli()}
}

func TestHTTPServiceSwitchPreviewImportVersionGuardsAndLocalPath(t *testing.T) {
	f := newAPIFixture(t)
	snapshot := fakeServiceSwitch(t, f.now, 43210)
	calls := 0
	f.backend.options.ServiceSwitch = serviceSwitchQueryFunc(func(_ context.Context, id string) (config.ServiceSwitchSnapshot, error) {
		calls++
		if id != "fake-host" {
			t.Fatal(id)
		}
		return snapshot, nil
	})
	original := bytes.Clone(f.files.MapFS["targets.yaml"].Data)
	originalRules := bytes.Clone(f.files.MapFS["rules.yaml"].Data)
	path := "/api/v1/collectors/fake-host/service-switch"
	response := f.request(t, "GET", path, "read", "", 200)
	var preview config.ServiceSwitchPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Collector.ID != "fake-host" || preview.TargetsVersion != config.TargetsFingerprint(original) || f.files.writes != 0 || len(preview.Items) != 1 || preview.Items[0].Action != "create" {
		t.Fatalf("preview=%+v writes=%d", preview, f.files.writes)
	}
	body, _ := json.Marshal(config.ServiceSwitchImportInput{SHA256: preview.SHA256, TargetsVersion: preview.TargetsVersion})
	f.request(t, "POST", path+"/import", "read", string(body), 403)
	limited := f.request(t, "GET", path, "read", "", 429)
	if calls != 1 || limited.Header().Get("Retry-After") != "2" {
		t.Fatal("shared read/save rate limit missing")
	}
	f.request(t, "GET", path+"?path=C:/fake/services.json", "read", "", 400)
	f.request(t, "POST", path+"/import", "config", `{"sha256":"fake","path":"C:/fake/services.json"}`, 400)
	f.request(t, "POST", path+"/import", "config", `{"refs":null}`, 400)
	if calls != 1 {
		t.Fatal("path-bearing request reached collector")
	}
	f.now = f.now.Add(2 * time.Second)
	saved := f.request(t, "POST", path+"/import", "config", string(body), 200)
	var result config.ServiceSwitchImportResult
	if err := json.Unmarshal(saved.Body.Bytes(), &result); err != nil || len(result.Created) != 1 || result.Created[0] != "fake-import" || result.TargetsVersion != config.TargetsFingerprint(f.files.MapFS["targets.yaml"].Data) {
		t.Fatalf("saved=%s %v", saved.Body, err)
	}
	if !bytes.Equal(originalRules, f.files.MapFS["rules.yaml"].Data) || len(f.manager.Snapshot().Targets) != 2 {
		t.Fatal("unrelated configuration changed")
	}
	after := bytes.Clone(f.files.MapFS["targets.yaml"].Data)
	f.now = f.now.Add(2 * time.Second)
	changed := f.request(t, "POST", path+"/import", "config", string(body), 409)
	if !strings.Contains(changed.Body.String(), "config_changed") || !bytes.Equal(after, f.files.MapFS["targets.yaml"].Data) {
		t.Fatal(changed.Body.String())
	}
	snapshot = fakeServiceSwitch(t, f.now, 43211)
	f.now = f.now.Add(2 * time.Second)
	changed = f.request(t, "POST", path+"/import", "config", string(body), 409)
	if !strings.Contains(changed.Body.String(), "source_changed") || !bytes.Equal(after, f.files.MapFS["targets.yaml"].Data) {
		t.Fatal(changed.Body.String())
	}
	f.request(t, "PUT", "/api/v1/config/settings", "admin", `{"serviceSwitch":{"path":"C:\\fake\\services.json"}}`, 400)
	if f.manager.Snapshot().Settings.ServiceSwitch.Path != "" {
		t.Fatal("remote settings changed local file path")
	}
	f.request(t, "GET", "/api/v1/collectors/missing/service-switch", "read", "", 404)
}

func TestHTTPServiceSwitchMapsCollectorAndSourceFailures(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{transport.ErrCollectorOffline, 503, "collector_offline"}, {transport.ErrCollectorOutdated, 409, "collector_outdated"}, {context.DeadlineExceeded, 504, "collector_timeout"},
		{collector.ErrServiceSwitchNotConfigured, 409, "source_not_configured"}, {&collector.ServiceSwitchError{Message: "虚构文件不存在"}, 422, "source_invalid"},
	} {
		t.Run(test.code, func(t *testing.T) {
			f := newAPIFixture(t)
			f.backend.options.ServiceSwitch = serviceSwitchQueryFunc(func(context.Context, string) (config.ServiceSwitchSnapshot, error) {
				return config.ServiceSwitchSnapshot{}, test.err
			})
			response := f.request(t, "GET", "/api/v1/collectors/fake-host/service-switch", "read", "", test.status)
			if !strings.Contains(response.Body.String(), test.code) {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestHTTPServiceSwitchRemovalValidationKeepsFilesAndEmptySelectionIsNoOp(t *testing.T) {
	f := newAPIFixture(t)
	data := []byte("# preserved manual comment\ntargets:\n  - id: web\n    collector: fake-host\n    match: {process: fake.exe}\n    source: {kind: service-switch, collector: fake-host, ref: 'app:web'}\n")
	f.files.MapFS["targets.yaml"] = &fstest.MapFile{Data: data}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	snapshot := fakeServiceSwitch(t, f.now, 43210)
	f.backend.options.ServiceSwitch = serviceSwitchQueryFunc(func(context.Context, string) (config.ServiceSwitchSnapshot, error) { return snapshot, nil })
	response := f.request(t, "GET", "/api/v1/collectors/fake-host/service-switch", "read", "", 200)
	var preview config.ServiceSwitchPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	body := `{"sha256":"` + preview.SHA256 + `","targetsVersion":"` + preview.TargetsVersion + `","refs":[],"removeMissing":true}`
	f.now = f.now.Add(2 * time.Second)
	rejected := f.request(t, "POST", "/api/v1/collectors/fake-host/service-switch/import", "config", body, 422)
	if !strings.Contains(rejected.Body.String(), `"issues"`) || !bytes.Equal(data, f.files.MapFS["targets.yaml"].Data) || f.files.writes != 0 {
		t.Fatal("invalid removal changed files", rejected.Body.String())
	}
	f.now = f.now.Add(2 * time.Second)
	body = strings.Replace(body, `"removeMissing":true`, `"removeMissing":false`, 1)
	f.request(t, "POST", "/api/v1/collectors/fake-host/service-switch/import", "config", body, 200)
	if !bytes.Equal(data, f.files.MapFS["targets.yaml"].Data) || f.files.writes != 0 {
		t.Fatal("empty selection rewrote file")
	}
}
