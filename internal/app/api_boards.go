package app

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/config"
	"gopkg.in/yaml.v3"
)

// DefaultBoard is created on first use, so a fresh install never asks where a monitor should go.
var DefaultBoard = config.Board{ID: "main", Name: "我的服务"}

var stateRank = map[string]int{"": 0, "up": 1, "unknown": 2, "down": 3}

func worse(a, b string) string {
	if stateRank[b] > stateRank[a] {
		return b
	}
	return a
}

// ancestors lists a target's parents from the nearest up, stopping at a cycle or missing link.
func ancestors(targets map[string]config.Target, id string) []string {
	var result []string
	seen := map[string]bool{id: true}
	for current := targets[id].Parent; current != "" && !seen[current]; current = targets[current].Parent {
		if _, ok := targets[current]; !ok {
			break
		}
		seen[current] = true
		result = append(result, current)
	}
	return result
}

func descendants(children map[string][]string, id string) []string {
	var result []string
	seen := map[string]bool{id: true}
	var walk func(string)
	walk = func(current string) {
		for _, child := range children[current] {
			if !seen[child] {
				seen[child] = true
				result = append(result, child)
				walk(child)
			}
		}
	}
	walk(id)
	return result
}

func targetIndex(cfg *config.Config) map[string]config.Target {
	result := make(map[string]config.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		result[target.ID] = target
	}
	return result
}

// Only the explicitly named group history spans descendants. Every monitor's
// state and issue describe its own checks, at every depth of the tree.
func decorate(cfg *config.Config, items []api.Monitor, base string) {
	targets := targetIndex(cfg)
	children := config.Children(cfg.Targets)
	listedOn := map[string][]string{}
	for _, board := range cfg.Boards {
		for _, id := range board.Monitors {
			listedOn[id] = append(listedOn[id], board.ID)
		}
	}
	byID := make(map[string]*api.Monitor, len(items))
	for i := range items {
		item := &items[i]
		byID[item.ID] = item
		if parent := targets[item.ID].Parent; parent != "" {
			if _, ok := targets[parent]; ok {
				item.Parent = parent
			}
		}
		item.Children = append([]string{}, children[item.ID]...)
		item.Boards = append([]string{}, listedOn[item.ID]...)
		item.Depth = len(ancestors(targets, item.ID))
		item.MonitorURL = base + "/#/monitors/" + url.PathEscape(item.ID)
	}
	for i := range items {
		item := &items[i]
		item.Overall = item.State
		below := descendants(children, item.ID)
		if len(below) == 0 {
			continue
		}
		beats := slices.Clone(item.Beats)
		slots := slices.Clone(item.BeatSlots)
		for index := range slots {
			slots[index].Segments = slices.Clone(slots[index].Segments)
		}
		uptime := item.Uptime
		for _, id := range below {
			child, ok := byID[id]
			if !ok {
				continue
			}
			for index := range beats {
				if index < len(child.Beats) {
					beats[index] = worse(beats[index], child.Beats[index])
				}
			}
			for index := range slots {
				if index < len(child.BeatSlots) {
					other := child.BeatSlots[index]
					slots[index].State = worse(slots[index].State, other.State)
					if other.Uptime != nil && (slots[index].Uptime == nil || *other.Uptime < *slots[index].Uptime) {
						slots[index].Uptime = other.Uptime
					}
					slots[index].Segments = append(slots[index].Segments, other.Segments...)
				}
			}
			if child.Uptime != nil && (uptime == nil || *child.Uptime < *uptime) {
				value := *child.Uptime
				uptime = &value
			}
		}
		item.GroupBeats, item.GroupUptime = beats, uptime
		item.GroupBeatSlots = slots
	}
}

