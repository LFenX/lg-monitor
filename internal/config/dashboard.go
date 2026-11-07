package config

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

type Dashboard struct {
	ID           string           `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Title        string           `yaml:"title" json:"title" jsonschema:"minLength=1"`
	Refresh      Duration         `yaml:"refresh" json:"refresh"`
	DefaultRange Duration         `yaml:"defaultRange,omitempty" json:"defaultRange,omitempty"`
	Panels       []DashboardPanel `yaml:"panels" json:"panels" jsonschema:"minItems=1"`
}

type DashboardPanelState struct {
	ID         string
	Parameters []string
	Error      *string
}
type DashboardState struct {
	ID, File, Version string
	Error             *string
	Panels            []DashboardPanelState
	Issues            Errors
}

func DashboardSQLParameters(query string) ([]string, error) {
	if err := ValidateReadOnlySQL(query); err != nil {
		return nil, err
	}
	tokens, _ := SQLTokens(query)
	parameters := []string{}
	for _, token := range tokens {
		if token.Kind != "parameter" {
			continue
		}
		if query[token.Start] != ':' || !oneOf(token.Text, "from", "to", "collector") {
			return nil, fmt.Errorf("查询用到了参数 %s，只能使用 :from、:to、:collector", query[token.Start:token.End])
		}
		if !slices.Contains(parameters, token.Text) {
			parameters = append(parameters, token.Text)
		}
	}
	return parameters, nil
}

func (c *Config) loadDashboards(fsys fs.FS, entries []fs.DirEntry) {
	for _, entry := range entries {
		if entry.IsDir() || !oneOf(strings.ToLower(path.Ext(entry.Name())), ".yaml", ".yml") {
			continue
		}
		file := path.Join("dashboards", entry.Name())
		state := DashboardState{ID: strings.TrimSuffix(entry.Name(), path.Ext(entry.Name())), File: file}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			state.Issues.add(file, nil, "读取面板文件失败，请检查访问权限", err)
		} else {
			sum := sha256.Sum256(data)
			state.Version = fmt.Sprintf("%x", sum[:8])
		}
		var dashboard Dashboard
		if len(state.Issues) == 0 {
			readDocument(documentOverlay{name: file, data: data}, file, &dashboard, c.sources, &state.Issues)
		}
		if len(state.Issues) == 0 {
			if strings.ToLower(path.Ext(entry.Name())) != ".yaml" {
				state.Issues.add(file, nil, "面板文件请使用 .yaml 扩展名", nil)
			}
			if dashboard.ID != state.ID {
				state.Issues.add(file, nodeAt(c.sources[file], "id"), "文件名必须与面板 id 相同", nil)
			}
			temporary := &Config{Dashboards: []Dashboard{dashboard}, dashboardFiles: []string{file}, sources: c.sources}
			state.Issues = append(state.Issues, temporary.validateDashboards()...)
		}
		if len(state.Issues) > 0 {
			message := state.Issues.Error()
			state.Error = &message
		} else {
			if dashboard.DefaultRange == "" {
				dashboard.DefaultRange = "1h"
			}
			for i, panel := range dashboard.Panels {
				meta := DashboardPanelState{ID: panel.ID}
				meta.Parameters, err = DashboardSQLParameters(panel.Query)
				if err != nil {
					issue := Issue{File: file, Line: 1, Message: err.Error(), cause: err}
					if node := nodeAt(c.sources[file], "panels", i, "query"); node != nil {
						issue.Line = node.Line
					}
					state.Issues = append(state.Issues, issue)
					message := issue.Error()
					meta.Error = &message
				}
				state.Panels = append(state.Panels, meta)
			}
			c.Dashboards = append(c.Dashboards, dashboard)
			c.dashboardFiles = append(c.dashboardFiles, file)
		}
		c.DashboardStates = append(c.DashboardStates, state)
	}
}

func (c *Config) DashboardIssues() Errors {
	var issues Errors
	for _, state := range c.DashboardStates {
		issues = append(issues, state.Issues...)
	}
	return issues
}

func (c *Config) preserveDashboards(previous *Config) {
	for i, state := range c.DashboardStates {
		if state.Error == nil {
			continue
		}
		if slices.ContainsFunc(c.Dashboards, func(d Dashboard) bool { return d.ID == state.ID }) {
			continue
		}
		for _, old := range previous.Dashboards {
			if old.ID != state.ID {
				continue
			}
			c.Dashboards = append(c.Dashboards, old)
			c.dashboardFiles = append(c.dashboardFiles, state.File)
			for _, meta := range previous.DashboardStates {
				if meta.ID == state.ID {
					c.DashboardStates[i].Version = meta.Version
					c.DashboardStates[i].Panels = meta.Panels
				}
			}
			break
		}
	}
	slices.SortFunc(c.Dashboards, func(a, b Dashboard) int { return strings.Compare(a.ID, b.ID) })
}

type DashboardPanel struct {
	ID    string `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Type  string `yaml:"type" json:"type" jsonschema:"enum=stat,enum=line,enum=bar,enum=status_grid,enum=table,enum=alerts,enum=log"`
	Title string `yaml:"title" json:"title" jsonschema:"minLength=1"`
	Query string `yaml:"query" json:"query" jsonschema:"minLength=1,maxLength=262144"`
	Width int    `yaml:"width" json:"width" jsonschema:"minimum=1,maximum=12"`
}

func (c *Config) validateDashboards() Errors {
	var issues Errors
	seen := map[string]bool{}
	for _, dashboard := range c.Dashboards {
		file := "dashboards/" + dashboard.ID + ".yaml"
		loc := location{file: file, node: c.sources[file]}
		if !idPattern.MatchString(dashboard.ID) {
			loc.at("id").add(&issues, "面板 ID 必须由小写字母、数字或横线组成且不能为空")
		}
		if seen[dashboard.ID] {
			loc.at("id").add(&issues, "面板 %s 的 ID 重复，请修改 ID", dashboard.ID)
		}
		seen[dashboard.ID] = true
		if strings.TrimSpace(dashboard.Title) == "" {
			loc.at("title").add(&issues, "面板 title 不能为空")
		}
		checkDuration(&issues, loc.at("refresh"), dashboard.Refresh, "面板刷新间隔", true)
		if dashboard.DefaultRange != "" {
			checkDuration(&issues, loc.at("defaultRange"), dashboard.DefaultRange, "默认时间范围", true)
		}
		if len(dashboard.Panels) == 0 {
			loc.at("panels").add(&issues, "面板至少需要一个 panels 条目")
		}
		panelIDs := map[string]bool{}
		for i, panel := range dashboard.Panels {
			item := loc.at("panels", i)
			if !idPattern.MatchString(panel.ID) {
				item.at("id").add(&issues, "卡片 ID 必须由小写字母、数字或横线组成且不能为空")
			}
			if panelIDs[panel.ID] {
				item.at("id").add(&issues, "卡片 %s 的 ID 在当前面板内重复", panel.ID)
			}
			panelIDs[panel.ID] = true
			if !oneOf(panel.Type, "stat", "line", "bar", "status_grid", "table", "alerts", "log") {
				item.at("type").add(&issues, "面板类型须为 stat、line、bar、status_grid、table、alerts 或 log")
			}
			if strings.TrimSpace(panel.Title) == "" {
				item.at("title").add(&issues, "面板块 title 不能为空")
			}
			if panel.Width < 1 || panel.Width > 12 {
				item.at("width").add(&issues, "面板块 width 必须为 1 到 12 的整数")
			}
		}
	}
	return issues
}
