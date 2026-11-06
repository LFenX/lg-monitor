package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestRuleDraftUsesStrictFieldsAndCurrentReferences(t *testing.T) {
	files := fstest.MapFS{
		"targets.yaml":   {Data: []byte("targets: [{id: web, match: {process: fake.exe}}]")},
		"notifiers.yaml": {Data: []byte("feishuApps: [{id: fake, appId: cli_fake, secretRef: fake, domain: feishu}]\nnotifiers: [{id: fake, type: feishu_app, app: fake, chatId: oc_fake}]")},
	}
	cfg, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	valid := "id: draft\ntype: process_absent\ntarget: web\nseverity: warning\nnotify: [fake]\n"
	for _, tc := range []struct{ text, want string }{
		{valid, ""},
		{strings.Replace(valid, "web", "missing", 1), "draft.yaml 第 3 行"},
		{valid + "typo: value\n", "typo"},
		{strings.Replace(valid, "id: draft", "id: 123", 1), "字符串"},
		{"- " + strings.ReplaceAll(valid, "\n", "\n  "), "字段类型"},
		{valid + "---\n" + valid, "只能包含一个 YAML 文档"},
	} {
		files["draft.yaml"] = &fstest.MapFile{Data: []byte(tc.text)}
		rule, err := LoadRuleFile(files, "draft.yaml", cfg)
		if tc.want == "" {
			if err != nil || rule.ID != "draft" || len(cfg.Rules) != 0 {
				t.Fatalf("draft changed config or did not load: %+v %v", rule, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("draft %q: %v, want %q", tc.text, err, tc.want)
		}
	}
}