// boardItems expands a board into display order: each listed service followed by its subtree.
// A listed monitor whose ancestor is also on the board is shown under that ancestor instead.
func boardItems(cfg *config.Config, board config.Board, monitors []api.Monitor) []api.Monitor {
	targets := targetIndex(cfg)
	byID := make(map[string]api.Monitor, len(monitors))
	for _, item := range monitors {
		byID[item.ID] = item
	}
	listed := map[string]bool{}
	for _, id := range board.Monitors {
		listed[id] = true
	}
	shown := map[string]bool{}
	items := []api.Monitor{}
	var add func(string, int)
	add = func(id string, depth int) {
		item, ok := byID[id]
		if !ok || shown[id] {
			return
		}
		shown[id] = true
		item.Depth = depth
		items = append(items, item)
		for _, child := range item.Children {
			add(child, depth+1)
		}
	}
	for _, id := range board.Monitors {
		if slices.ContainsFunc(ancestors(targets, id), func(parent string) bool { return listed[parent] }) {
			continue
		}
		add(id, 0)
	}
	return items
}

func summarize(items []api.Monitor) api.BoardSummary {
	summary := api.BoardSummary{State: "empty"}
	var total float64
	var counted int
	worst := ""
	for _, item := range items {
		if item.Depth == 0 {
			summary.Services++
		} else {
			summary.Children++
		}
		switch item.State {
		case "up":
			summary.Up++
		case "down":
			summary.Down++
		default:
			summary.Unknown++
		}
		worst = worse(worst, item.State)
		if item.Uptime != nil {
			total += *item.Uptime
			counted++
		}
	}
	if worst != "" {
		summary.State = worst
	}
	if counted > 0 {
		average := total / float64(counted)
		summary.Uptime = &average
	}
	return summary
}

func boardInfo(board config.Board, items []api.Monitor, base string) api.BoardInfo {
	style := board.CardStyle
	if style == "" {
		style = config.CardStyles[0]
	}
	return api.BoardInfo{ID: board.ID, Name: board.Name, Description: board.Description, Monitors: append([]string{}, board.Monitors...), Summary: summarize(items), URL: base + "/#/boards/" + url.PathEscape(board.ID), CardStyle: style, Cards: maps.Clone(board.Cards)}
}

// unassigned lists top-level monitors that no board shows, so nothing silently disappears from view.
func unassigned(cfg *config.Config) []string {
	listed := map[string]bool{}
	for _, board := range cfg.Boards {
		for _, id := range board.Monitors {
			listed[id] = true
		}
	}
	targets := targetIndex(cfg)
	result := []string{}
	for _, target := range cfg.Targets {
		if listed[target.ID] || slices.ContainsFunc(ancestors(targets, target.ID), func(parent string) bool { return listed[parent] }) {
			continue
		}
		if parent := target.Parent; parent != "" {
			if _, ok := targets[parent]; ok {
				continue
			}
		}
		result = append(result, target.ID)
	}
	return result
}

func (b *APIBackend) boardList(ctx context.Context, cfg *config.Config) ([]api.BoardInfo, error) {
	monitors, err := b.monitors(ctx, cfg)
	if err != nil {
		return nil, err
	}
	base := b.baseURL(cfg)
	result := make([]api.BoardInfo, 0, len(cfg.Boards))
	for _, board := range cfg.Boards {
		result = append(result, boardInfo(board, boardItems(cfg, board, monitors), base))
	}
	return result, nil
}

func (b *APIBackend) boardView(ctx context.Context, cfg *config.Config, id string) (api.BoardView, error) {
	board, ok := cfg.FindBoard(id)
	if !ok {
		return api.BoardView{}, missingAPI("面板 " + id + " 不存在，先调用 list_boards 核对 ID")
	}
	monitors, err := b.monitors(ctx, cfg)
	if err != nil {
		return api.BoardView{}, err
	}
	items := boardItems(cfg, board, monitors)
	if err := b.addCardFacts(ctx, cfg, board, items); err != nil {
		return api.BoardView{}, err
	}
	return api.BoardView{BoardInfo: boardInfo(board, items, b.baseURL(cfg)), Items: items, Unassigned: unassigned(cfg)}, nil
}

// cardFacts are the slower figures some card styles show; they change slowly, so a minute-old copy is fine.
type cardFacts struct {
	at                  time.Time
	days                []api.StatusSlot
	uptime7d, uptime30d *float64
	latency             []api.LatencyPoint
}

const cardFactsAge = time.Minute

