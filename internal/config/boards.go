package config

import (
	"strings"
	"unicode/utf8"
)

// MaxMonitorDepth keeps a board readable: a service, its parts, and their parts.
const MaxMonitorDepth = 3

const (
	MaxBoardName        = 40
	MaxBoardDescription = 200
	MaxBoardID          = 48
)

// FindBoard returns the board with the given ID.
func (c *Config) FindBoard(id string) (Board, bool) {
	for _, board := range c.Boards {
		if board.ID == id {
			return board, true
		}
	}
	return Board{}, false
}

// Children lists each target's direct children in configuration order.
func Children(targets []Target) map[string][]string {
	known := make(map[string]bool, len(targets))
	for _, target := range targets {
		known[target.ID] = true
	}
	result := map[string][]string{}
	for _, target := range targets {
		if target.Parent != "" && target.Parent != target.ID && known[target.Parent] {
			result[target.Parent] = append(result[target.Parent], target.ID)
		}
	}
	return result
}

// CheckBoard reports the first problem with a board's own fields; references are checked by Validate.
func CheckBoard(board Board) string {
	switch {
	case !idPattern.MatchString(board.ID) || len(board.ID) > MaxBoardID:
		return "面板 ID 必须由小写字母、数字或横线组成，最长 48 个字符"
	case strings.TrimSpace(board.Name) == "":
		return "面板名称不能为空"
	case utf8.RuneCountInString(board.Name) > MaxBoardName:
		return "面板名称最多 40 个字"
	case utf8.RuneCountInString(board.Description) > MaxBoardDescription:
		return "面板简介最多 200 个字"
	}
	return ""
}

func validateTree(cfg *Config, root func(string) location, issues *Errors) {
	targets := make(map[string]Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets[target.ID] = target
	}
	for i, target := range cfg.Targets {
		if target.Parent == "" {
			continue
		}
		loc := root("targets.yaml").at("targets", i, "parent")
		if target.Parent == target.ID {
			loc.add(issues, "目标 %s 的 parent 不能是它自己", target.ID)
			continue
		}
		if _, ok := targets[target.Parent]; !ok {
			loc.add(issues, "目标 %s 的 parent %s 不存在，请先添加主服务或改正 ID", target.ID, target.Parent)
			continue
		}
		depth, seen, cycle := 1, map[string]bool{target.ID: true}, false
		for current := target.Parent; current != ""; current = targets[current].Parent {
			if seen[current] {
				cycle = true
				break
			}
			seen[current] = true
			depth++
		}
		switch {
		case cycle:
			loc.add(issues, "目标 %s 的 parent 形成了循环，子服务不能反过来做主服务的上级", target.ID)
		case depth > MaxMonitorDepth:
			loc.add(issues, "目标 %s 嵌套过深：子服务最多三层（主服务、子服务、子服务的子服务）", target.ID)
		}
	}
	boards := map[string]bool{}
	for i, board := range cfg.Boards {
		loc := root("boards.yaml").at("boards", i)
		if problem := CheckBoard(board); problem != "" {
			field := "name"
			if strings.HasPrefix(problem, "面板 ID") {
				field = "id"
			} else if strings.HasPrefix(problem, "面板简介") {
				field = "description"
			}
			loc.at(field).add(issues, "%s", problem)
		}
		if boards[board.ID] {
			loc.at("id").add(issues, "面板 %s 的 ID 重复，请修改 ID", board.ID)
		}
		boards[board.ID] = true
		listed := map[string]bool{}
		for j, id := range board.Monitors {
			item := loc.at("monitors", j)
			if listed[id] {
				item.add(issues, "面板 %s 重复列出了监控 %s", board.ID, id)
			}
			listed[id] = true
			if _, ok := targets[id]; !ok {
				item.add(issues, "面板 %s 列出的监控 %s 不存在，请删除这一项或先添加监控", board.ID, id)
			}
		}
	}
}
