// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"slices"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestGuestServicesAcrossBoardsAndNestedTargets(t *testing.T) {
	cfg := &config.Config{
		Boards: []config.Board{
			{ID: "a", Monitors: []string{"root"}},
			{ID: "b", Monitors: []string{"child"}},
		},
		Targets: []config.Target{
			{ID: "root"}, {ID: "child", Parent: "root"}, {ID: "db", Name: "Database", Parent: "child"},
		},
	}
	cfg.Settings.LAN.WebBoards = []string{"a"}
	s := configuredGuestScope(cfg)
	if !s.visible["db"] || s.boards["b"] != nil {
		t.Fatal("existing board-only configuration changed")
	}
	cfg.Settings.LAN.WebServices = map[string][]string{"a": {"child"}}
	s = configuredGuestScope(cfg)
	input := []api.Monitor{
		{ID: "root", Children: []string{"child"}},
		{ID: "child", Parent: "root", Children: []string{"db"}, Depends: []api.MonitorDependency{{ID: "db", Relation: "reads", Note: "private note"}}},
		{ID: "db", Name: "Database", Parent: "child"},
	}
	view := s.board(cfg, cfg.Boards[0], input, "", true)
	if len(view.Items) != 1 || view.Items[0].Parent != "" || view.Items[0].Depth != 0 || len(view.Items[0].Children) != 0 || view.Summary.Services != 1 || view.Unassigned == nil || !slices.Equal(view.Monitors, []string{"child"}) {
		t.Fatalf("unexpected shared board: %+v", view)
	}
	if len(view.Graph) != 3 || !view.Graph[0].Restricted || !view.Graph[2].Restricted || view.Graph[1].Parent != "root" || view.Graph[2].Parent != "child" {
		t.Fatal("hidden parent/child topology was lost")
	}
	link := view.Items[0].Depends[0]
	if link.RestrictedName != "Database" || link.Note != "" || input[1].Depends[0].Note != "private note" {
		t.Fatal("dependency projection changed shared data or exposed private fields")
	}
	cfg.Settings.LAN.WebBoards = nil
	s = configuredGuestScope(cfg)
	if !s.visible["db"] || s.boards["a"]["db"] || !s.boards["b"]["db"] {
		t.Fatal("overlapping boards lost their individual grants")
	}
	patch := map[string][]string{"b": {}}
	lan := applyLANInput(cfg.Settings.LAN, api.LANConfigInput{WebServices: &patch})
	cfg.Settings.LAN = lan
	if scoped := configuredGuestScope(cfg); scoped.visible["root"] || scoped.visible["db"] || !scoped.visible["child"] {
		t.Fatal("empty selection granted services or replaced another board's choices")
	}
	patch["a"] = nil
	cfg.Settings.LAN = applyLANInput(lan, api.LANConfigInput{WebServices: &patch})
	if _, exists := cfg.Settings.LAN.WebServices["a"]; exists || !configuredGuestScope(cfg).visible["root"] {
		t.Fatal("whole-board sharing was not restored")
	}
}