// addCardFacts fills the 30-day and latency figures only for top-level services whose style shows them.
func (b *APIBackend) addCardFacts(ctx context.Context, cfg *config.Config, board config.Board, items []api.Monitor) error {
	targets := targetIndex(cfg)
	now := b.options.Now()
	for index := range items {
		item := &items[index]
		style := board.StyleOf(item.ID)
		if item.Depth > 0 || (style != "ring" && style != "days" && style != "latency") {
			continue
		}
		target, ok := targets[item.ID]
		if !ok {
			continue
		}
		key := item.ID + "|" + style
		b.cardsMu.Lock()
		facts, fresh := b.cards[key]
		b.cardsMu.Unlock()
		if !fresh || now.Sub(facts.at) > cardFactsAge || now.Before(facts.at) {
			facts = cardFacts{at: now}
			if style == "latency" {
				if len(target.Probes) > 0 {
					since := now.Add(-detailSlots * detailSlot).UnixMilli()
					points, _, err := b.latencyHistory(ctx, target, since, detailSlot.Milliseconds())
					if err != nil {
						return err
					}
					facts.latency = points
				}
			} else {
				history, err := b.dayHistory(ctx, target, now, false)
				if err != nil {
					return err
				}
				facts.days, facts.uptime7d, facts.uptime30d = history.days, history.uptime7d, history.uptime30d
			}
			b.cardsMu.Lock()
			if b.cards == nil {
				b.cards = map[string]cardFacts{}
			}
			b.cards[key] = facts
			b.cardsMu.Unlock()
		}
		item.Days, item.Uptime7d, item.Uptime30d, item.Latency = facts.days, facts.uptime7d, facts.uptime30d, facts.latency
		if style == "latency" && item.Latency == nil {
			item.Latency = []api.LatencyPoint{}
		}
	}
	return nil
}

func (b *APIBackend) editBoards(ctx context.Context, change func([]config.Board) ([]config.Board, error)) error {
	_, err := b.options.Editor.Update(ctx, "boards.yaml", func(old []byte) ([]byte, error) {
		var file config.BoardsFile
		if err := yaml.Unmarshal(old, &file); err != nil {
			return nil, fmt.Errorf("读取现有面板失败：%w", err)
		}
		boards, err := change(file.Boards)
		if err != nil {
			return nil, err
		}
		file.Boards = boards
		return encodeYAML(file)
	})
	return err
}

