package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestCardTemplateRejectsUnboundedSyntaxWithinOneSecond(t *testing.T) {
	for _, source := range []string{
		`{{range 100000000000}}{{end}}`,
		`{{$values := .Alerts}}{{range $values}}{{end}}`,
		`{{range (len .Alerts)}}{{end}}`,
		`{{range .Alerts}}{{if true}}{{range $.Processes}}{{end}}{{end}}{{end}}`,
		`{{define "self"}}{{template "self"}}{{end}}{{template "self"}}`,
		`{{define "card"}}safe{{end}}`,
		`{{block "body" .}}safe{{end}}`,
	} {
		started := time.Now()
		err := validateTemplateText(source)
		elapsed := time.Since(started)
		if err == nil {
			t.Fatalf("unsafe template accepted: %s", source)
		}
		if elapsed > time.Second {
			t.Fatalf("AST validation exceeded one second: %s", elapsed)
		}
	}
	for _, source := range []string{
		`{{range .Alerts}}{{.Alert.ID}}{{end}}`,
		`{{range $.Processes}}{{.Name}}{{end}}`,
		`{{range .Target.Tags}}{{.}}{{end}}`,
		`{{range $.Target.Tags}}{{.}}{{end}}`,
		`{{printf "%s" "{{define literal}}"}}`,
		"{{/* {{define ignored}} */}}safe",
	} {
		if err := validateTemplateText(source); err != nil {
			t.Fatalf("bounded/literal template refused: %s %v", source, err)
		}
	}
}

func TestUnsafeLoadedTemplateReportsItsYAMLLine(t *testing.T) {
	files := fstest.MapFS{"card-templates/unsafe.yaml": {Data: []byte("id: unsafe\nextends: standard\ntitle: '{{range 100000000000}}{{end}}'\n")}}
	if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "card-templates/unsafe.yaml 第 3 行") || !strings.Contains(err.Error(), "range") {
		t.Fatalf("unsafe template not rejected at source: %v", err)
	}
}
