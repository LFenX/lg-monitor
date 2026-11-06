package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type fakeDocuments struct {
	fstest.MapFS
	writes, removes int
	failure         error
	afterReplace    func()
}

func (f *fakeDocuments) Replace(_ context.Context, name string, data []byte) error {
	if f.failure != nil {
		return f.failure
	}
	f.writes++
	f.MapFS[name] = &fstest.MapFile{Data: bytes.Clone(data)}
	if f.afterReplace != nil {
		hook := f.afterReplace
		f.afterReplace = nil
		hook()
	}
	return nil
}
func (f *fakeDocuments) Remove(_ context.Context, name string) error {
	if _, ok := f.MapFS[name]; !ok {
		return fs.ErrNotExist
	}
	f.removes++
	delete(f.MapFS, name)
	return nil
}

func editorFixture(t *testing.T) (*Editor, *fakeDocuments, *Manager) {
	t.Helper()
	files := &fakeDocuments{MapFS: validFiles()}
	manager, err := NewManager(FSLoader{FS: files}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	editor, err := NewEditor(files, manager)
	if err != nil {
		t.Fatal(err)
	}
	return editor, files, manager
}

func TestDocumentEditorValidatesBeforeWritingAndPublishesWholeConfig(t *testing.T) {
	editor, files, manager := editorFixture(t)
	old := bytes.Clone(files.MapFS["targets.yaml"].Data)
	bad := []byte("targets:\n  - id: web\n    match: {cmdline: '['}\n")
	_, err := editor.Replace(context.Background(), "targets.yaml", bad)
	if err == nil || !strings.Contains(err.Error(), "targets.yaml 第 3 行") || files.writes != 0 || !bytes.Equal(files.MapFS["targets.yaml"].Data, old) {
		t.Fatalf("invalid config modified files: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := manager.Subscribe(ctx)
	<-updates
	valid := []byte(`{"targets":[{"id":"web","match":{"process":"new.exe"}}]}`)
	cfg, err := editor.Replace(ctx, "targets.yaml", valid)
	if err != nil || cfg.Targets[0].Match.Process != "new.exe" || len(cfg.Rules) != 1 || files.writes != 1 {
		t.Fatalf("valid document not applied: %+v %v", cfg, err)
	}
	select {
	case update := <-updates:
		if update.Targets[0].Match.Process != "new.exe" {
			t.Fatal("stale update")
		}
	default:
		t.Fatal("configuration update was not published")
	}
	if !bytes.Equal(files.MapFS["targets.yaml"].Data, valid) {
		t.Fatal("saved content differs")
	}
	cancel()
	if _, err := editor.Replace(ctx, "targets.yaml", old); !errors.Is(err, context.Canceled) || files.writes != 1 {
		t.Fatal("cancelled request wrote config")
	}
}

func TestTemplateEditorRejectsUnsafeSyntaxAndReferencedDeletion(t *testing.T) {
	editor, files, _ := editorFixture(t)
	for _, id := range []string{"../escape", "standard", "bad/name", ""} {
		if _, err := editor.SaveTemplate(context.Background(), id, []byte("id: safe\nextends: standard")); err == nil {
			t.Fatalf("invalid template path accepted: %q", id)
		}
	}
	for _, source := range []string{"{{range 100000000000}}{{end}}", "{{$n := .Alerts}}{{range $n}}{{end}}", "{{range len .Alerts}}{{end}}", "{{range .Alerts}}{{range $.Alerts}}{{end}}{{end}}", `{{define "loop"}}{{template "loop"}}{{end}}`} {
		start := time.Now()
		_, err := editor.SaveTemplate(context.Background(), "draft", []byte(fmt.Sprintf("id: draft\nextends: standard\ntitle: '%s'\n", source)))
		if err == nil || !strings.Contains(err.Error(), "第 3 行") || time.Since(start) > time.Second {
			t.Fatalf("unsafe save was not rejected promptly: %s %v", source, err)
		}
	}
	if files.writes != 0 {
		t.Fatal("unsafe template was saved")
	}
	if _, err := editor.DeleteTemplate(context.Background(), "oncall"); err == nil || files.removes != 0 {
		t.Fatal("referenced template was deleted")
	}
	if _, err := editor.SaveTemplate(context.Background(), "extra", []byte("id: extra\nextends: compact\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := editor.DeleteTemplate(context.Background(), "extra"); err != nil {
		t.Fatal(err)
	}
	if _, ok := files.MapFS["card-templates/extra.yaml"]; ok {
		t.Fatal("template was not deleted")
	}
}

func TestEditorRestoresFileAndSnapshotAfterConcurrentInvalidEdit(t *testing.T) {
	editor, files, manager := editorFixture(t)
	old := bytes.Clone(files.MapFS["targets.yaml"].Data)
	files.afterReplace = func() { files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte("httpListen: invalid\n")} }
	_, err := editor.Replace(context.Background(), "targets.yaml", []byte(`{"targets":[{"id":"web","match":{"process":"new.exe"}}]}`))
	if err == nil || !strings.Contains(err.Error(), "已恢复原文件") || !bytes.Equal(old, files.MapFS["targets.yaml"].Data) || manager.Snapshot().Targets[0].Match.Process != "demo.exe" {
		t.Fatalf("failed reload changed accepted config: %v", err)
	}
	delete(files.MapFS, "settings.yaml")
	failure := errors.New("fake write failure")
	files.failure = failure
	if _, err := editor.Replace(context.Background(), "targets.yaml", old); !errors.Is(err, failure) {
		t.Fatalf("write failure not wrapped: %v", err)
	}
}

func TestRuleAndTemplateDraftsUseCurrentReferencesWithoutMutatingThem(t *testing.T) {
	_, _, manager := editorFixture(t)
	base := manager.Snapshot()
	spec, draft, err := ParseTemplate([]byte("id: oncall\nextends: compact\ntitle: '{{range .Target.Tags}}{{.}}{{end}}'\n"), base)
	if err != nil || spec.Extends != "compact" || draft.Templates[0].Extends != "compact" || base.Templates[0].Extends != "standard" {
		t.Fatalf("template preview changed active config: %+v %v", spec, err)
	}
	if _, _, err := ParseTemplate([]byte("id: draft\nextends: standard\ntitle: '{{range 100000000000}}{{end}}'\n"), base); err == nil {
		t.Fatal("unsafe template preview was accepted")
	}
	rule, err := ParseRule([]byte(`{"id":"draft","type":"process_absent","target":"web","severity":"warning","notify":["alerts"]}`), base)
	if err != nil || rule.ID != "draft" || len(base.Rules) != 1 || base.Rules[0].ID != "down" {
		t.Fatalf("rule preview changed active config: %+v %v", rule, err)
	}
	if _, err := ParseRule([]byte(`{"id":"draft","type":"process_absent","target":"missing","severity":"warning","notify":["alerts"]}`), base); err == nil {
		t.Fatal("rule preview ignored missing reference")
	}
}

func TestDocumentPathsStayRelative(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", `C:\escape`, "C:/escape", `card-templates\escape.yaml`, ".", "card-templates/../escape.yaml"} {
		if validDocumentPath(name) {
			t.Fatalf("unsafe document path accepted: %s", name)
		}
	}
	if !validDocumentPath("card-templates/fake.yaml") {
		t.Fatal("valid relative document path rejected")
	}
}