func missingMonitors(cfg *config.Config, ids []string) []string {
	targets := targetIndex(cfg)
	var missing []string
	for _, id := range ids {
		if _, ok := targets[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

// newBoardID derives a readable ID from the name, falling back to a counter for names without ASCII.
func newBoardID(cfg *config.Config, name string) string {
	base := blueprint.Slug(name)
	if base == "" {
		if _, taken := cfg.FindBoard(DefaultBoard.ID); !taken {
			return DefaultBoard.ID
		}
		base = "board"
	}
	for n := 1; ; n++ {
		candidate := base
		if n > 1 || base == "board" {
			candidate = base + "-" + strconv.Itoa(n)
		}
		if _, taken := cfg.FindBoard(candidate); !taken {
			return candidate
		}
	}
}

// createBoard adds a board; the first board gathers monitors that no board shows yet,
// which is how installs from before boards existed keep seeing their monitors.
func (b *APIBackend) createBoard(ctx context.Context, cfg *config.Config, input api.BoardCreateInput) (config.Board, error) {
	board := config.Board{ID: strings.TrimSpace(input.ID), Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description)}
	if board.ID == "" {
		board.ID = newBoardID(cfg, board.Name)
	} else if _, taken := cfg.FindBoard(board.ID); taken {
		return board, &api.Fault{Status: 409, Code: "board_exists", Message: "面板 " + board.ID + " 已经存在；修改它请用 update_board"}
	}
	if problem := config.CheckBoard(board); problem != "" {
		return board, invalidAPI(problem)
	}
	if len(cfg.Boards) == 0 {
		board.Monitors = unassigned(cfg)
	}
	err := b.editBoards(ctx, func(items []config.Board) ([]config.Board, error) {
		return append(items, board), nil
	})
	return board, err
}

func (b *APIBackend) updateBoard(ctx context.Context, cfg *config.Config, id string, input api.BoardUpdateInput) error {
	board, ok := cfg.FindBoard(id)
	if !ok {
		return missingAPI("面板 " + id + " 不存在，先调用 list_boards 核对 ID")
	}
	if input.Name != nil {
		board.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		board.Description = strings.TrimSpace(*input.Description)
	}
	if input.Monitors != nil {
		board.Monitors = slices.Compact(slices.Clone(input.Monitors))
	}
	for _, monitor := range input.Add {
		if !slices.Contains(board.Monitors, monitor) {
			board.Monitors = append(board.Monitors, monitor)
		}
	}
	board.Monitors = slices.DeleteFunc(board.Monitors, func(monitor string) bool { return slices.Contains(input.Remove, monitor) })
	if input.CardStyle != nil {
		board.CardStyle = strings.TrimSpace(*input.CardStyle)
		if board.CardStyle == config.CardStyles[0] {
			board.CardStyle = ""
		}
	}
	if len(input.Cards) > 0 {
		board.Cards = maps.Clone(board.Cards)
		if board.Cards == nil {
			board.Cards = map[string]string{}
		}
		var missing []string
		for id, style := range input.Cards {
			if _, ok := targetIndex(cfg)[id]; !ok {
				missing = append(missing, id)
			}
			if style = strings.TrimSpace(style); style == "" {
				delete(board.Cards, id)
			} else {
				board.Cards[id] = style
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			return invalidAPI("这些监控不存在：" + strings.Join(missing, "、") + "；先调用 list_monitors 核对 ID")
		}
	}
	// A service set to the board's default simply follows it, so only real exceptions are written down.
	if len(board.Cards) > 0 {
		board.Cards = maps.Clone(board.Cards)
		maps.DeleteFunc(board.Cards, func(_, style string) bool { return style == board.StyleOf("") })
		if len(board.Cards) == 0 {
			board.Cards = nil
		}
	}
	if missing := missingMonitors(cfg, board.Monitors); len(missing) > 0 {
		return invalidAPI("这些监控不存在：" + strings.Join(missing, "、") + "；先调用 list_monitors 核对 ID")
	}
	if problem := config.CheckBoard(board); problem != "" {
		return invalidAPI(problem)
	}
	return b.editBoards(ctx, func(items []config.Board) ([]config.Board, error) {
		for index := range items {
			if items[index].ID == id {
				items[index] = board
			}
		}
		return items, nil
	})
}

// guestWebScope returns the allowed board and monitor IDs when a LAN web guest is restricted to
// a subset of boards via settings.lan.webBoards. When unrestricted, restricted is false.
func guestWebScope(ctx context.Context, cfg *config.Config) (restricted bool, boards map[string]bool, monitors map[string]bool) {
	principal, ok := api.CurrentPrincipal(ctx)
	if !ok || !principal.Guest {
		return false, nil, nil
	}
	allowed := cfg.Settings.LAN.WebBoards
	if len(allowed) == 0 {
		return false, nil, nil
	}
	boards = make(map[string]bool, len(allowed))
	for _, id := range allowed {
		boards[id] = true
	}
	monitors = map[string]bool{}
	for _, b := range cfg.Boards {
		if !boards[b.ID] {
			continue
		}
		for _, mid := range b.Monitors {
			monitors[mid] = true
		}
	}
	// Include all descendant sub-services of allowed monitors.
	changed := true
	for changed {
		changed = false
		for _, t := range cfg.Targets {
			if t.Parent != "" && monitors[t.Parent] && !monitors[t.ID] {
				monitors[t.ID] = true
				changed = true
			}
		}
	}
	return true, boards, monitors
}

func (b *APIBackend) boardOperation(ctx context.Context, cfg *config.Config, op api.Operation, r api.Request) (any, error) {
	id := r.Params["id"]
	restricted, allowedBoards, _ := guestWebScope(ctx, cfg)
	switch op {
	case api.ListBoards:
		list, err := b.boardList(ctx, cfg)
		if err != nil || !restricted {
			return list, err
		}
		filtered := make([]api.BoardInfo, 0, len(list))
		for _, item := range list {
			if allowedBoards[item.ID] {
				filtered = append(filtered, item)
			}
		}
		return filtered, nil
	case api.GetBoard:
		if restricted && !allowedBoards[id] {
			return nil, &api.Fault{Status: 403, Code: "forbidden_board", Message: "此面板未对内网访客开放"}
		}
		view, err := b.boardView(ctx, cfg, id)
		if err != nil || !restricted {
			return view, err
		}
		// Never expose unassigned services from outside the guest's allowed boards.
		view.Unassigned = nil
		return view, nil
	case api.CreateBoard:
		var input api.BoardCreateInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		board, err := b.createBoard(ctx, cfg, input)
		if err != nil {
			return nil, err
		}
		return b.boardView(ctx, b.options.Config.Snapshot(), board.ID)
	case api.UpdateBoard:
		var input api.BoardUpdateInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		if err := b.updateBoard(ctx, cfg, id, input); err != nil {
			return nil, err
		}
		return b.boardView(ctx, b.options.Config.Snapshot(), id)
	case api.DeleteBoard:
		board, ok := cfg.FindBoard(id)
		if !ok {
			return nil, missingAPI("面板 " + id + " 不存在，先调用 list_boards 核对 ID")
		}
		if err := b.editBoards(ctx, func(items []config.Board) ([]config.Board, error) {
			return slices.DeleteFunc(items, func(item config.Board) bool { return item.ID == id }), nil
		}); err != nil {
			return nil, err
		}
		return api.Confirmation{OK: true, Message: fmt.Sprintf("面板 %s 已删除，上面的 %d 个监控保留", board.Name, len(board.Monitors))}, nil
	}
	return nil, fmt.Errorf("面板操作 %s 未实现", op)
}

// placeMonitor decides which board shows a monitor after it is saved. Children follow their
// parent; anything else goes where it was asked, or onto the first board, creating one if needed.
func (b *APIBackend) placeMonitor(ctx context.Context, id, requested string) (string, bool, error) {
	cfg := b.options.Config.Snapshot()
	targets := targetIndex(cfg)
	lineage := append([]string{id}, ancestors(targets, id)...)
	shows := func(board config.Board) bool {
		return slices.ContainsFunc(lineage, func(item string) bool { return slices.Contains(board.Monitors, item) })
	}
	tidy := func(items []config.Board) []config.Board {
		// A monitor that became a child no longer needs its own line where its parent is listed.
		for index := range items {
			if slices.ContainsFunc(lineage[1:], func(parent string) bool { return slices.Contains(items[index].Monitors, parent) }) {
				items[index].Monitors = slices.DeleteFunc(items[index].Monitors, func(item string) bool { return item == id })
			}
		}
		return items
	}
	if requested != "" {
		board, ok := cfg.FindBoard(requested)
		if !ok {
			return "", false, missingAPI("面板 " + requested + " 不存在")
		}
		return requested, false, b.editBoards(ctx, func(items []config.Board) ([]config.Board, error) {
			items = tidy(items)
			if !shows(board) {
				for index := range items {
					if items[index].ID == requested {
						items[index].Monitors = append(items[index].Monitors, id)
					}
				}
			}
			return items, nil
		})
	}
	for _, board := range cfg.Boards {
		if shows(board) {
			if len(lineage) > 1 && slices.ContainsFunc(cfg.Boards, func(item config.Board) bool { return slices.Contains(item.Monitors, id) }) {
				return board.ID, false, b.editBoards(ctx, func(items []config.Board) ([]config.Board, error) { return tidy(items), nil })
			}
			return board.ID, false, nil
		}
	}
	if len(cfg.Boards) == 0 {
		board, err := b.createBoard(ctx, cfg, api.BoardCreateInput{ID: DefaultBoard.ID, Name: DefaultBoard.Name})
		return board.ID, err == nil, err
	}
	// Listing the topmost ancestor brings the whole family onto the board, not a lone child.
	first, root := cfg.Boards[0].ID, lineage[len(lineage)-1]
	return first, false, b.editBoards(ctx, func(items []config.Board) ([]config.Board, error) {
		for index := range items {
			if items[index].ID == first {
				items[index].Monitors = append(items[index].Monitors, root)
			}
		}
		return items, nil
	})
}
