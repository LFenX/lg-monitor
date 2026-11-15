package config

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func treeFiles(targets, boards string) fstest.MapFS {
	files := fstest.MapFS{"targets.yaml": {Data: []byte(targets)}}
	if boards != "" {
		files["boards.yaml"] = &fstest.MapFile{Data: []byte(boards)}
	}
	return files
}

const treeTargets = `targets:
  - id: site
    match: {port: 3000}
  - id: tunnel
    parent: site
    match: {service: cloudflared}
  - id: gateway
    parent: site
    match: {process: gateway.exe}
`

func TestBoardsAndChildrenLoad(t *testing.T) {
	cfg, err := Load(treeFiles(treeTargets, "boards:\n  - id: main\n    name: 我的服务\n    monitors: [site]\n  - id: edge\n    name: 边缘\n    description: 只看隧道\n    monitors: [tunnel]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if board, ok := cfg.FindBoard("edge"); !ok || board.Description != "只看隧道" {
		t.Fatalf("board was not loaded: %+v", cfg.Boards)
	}
	children := Children(cfg.Targets)
	if strings.Join(children["site"], ",") != "tunnel,gateway" || len(children["tunnel"]) != 0 {
		t.Fatalf("children must follow configuration order: %v", children)
	}
}

func TestBoardsFileIsOptional(t *testing.T) {
	cfg, err := Load(treeFiles(treeTargets, ""))
	if err != nil || len(cfg.Boards) != 0 {
		t.Fatalf("a missing boards.yaml must load as no boards: %v %+v", err, cfg)
	}
}

func TestTreeAndBoardErrors(t *testing.T) {
	cases := []struct {
		name, targets, boards, file, want string
		line                              int
	}{
		{"self parent", "targets:\n  - id: site\n    parent: site\n    match: {port: 3000}\n", "", "targets.yaml", "不能是它自己", 3},
		{"missing parent", "targets:\n  - id: site\n    parent: nowhere\n    match: {port: 3000}\n", "", "targets.yaml", "nowhere 不存在", 3},
		{"cycle", "targets:\n  - id: a\n    parent: b\n    match: {port: 3000}\n  - id: b\n    parent: a\n    match: {port: 3001}\n", "", "targets.yaml", "循环", 3},
		{"too deep", "targets:\n  - id: a\n    match: {port: 1}\n  - id: b\n    parent: a\n    match: {port: 2}\n  - id: c\n    parent: b\n    match: {port: 3}\n  - id: d\n    parent: c\n    match: {port: 4}\n", "", "targets.yaml", "最多三层", 11},
		{"duplicate board", treeTargets, "boards:\n  - id: main\n    name: A\n  - id: main\n    name: B\n", "boards.yaml", "ID 重复", 4},
		{"empty name", treeTargets, "boards:\n  - id: main\n    name: \"  \"\n", "boards.yaml", "名称不能为空", 3},
		{"long name", treeTargets, "boards:\n  - id: main\n    name: " + strings.Repeat("长", 41) + "\n", "boards.yaml", "最多 40 个字", 3},
		{"bad id", treeTargets, "boards:\n  - id: Main\n    name: A\n", "boards.yaml", "面板 ID", 2},
		{"unknown monitor", treeTargets, "boards:\n  - id: main\n    name: A\n    monitors: [site, ghost]\n", "boards.yaml", "ghost 不存在", 4},
		{"repeated monitor", treeTargets, "boards:\n  - id: main\n    name: A\n    monitors: [site, site]\n", "boards.yaml", "重复列出", 4},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(treeFiles(test.targets, test.boards))
			var issues Errors
			if !errors.As(err, &issues) {
				t.Fatalf("expected configuration issues, got %v", err)
			}
			for _, issue := range issues {
				if issue.File == test.file && strings.Contains(issue.Message, test.want) {
					if issue.Line != test.line {
						t.Fatalf("issue %q reported on line %d, want %d", issue.Message, issue.Line, test.line)
					}
					return
				}
			}
			t.Fatalf("missing %q in %s: %v", test.want, test.file, err)
		})
	}
}

func TestTargetConfigKeepsParentOnMonitor(t *testing.T) {
	cfg := &Config{Targets: []Target{{ID: "site", Match: Match{Process: "node.exe"}}, {ID: "tunnel", Parent: "site", Match: Match{Service: "cloudflared"}}}}
	_, data, err := TargetConfig(cfg, "fake-id", "fake-host")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "parent") {
		t.Fatalf("collector configuration must not carry monitor-only fields: %s", data)
	}
	if cfg.Targets[1].Parent != "site" {
		t.Fatal("stripping the collector copy must not change the monitor configuration")
	}
}
