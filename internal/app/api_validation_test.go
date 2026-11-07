package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LFenX/lg-monitor/internal/config"
)

func validationRequest(t *testing.T, f *apiFixture, files map[string]*string, status int) config.ValidationResult {
	t.Helper()
	data, err := json.Marshal(config.ValidationInput{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "POST", "/api/v1/config/validate", "read", string(data), status)
	var result config.ValidationResult
	if status == 200 {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func draftText(value string) *string { return &value }

func TestHTTPValidateConfigDiskDraftDeletionLinesAndNoPublishing(t *testing.T) {
	f := newAPIFixture(t)
	template := "id: kept\nextends: compact\n"
	f.files.MapFS["card-templates/kept.yaml"] = &fstest.MapFile{Data: []byte(template)}
	rules := strings.Replace(string(f.files.MapFS["rules.yaml"].Data), "notify:", "template: kept, notify:", 1)
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte(rules)}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	before := f.manager.Snapshot()
	broken := "rules:\n  - id: broken\n    type: process_absent\n    target: missing\n    severity: critical\n    notify: [fake-channel]\n"
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte(broken)}
	if err := f.manager.Reload(); err == nil {
		t.Fatal("invalid disk config replaced last good config")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := f.manager.Subscribe(ctx)
	<-updates
	response := f.request(t, "POST", "/api/v1/config/validate", "read", "", 200)
	var checked config.ValidationResult
	if err := json.Unmarshal(response.Body.Bytes(), &checked); err != nil || checked.Valid || len(checked.Issues) != 1 || checked.Issues[0].Line != 4 || checked.Issues[0].File != "rules.yaml" {
		t.Fatalf("disk=%s %v", response.Body.String(), err)
	}
	checked = validationRequest(t, f, map[string]*string{"rules.yaml": &rules}, 200)
	if !checked.Valid || len(checked.Issues) != 0 {
		t.Fatalf("repair draft failed: %+v", checked)
	}
	checked = validationRequest(t, f, map[string]*string{"rules.yaml": &rules, "card-templates/kept.yaml": nil}, 200)
	if checked.Valid || !strings.Contains(checked.Issues.Error(), "kept") {
		t.Fatal("deleting referenced draft template was valid")
	}
	sources := map[string]string{}
	for _, file := range checked.Files {
		sources[file.Path] = file.Source
	}
	if sources["rules.yaml"] != "draft" || sources["targets.yaml"] != "disk" || sources["card-templates/kept.yaml"] != "deleted" {
		t.Fatalf("sources=%v", sources)
	}
	sqlRule := "rules: [{id: draft-sql, type: sql, query: 'DELETE FROM target_status', severity: warning, notify: [fake-channel]}]\n"
	checked = validationRequest(t, f, map[string]*string{"rules.yaml": &sqlRule}, 200)
	if checked.Valid || len(checked.Issues) == 0 {
		t.Fatal("SQL draft bypassed normal validation")
	}
	checked = validationRequest(t, f, map[string]*string{"rules.yaml": &rules, "dashboards/bad.yaml": draftText("id: bad\npanels: [{id: x, type: stat, title: test, query: 'SELECT :typo AS value', width: 3}]\nrefresh: 10s\ntitle: draft\n")}, 200)
	if checked.Valid || !strings.Contains(checked.Issues.Error(), "typo") {
		t.Fatal("dashboard draft bypassed parameter checks")
	}
	if f.files.writes != 0 || !bytes.Equal(f.files.MapFS["rules.yaml"].Data, []byte(broken)) || f.manager.Snapshot().Rules[0].ID != before.Rules[0].ID || len(f.client.sent) != 0 || f.vault.setCalls != 0 {
		t.Fatal("read-only validation changed persistent or active state")
	}
	select {
	case <-updates:
		t.Fatal("validation published a config update")
	default:
	}
}

func TestHTTPValidateConfigPathsAndByteLimits(t *testing.T) {
	f := newAPIFixture(t)
	for _, path := range []string{"../rules.yaml", "dashboards/../rules.yaml", "data/lgmon.db", "rules.yml", "dashboards/nested/a.yaml", `dashboards\fake.yaml`, "dashboards/a..b.yaml", "dashboards/.yaml"} {
		validationRequest(t, f, map[string]*string{path: draftText("")}, 400)
	}
	many := map[string]*string{}
	for i := range 65 {
		many[fmt.Sprintf("dashboards/fake-%d.yaml", i)] = nil
	}
	validationRequest(t, f, many, 400)
	oversize := strings.Repeat("#", config.MaxDraftFileBytes+1)
	validationRequest(t, f, map[string]*string{"rules.yaml": &oversize}, 400)
	full := "#" + strings.Repeat(" ", config.MaxDraftFileBytes-2) + "\n"
	total := map[string]*string{}
	for i := range 5 {
		total[fmt.Sprintf("dashboards/fake-%d.yaml", i)] = &full
	}
	validationRequest(t, f, total, 400)
	exact := map[string]*string{}
	for _, name := range []string{"targets.yaml", "rules.yaml", "notifiers.yaml", "settings.yaml"} {
		body := string(f.files.MapFS[name].Data)
		exact[name] = draftText("#" + strings.Repeat(" ", config.MaxDraftFileBytes-len(body)-2) + "\n" + body)
	}
	checked := validationRequest(t, f, exact, 200)
	if !checked.Valid || len(checked.Files) != 4 || f.files.writes != 0 {
		t.Fatalf("4 MiB boundary failed: %+v", checked)
	}
}
