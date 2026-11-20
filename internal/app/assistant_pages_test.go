package app

import (
	"context"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
)

func TestBohemiaOpensOnlyRealPagesAndSaysWhere(t *testing.T) {
	monitors, boards := map[string]string{"web": "官网"}, map[string]string{"main": "我的服务"}
	for raw, want := range map[string]string{
		"#/settings/access": "设置 → 访问权限", "settings/assistant": "设置 → 波西米亚", "#/": "首页", "#/lan": "内网",
		"#/boards/main": "面板「我的服务」", "#/monitors/web": "服务「官网」", "#/lan/remote/lanout-1": "别人共享的面板",
		"#/alerts/al-1/diagnostics": "告警诊断",
	} {
		if _, name, ok := sitePage(raw, monitors, boards); !ok || name != want {
			t.Fatalf("%q: %q %v", raw, name, ok)
		}
	}
	for _, raw := range []string{"#/settings/permissions", "#/boards/gone", "#/monitors/gone", "https://evil.test/", "#/lan/remote/a b", "javascript:alert(1)"} {
		if _, _, ok := sitePage(raw, monitors, boards); ok {
			t.Fatalf("opened %q", raw)
		}
	}
	var events []string
	turn := &assistantTurn{monitors: monitors, boards: boards, emit: func(event string, data any) error {
		events = append(events, event+":"+data.(map[string]string)["hash"])
		return nil
	}}
	open := turn.openPage()
	if value, err := open.run(context.Background(), nil, map[string]any{"page": "#/settings/access"}); err != nil || value.(map[string]string)["opened"] != "设置 → 访问权限" {
		t.Fatalf("value=%v err=%v", value, err)
	}
	if _, err := open.run(context.Background(), nil, map[string]any{"page": "#/settings/permissions"}); err == nil {
		t.Fatal("an unknown page opened")
	}
	if strings.Join(events, ",") != "navigate:#/settings/access" {
		t.Fatalf("events=%v", events)
	}
	// It knows the pages, and the kinds of keys apart.
	for _, known := range []string{"#/settings/access", "open_page", "内测管理", "连点三下", "assistant set", "localAccess: config"} {
		if !strings.Contains(assistantPersona, known) {
			t.Fatalf("persona lacks %q", known)
		}
	}
}

func TestBohemiaGivesTheTokenCommandForThisInstallation(t *testing.T) {
	service := installNote(api.GuideInstall{Executable: `C:\Program Files\lg-monitor\lgmon.exe`, Home: `C:\ProgramData\lg-monitor`})
	for _, want := range []string{"以管理员身份运行", `& 'C:\Program Files\lg-monitor\lgmon.exe' token create --scope config --name 网页 --home 'C:\ProgramData\lg-monitor'`, "--scope admin"} {
		if !strings.Contains(service, want) {
			t.Fatalf("service note lacks %q: %s", want, service)
		}
	}
	personal := installNote(api.GuideInstall{Executable: `D:\it's\lgmon.exe`, Home: `D:\home`, Personal: true})
	if strings.Contains(personal, "以管理员身份运行") || !strings.Contains(personal, `& 'D:\it''s\lgmon.exe' token create`) {
		t.Fatalf("personal note: %s", personal)
	}
}
