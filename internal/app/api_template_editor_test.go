package app

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestHTTPTemplateEditorKeepsExplicitEmptyFields(t *testing.T) {
	f := newAPIFixture(t)
	payload := `{"id":"no-fields","extends":"standard","fields":[],"title":"虚构空字段模板"}`
	preview := f.request(t, "POST", "/api/v1/templates/no-fields/preview", "read", payload, 200)
	if bytes.Contains(preview.Body.Bytes(), []byte("column_set")) {
		t.Fatal("explicitly empty draft fields inherited the preset fields")
	}
	if len(f.manager.Snapshot().Templates) != 0 || f.files.writes != 0 {
		t.Fatal("preview persisted the draft")
	}
	f.request(t, "PUT", "/api/v1/templates/no-fields", "config", payload, 200)
	response := f.request(t, "GET", "/api/v1/templates/no-fields", "read", "", 200)
	var definition config.Template
	if err := json.Unmarshal(response.Body.Bytes(), &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Fields == nil || len(definition.Fields) != 0 {
		t.Fatalf("definition lost the explicit empty list: %s", response.Body.String())
	}
	body, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	preview = f.request(t, "POST", "/api/v1/templates/no-fields/preview", "read", string(body), 200)
	if bytes.Contains(preview.Body.Bytes(), []byte("column_set")) {
		t.Fatal("round-trip edit restored hidden preset fields")
	}
}
