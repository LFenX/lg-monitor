package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const serviceSwitchFixture = `{"schemaVersion":1,"apps":[{"id":"blog","name":"虚构博客","root":"D:\\fake\\blog","ports":[3000,3001],"healthPath":"/health","domain":"blog.example.test","controller":"lfen-guardian","guardian":{"statusUrl":"http://127.0.0.1:8000/status"},"tunnel":{"metricsPort":9000,"config":"fake-private-tunnel"},"autostart":"off","env":{"PASSWORD":"fake-private-env"},"start":"fake-private-command"}],"database":{"kind":"postgres","name":"虚构数据库","ctl":"C:\\fake\\pgsql\\bin\\pg_ctl.exe","password":"fake-private-db"},"scripts":[{"name":"fake-private-script","start":"fake-private-script-command"}],"settings":{"token":"fake-private-settings"}}`

func serviceSwitchSnapshot(t *testing.T, data string) ServiceSwitchSnapshot {
	t.Helper()
	summary, err := ParseServiceSwitch([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	sha, _, err := ServiceSwitchFingerprint(summary)
	if err != nil {
		t.Fatal(err)
	}
	return ServiceSwitchSnapshot{Summary: summary, SHA256: sha, ReadAt: 2000000000000}
}

func TestServiceSwitchSanitizesBeforeHashAndGeneratesAllTargets(t *testing.T) {
	snapshot := serviceSwitchSnapshot(t, serviceSwitchFixture)
	_, wire, _ := ServiceSwitchFingerprint(snapshot.Summary)
	for _, private := range []string{"fake-private", `"env"`, `"start"`, `"settings"`, `"config"`, `"scripts"`} {
		// A generic skipped-kind label is allowed; no script object is transmitted.
		if private == `"scripts"` {
			continue
		}
		if bytes.Contains(wire, []byte(private)) {
			t.Fatalf("private field transmitted: %s", wire)
		}
	}
	changed := strings.ReplaceAll(serviceSwitchFixture, "fake-private", "changed-private")
	if other := serviceSwitchSnapshot(t, changed); other.SHA256 != snapshot.SHA256 {
		t.Fatal("ignored fields changed canonical fingerprint")
	}
	host := ServiceSwitchCollector{ID: "fake-host", Hostname: "fake-pc"}
	preview := ServiceSwitchPreviewTargets(host, snapshot, nil, TargetsFingerprint(nil), true)
	if len(preview.Items) != 4 {
		t.Fatalf("items=%+v", preview.Items)
	}
	blog, tunnel, database := preview.Items[0].Target, preview.Items[1].Target, preview.Items[2].Target
	if blog == nil || blog.ID != "blog" || blog.Collector != host.ID || blog.Source.Ref != "app:blog" || len(blog.Probes) != 4 || blog.Probes[0].URL != "http://127.0.0.1:3000/health" || blog.Probes[0].ExpectStatus != "<500" || blog.Probes[1].Type != "tcp" || blog.Probes[2].ExpectStatus != "2xx" || blog.Probes[3].Interval != "5m" || preview.Items[0].Autostart != "off" {
		t.Fatalf("blog=%+v", blog)
	}
	if tunnel == nil || tunnel.ID != "blog-tunnel" || *tunnel.Match.Port != 9000 || tunnel.Probes[0].URL != "http://127.0.0.1:9000/ready" || tunnel.Probes[0].ExpectStatus != "200" {
		t.Fatalf("tunnel=%+v", tunnel)
	}
	if database == nil || database.ID != "postgresql" || database.Match.Process != "postgres.exe" || database.Match.ExeDir != `C:\fake\pgsql\bin` || len(database.Probes) != 0 {
		t.Fatalf("database=%+v", database)
	}
	if preview.Items[3].Action != "skipped" {
		t.Fatal(preview.Items[3])
	}
	withoutPublic := ServiceSwitchPreviewTargets(host, snapshot, nil, "", false)
	if len(withoutPublic.Items[0].Target.Probes) != 3 {
		t.Fatal("publicProbes=false retained domain probe")
	}
	_, pushed, err := TargetConfig(&Config{Targets: []Target{*blog, *tunnel, *database}}, host.ID, host.Hostname)
	if err != nil || bytes.Contains(pushed, []byte(`"source"`)) {
		t.Fatalf("source leaked downstream: %s %v", pushed, err)
	}
	if blog.Source == nil {
		t.Fatal("downstream filtering mutated monitor target")
	}
	if err := Validate(&Config{Targets: []Target{*blog, *tunnel, *database}}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceSwitchInvalidApplicationsAreIsolatedAndPathsConstrained(t *testing.T) {
	for _, bad := range []string{
		`{"id":"BAD"}`, `{"id":"bad","ports":[0]}`, `{"id":"bad","ports":[65536]}`, `{"id":"bad","ports":"secret-value"}`,
		`{"id":"bad","healthPath":"invalid"}`, `{"id":"bad","healthPath":"//evil.example.test/"}`, `{"id":"bad","guardian":{"statusUrl":"file:///secret"}}`, `{"id":"bad","domain":"user:password@example.test"}`, `null`,
		`{"id":"bad","tunnel":{"metricsPort":0}}`, `{"id":"bad","tunnel":{"metricsPort":65536}}`,
	} {
		s := serviceSwitchSnapshot(t, `{"schemaVersion":1,"apps":[`+bad+`,{"id":"good","ports":[1234]}]}`)
		if len(s.Summary.Apps) != 1 || s.Summary.Apps[0].ID != "good" || len(s.Summary.Skipped) != 1 || strings.Contains(s.Summary.Skipped[0].Reason, "secret-value") {
			t.Fatalf("bad=%s summary=%+v", bad, s.Summary)
		}
	}
	for _, data := range []string{`{`, `null`, `[]`, `{"schemaVersion":2}`, `{"schemaVersion":1,"apps":{}}`, strings.Repeat(" ", MaxServiceSwitchFile+1)} {
		if _, err := ParseServiceSwitch([]byte(data)); err == nil {
			t.Fatal("accepted invalid source")
		}
	}
	for _, name := range []string{`C:\fake\config\services.json`, `d:/fake/SERVICES.JSON`, `\\fake-server\fake-share\services.json`} {
		if err := (ServiceSwitchSettings{Path: name}).Validate(); err != nil {
			t.Fatalf("%s %v", name, err)
		}
	}
	for _, name := range []string{`services.json`, `C:services.json`, `C:\fake\secrets.json`, `\\?\C:\fake\services.json`, `\\.\services.json`, `C:\fake\services.json:secret`, `C:\fake\services.json\`} {
		if err := (ServiceSwitchSettings{Path: name}).Validate(); err == nil {
			t.Fatalf("accepted path %s", name)
		}
	}
	duplicates := serviceSwitchSnapshot(t, `{"schemaVersion":1,"apps":[{"id":"same","ports":[1]},{"id":"same","ports":[2]}]}`)
	if len(duplicates.Summary.Apps) != 0 || len(duplicates.Summary.Skipped) != 1 {
		t.Fatalf("duplicate refs=%+v", duplicates)
	}
	badDuplicate := serviceSwitchSnapshot(t, `{"schemaVersion":1,"apps":[{"id":"same","ports":"invalid"},{"id":"same","ports":[2]}]}`)
	if len(badDuplicate.Summary.Apps) != 0 || len(badDuplicate.Summary.Skipped) != 1 {
		t.Fatal("duplicate ID with invalid field type was imported")
	}
	unsupported := serviceSwitchSnapshot(t, `{"schemaVersion":1,"apps":[{"id":"no-ports"}],"database":{"kind":"other"}}`)
	p := ServiceSwitchPreviewTargets(ServiceSwitchCollector{ID: "fake-host"}, unsupported, nil, "", true)
	if len(p.Items) != 2 || p.Items[0].Action != "skipped" || p.Items[1].Action != "skipped" {
		t.Fatal(p.Items)
	}
}

func TestServiceSwitchDedupActionsAndYAMLComments(t *testing.T) {
	host := ServiceSwitchCollector{ID: "fake-host", Hostname: "fake-pc"}
	snapshot := serviceSwitchSnapshot(t, serviceSwitchFixture)
	base := ServiceSwitchPreviewTargets(host, snapshot, nil, "", true)
	port := 3000
	existing := []Target{
		{ID: "manual-blog", Collector: "FAKE-PC", Match: Match{Port: &port}},
		*base.Items[1].Target, *base.Items[2].Target,
		{ID: "old", Collector: host.ID, Source: &TargetSource{Kind: "service-switch", Collector: host.ID, Ref: "app:old"}, Match: Match{Process: "old.exe"}},
	}
	existing[1].Name = "edited"
	preview := ServiceSwitchPreviewTargets(host, snapshot, existing, "", true)
	for i, action := range []string{"covered", "update", "unchanged", "skipped", "missing"} {
		if preview.Items[i].Action != action {
			t.Fatalf("items=%+v", preview.Items)
		}
	}
	if preview.Items[0].CoveredBy == nil || *preview.Items[0].CoveredBy != "manual-blog" || preview.Items[0].Target != nil || preview.Items[2].Target != nil || preview.Items[4].Target != nil {
		t.Fatal(preview.Items)
	}
	conflict := ServiceSwitchPreviewTargets(host, snapshot, []Target{{ID: "blog", Collector: "*", Match: Match{Port: &port}}, {ID: "blog-2"}}, "", true)
	if conflict.Items[0].TargetID != "blog-3" {
		t.Fatal(conflict.Items[0])
	}
	raw := []byte("# top comment\ntargets:\n  - id: manual # preserve id\n    name: 'Manual name'\n    match: {process: fake.exe} # preserve match\n")
	writable := ServiceSwitchPreviewTargets(host, snapshot, nil, TargetsFingerprint(raw), true)
	updated, result, err := ApplyServiceSwitchImport(raw, writable, ServiceSwitchImportInput{Refs: []string{"app:blog"}})
	if err != nil || len(result.Created) != 1 || !bytes.HasPrefix(updated, raw) {
		t.Fatalf("YAML not preserved: %s result=%+v %v", updated, result, err)
	}
	loaded, err := ServiceSwitchTargets(updated)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("targets=%+v %v", loaded, err)
	}
	none, result, err := ApplyServiceSwitchImport(raw, writable, ServiceSwitchImportInput{Refs: []string{}})
	if err != nil || !bytes.Equal(none, raw) || len(result.Created) != 0 {
		t.Fatalf("empty refs saved: %+v %v", result, err)
	}
	if _, _, err := ApplyServiceSwitchImport(raw, writable, ServiceSwitchImportInput{Refs: []string{"not-in-preview"}}); err == nil {
		t.Fatal("unknown ref accepted")
	}
	managedData, _ := json.Marshal(TargetsFile{Targets: existing})
	kept, changes, err := ApplyServiceSwitchImport(managedData, preview, ServiceSwitchImportInput{})
	if err != nil || len(changes.Removed) != 0 || !bytes.Contains(kept, []byte("app:old")) {
		t.Fatalf("default removed missing: %+v %v", changes, err)
	}
	removed, changes, err := ApplyServiceSwitchImport(managedData, preview, ServiceSwitchImportInput{RemoveMissing: true})
	if err != nil || len(changes.Removed) != 1 || bytes.Contains(removed, []byte("app:old")) {
		t.Fatalf("missing not removed: %+v %v", changes, err)
	}
	invalidApp := serviceSwitchSnapshot(t, `{"schemaVersion":1,"apps":[{"id":"old","ports":[0]}]}`)
	invalidPreview := ServiceSwitchPreviewTargets(host, invalidApp, existing[3:], "", true)
	if len(invalidPreview.Items) != 1 || invalidPreview.Items[0].Action != "skipped" {
		t.Fatal("invalid existing app treated as missing")
	}
}

func TestServiceSwitchMetadataValidationAndSnapshotIsolation(t *testing.T) {
	snapshot := serviceSwitchSnapshot(t, serviceSwitchFixture)
	preview := ServiceSwitchPreviewTargets(ServiceSwitchCollector{ID: "fake-host"}, snapshot, nil, "", true)
	cfg := &Config{Targets: []Target{*preview.Items[0].Target}}
	cloned := cfg.clone()
	cloned.Targets[0].Source.Ref = "app:changed"
	if cfg.Targets[0].Source.Ref != "app:blog" {
		t.Fatal("snapshot source metadata was shared")
	}
	duplicate := *preview.Items[0].Target
	duplicate.ID = "duplicate"
	if err := Validate(&Config{Targets: []Target{cfg.Targets[0], duplicate}}); err == nil {
		t.Fatal("duplicate ownership accepted")
	}
	duplicate.Collector = "*"
	if err := Validate(&Config{Targets: []Target{duplicate}}); err == nil {
		t.Fatal("wildcard owned target accepted")
	}
	data := []byte(`{"schemaVersion":1}`)
	data = append(data, bytes.Repeat([]byte(" "), MaxServiceSwitchFile-len(data))...)
	if _, err := ParseServiceSwitch(data); err != nil {
		t.Fatal("exactly 1 MiB valid source rejected", err)
	}
	oversizeSummary := `{"schemaVersion":1,"apps":[` + strings.Repeat(`{},`, 60000) + `{}]}`
	if len(oversizeSummary) >= MaxServiceSwitchFile {
		t.Fatal("oversize summary fixture source is not bounded")
	}
	if _, err := ParseServiceSwitch([]byte(oversizeSummary)); err == nil {
		t.Fatal("oversize wire summary was silently truncated")
	}
}
