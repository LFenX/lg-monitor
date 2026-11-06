package collector

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/config"
)

type scriptRunnerFunc func(context.Context, ScriptCommand) (ScriptOutput, error)

func (f scriptRunnerFunc) Run(ctx context.Context, command ScriptCommand) (ScriptOutput, error) {
	return f(ctx, command)
}

func TestScriptProbeSuccessFailureTimeoutAndInvalidOutput(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		exit         uint32
		err          error
		ok           bool
		wantError    string
	}{
		{"success", "progress\n{\"ok\":true,\"message\":\"最近备份两小时\",\"metrics\":{\"backup_age_hours\":2}}\r\n", 0, nil, true, ""},
		{"check failed", `{"ok":false,"message":"备份过旧","metrics":{"age":26}}`, 0, nil, false, "备份过旧"},
		{"exit failed", `{"ok":true}`, 7, nil, false, "退出码为 7"},
		{"timeout", "still running", 0, context.DeadlineExceeded, false, "超时"},
		{"malformed", "not json", 0, nil, false, "不是约定格式"},
		{"missing ok", `{"message":"missing"}`, 0, nil, false, "布尔 ok"},
		{"string metric", `{"ok":true,"metrics":{"x":"3"}}`, 0, nil, false, "必须是数字"},
		{"null metric", `{"ok":true,"metrics":{"x":null}}`, 0, nil, false, "不能使用字符串或 null"},
		{"infinite metric", `{"ok":true,"metrics":{"x":1e999}}`, 0, nil, false, "指标名称或数值无效"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prober := newProber(nil, nil, func() time.Time { return time.UnixMilli(123000) })
			defer prober.Close()
			prober.scripts = scriptRunnerFunc(func(ctx context.Context, command ScriptCommand) (ScriptOutput, error) {
				if command.Name != "fake.ps1" || len(command.Args) != 1 || command.Args[0] != "safe argument" {
					t.Fatal("script command changed")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("script has no timeout")
				}
				return ScriptOutput{Stdout: []byte(tc.output), Tail: tc.output, ExitCode: tc.exit}, tc.err
			})
			result := prober.Execute(context.Background(), "fake-script", 2, config.Probe{Type: "script", Script: "fake.ps1", Args: []string{"safe argument"}, Interval: "1m", Timeout: "5s"})
			if result.OK != tc.ok || result.Type != "script" || result.TargetID != "fake-script" || result.Probe != 2 || !strings.Contains(result.Error, tc.wantError) {
				t.Fatalf("script result: %+v", result)
			}
			if tc.name == "success" && (result.Message != "最近备份两小时" || result.Metrics["backup_age_hours"] != 2) {
				t.Fatalf("missing script message/metrics: %+v", result)
			}
		})
	}
}

func TestScriptDiagnosticKeepsLast20LinesAndOutputBuffersAreBounded(t *testing.T) {
	var output strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&output, "line-%02d\n", i)
	}
	prober := newProber(nil, nil, func() time.Time { return time.UnixMilli(1) })
	defer prober.Close()
	prober.scripts = scriptRunnerFunc(func(context.Context, ScriptCommand) (ScriptOutput, error) {
		return ScriptOutput{Tail: output.String(), ExitCode: 1}, nil
	})
	result := prober.Execute(context.Background(), "fake", 0, config.Probe{Type: "script", Script: "fake.ps1", Timeout: "1s"})
	if strings.Contains(result.Error, "line-29") || !strings.Contains(result.Error, "line-30") || !strings.Contains(result.Error, "line-49") {
		t.Fatalf("wrong diagnostic tail: %s", result.Error)
	}
	var capture scriptCapture
	capture.write([]byte(strings.Repeat("a", 4*MaxScriptOutput)), true)
	capture.write([]byte("\n{\"ok\":true}\n"), true)
	snapshot := capture.output(0)
	if len(snapshot.Stdout) > MaxScriptOutput || len(snapshot.Tail) > MaxScriptDiagnostic || !strings.HasSuffix(string(snapshot.Stdout), "{\"ok\":true}\n") {
		t.Fatal("script output grew without bound or lost final JSON")
	}
	for _, value := range []string{strings.Repeat("中文", MaxScriptDiagnostic), strings.Repeat("\xffx", MaxScriptDiagnostic)} {
		diagnostic := lastLines(value, 20)
		if len(diagnostic) > MaxScriptDiagnostic || !utf8.ValidString(diagnostic) {
			t.Fatal("diagnostic truncation broke UTF-8 or exceeded byte limit")
		}
	}
}
