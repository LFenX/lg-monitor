// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"maps"
	"slices"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

type guestScope struct {
	boards   map[string]map[string]bool
	visible  map[string]bool
	targets  map[string]config.Target
	children map[string][]string
}

func guestScopeFor(ctx context.Context, cfg *config.Config) *guestScope {
	p, ok := api.CurrentPrincipal(ctx)
	if !ok || !p.Guest {
		return nil
	}
	return configuredGuestScope(cfg)
}

func configuredGuestScope(cfg *config.Config) *guestScope {
	lan := cfg.Settings.LAN
	if len(lan.WebBoards) == 0 && len(lan.WebServices) == 0 {
		return nil
	}
	s := &guestScope{boards: map[string]map[string]bool{}, visible: map[string]bool{}, targets: targetIndex(cfg), children: config.Children(cfg.Targets)}
	for _, b := range cfg.Boards {
		if len(lan.WebBoards) > 0 && !slices.Contains(lan.WebBoards, b.ID) {
			continue
		}
		explicit, limited := lan.WebServices[b.ID]
		members := map[string]bool{}
		for _, root := range b.Monitors {
			for _, id := range append([]string{root}, descendants(s.children, root)...) {
				if _, exists := s.targets[id]; exists && (!limited || slices.Contains(explicit, id)) {
					members[id] = true
					s.visible[id] = true
				}
			}
		}
		s.boards[b.ID] = members
	}
	return s
}

func guestForbidden() error {
	return &api.Fault{Status: 403, Code: "guest_scope", Message: "此内容未向内网访客开放"}
}

func (s *guestScope) links(edges []api.MonitorDependency) []api.MonitorDependency {
	result := slices.Clone(edges)
	for i, e := range result {
		if !s.visible[e.ID] {
			result[i].RestrictedName = firstOf(s.targets[e.ID].Name, e.ID)
			result[i].Note = ""
		}
	}
	return result
}

func (s *guestScope) monitor(m api.Monitor, visible map[string]bool) api.Monitor {
	if !visible[m.Parent] {
		m.Parent = ""
	}
	m.Children = slices.DeleteFunc(slices.Clone(m.Children), func(id string) bool { return !visible[id] })
	m.Boards = slices.DeleteFunc(slices.Clone(m.Boards), func(id string) bool { return !s.boards[id][m.ID] })
	m.Depends = s.links(m.Depends)
	m.Dependents = s.links(m.Dependents)
	// Descendant histories in the shared cache can include services outside this view.
	if slices.ContainsFunc(descendants(s.children, m.ID), func(id string) bool { return !visible[id] }) {
		m.GroupBeats = nil
		m.GroupBeatSlots = nil
		m.GroupUptime = nil
	}
	m.Depth = 0
	for parent := m.Parent; parent != "" && visible[parent] && m.Depth < len(visible); parent = s.targets[parent].Parent {
		m.Depth++
	}
	return m
}

func (s *guestScope) monitors(items []api.Monitor, visible map[string]bool) []api.Monitor {
	result := make([]api.Monitor, 0, len(items))
	for _, item := range items {
		if visible[item.ID] {
			result = append(result, s.monitor(item, visible))
		}
	}
	return result
}

func (s *guestScope) board(cfg *config.Config, board config.Board, monitors []api.Monitor, base string, withGraph bool) api.BoardView {
	allowed := s.boards[board.ID]
	all := boardItems(cfg, board, monitors)
	items := s.monitors(all, allowed)
	board.Monitors = []string{}
	board.Cards = maps.Clone(board.Cards)
	maps.DeleteFunc(board.Cards, func(id, style string) bool { return !allowed[id] })
	for _, item := range items {
		if item.Parent == "" {
			board.Monitors = append(board.Monitors, item.ID)
		}
	}
	view := api.BoardView{BoardInfo: boardInfo(board, items, base), Items: items, Unassigned: []string{}}
	if withGraph && len(all) != len(items) {
		inside := make(map[string]bool, len(all))
		for _, m := range all {
			inside[m.ID] = true
		}
		for _, m := range all {
			node := api.BoardGraphNode{ID: m.ID, Name: firstOf(m.Name, m.ID), Restricted: !s.visible[m.ID]}
			if inside[m.Parent] {
				node.Parent = m.Parent
			}
			for _, edge := range m.Depends {
				if inside[edge.ID] {
					node.Depends = append(node.Depends, api.MonitorDependency{ID: edge.ID, Relation: edge.Relation, Optional: edge.Optional})
				}
			}
			view.Graph = append(view.Graph, node)
		}
	}
	return view
}

func (b *APIBackend) guestView(ctx context.Context, cfg *config.Config, s *guestScope, op api.Operation, id string) (any, error) {
	if op == api.GetMonitor {
		if !s.visible[id] {
			return nil, guestForbidden()
		}
		raw, err := b.monitorDetail(ctx, cfg, id)
		if err != nil {
			return nil, err
		}
		d := raw.(api.MonitorDetail)
		d.Monitor = s.monitor(d.Monitor, s.visible)
		d.Children = s.monitors(d.Children, s.visible)
		d.Boards = slices.DeleteFunc(d.Boards, func(ref api.BoardRef) bool { return !s.boards[ref.ID][id] })
		d.Lineage = slices.DeleteFunc(d.Lineage, func(ref api.MonitorRef) bool { return !s.visible[ref.ID] })
		d.Incidents = slices.DeleteFunc(d.Incidents, func(i api.MonitorIncident) bool { return !s.visible[i.MonitorID] })
		return d, nil
	}
	if op == api.GetBoard && s.boards[id] == nil {
		return nil, guestForbidden()
	}
	items, err := b.monitors(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if op == api.ListMonitors {
		return s.monitors(items, s.visible), nil
	}
	base := b.baseURL(cfg)
	if op == api.GetBoard {
		board, _ := cfg.FindBoard(id)
		view := s.board(cfg, board, items, base, true)
		if err := b.addCardFacts(ctx, cfg, board, view.Items); err != nil {
			return nil, err
		}
		return view, nil
	}
	list := []api.BoardInfo{}
	for _, board := range cfg.Boards {
		if s.boards[board.ID] != nil {
			list = append(list, s.board(cfg, board, items, base, false).BoardInfo)
		}
	}
	return list, nil
}
