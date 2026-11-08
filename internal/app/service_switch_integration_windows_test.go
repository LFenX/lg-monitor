//go:build windows && integration && webui

package app

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestNativeServiceSwitchImportBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC), time.Now)
	f.files.MapFS["targets.yaml"] = &fstest.MapFile{Data: []byte("# Keep this manual target\ntargets:\n  - {id: web, match: {process: fake.exe}}\n  - id: fake-gone\n    collector: fake-host\n    match: {port: 43211}\n    source: {kind: service-switch, collector: fake-host, ref: 'app:gone'}\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	summary, err := config.ParseServiceSwitch([]byte(`{"schemaVersion":1,"apps":[{"id":"fake-import","name":"虚构博客","ports":[43210,43212],"healthPath":"/health","domain":"fake.example.test","tunnel":{"metricsPort":43213},"autostart":"off","env":{"password":"fake-private-source"}}],"database":{"kind":"postgres","name":"虚构数据库","ctl":"C:\\fake\\pgsql\\bin\\pg_ctl.exe"}}`))
	if err != nil {
		t.Fatal(err)
	}
	sha, _, err := config.ServiceSwitchFingerprint(summary)
	if err != nil {
		t.Fatal(err)
	}
	f.backend.options.ServiceSwitch = serviceSwitchQueryFunc(func(context.Context, string) (config.ServiceSwitchSnapshot, error) {
		return config.ServiceSwitchSnapshot{Summary: summary, SHA256: sha, ReadAt: f.now.UnixMilli()}, nil
	})
	runNativeWebBrowser(t, f, "service-switch.spec.ts")
	if len(f.manager.Snapshot().Targets) != 5 || len(f.manager.Snapshot().Rules) != 1 {
		t.Fatal("browser did not import exactly three targets while retaining manual/missing targets and rules")
	}
	if len(f.client.sent) != 0 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatal("import attempted real notifications")
	}
}
