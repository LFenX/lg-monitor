package config

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Dashboard struct {
	ID           string           `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Title        string           `yaml:"title" json:"title" jsonschema:"minLength=1"`
	Description  string           `yaml:"description,omitempty" json:"description,omitempty" jsonschema:"maxLength=200" jsonschema_description:"一句话说明这页面板看什么，显示在面板库卡片上。"`
	Theme        string           `yaml:"theme,omitempty" json:"theme,omitempty" jsonschema:"enum=aurora,enum=midnight,enum=terminal,enum=sunset,enum=ocean,enum=forest,enum=paper" jsonschema_description:"整页配色风格，省略为 aurora。"`
	Icon         string           `yaml:"icon,omitempty" json:"icon,omitempty" jsonschema:"enum=pulse,enum=server,enum=globe,enum=cpu,enum=database,enum=shield,enum=bell,enum=layers,enum=rocket,enum=terminal" jsonschema_description:"面板库卡片图标，省略为 pulse。"`
	Refresh      Duration         `yaml:"refresh" json:"refresh,omitempty" jsonschema_description:"卡片刷新间隔，例如 10s；通过接口保存时省略为 10s。"`
	DefaultRange Duration         `yaml:"defaultRange,omitempty" json:"defaultRange,omitempty"`
	Panels       []DashboardPanel `yaml:"panels,omitempty" json:"panels,omitempty" jsonschema:"minItems=1"`
	HTML         string           `yaml:"html,omitempty" json:"html,omitempty" jsonschema:"minLength=6,pattern=^[a-z0-9-]+[.]html$"`
	HTMLContent  string           `yaml:"-" json:"-"`
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
		if len(state.Issues) == 0 && dashboard.HTML != "" {
			htmlFile := path.Join("dashboards", dashboard.HTML)
			content, err := readDashboardHTML(fsys, htmlFile)
			if err != nil {
				state.Issues.add(htmlFile, nil, err.Error(), err)
			} else {
				dashboard.HTMLContent = string(content)
				fingerprint := sha256.New()
				_, _ = fingerprint.Write(data)
				_, _ = fingerprint.Write([]byte{0})
				_, _ = fingerprint.Write(content)
				state.Version = fmt.Sprintf("%x", fingerprint.Sum(nil)[:8])
			}
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
	ID       string   `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Type     string   `yaml:"type" json:"type" jsonschema:"enum=stat,enum=gauge,enum=trend,enum=line,enum=area,enum=bar,enum=toplist,enum=pie,enum=status_grid,enum=timeline,enum=table,enum=alerts,enum=log" jsonschema_description:"stat/gauge 一行 value；trend/line/area 列 ts,value（line/area 可加 series）；bar/toplist/pie 列 label,value；status_grid 列 label,state；timeline 列 ts,label,state；alerts 列 summary,severity,state；log 列 ts,message；table 任意列。"`
	Title    string   `yaml:"title" json:"title" jsonschema:"minLength=1"`
	Query    string   `yaml:"query" json:"query" jsonschema:"minLength=1,maxLength=262144"`
	Width    int      `yaml:"width" json:"width" jsonschema:"minimum=1,maximum=12"`
	Unit     string   `yaml:"unit,omitempty" json:"unit,omitempty" jsonschema:"enum=percent,enum=bytes,enum=bytes_per_sec,enum=ms,enum=seconds,enum=count" jsonschema_description:"数值显示单位；省略时原样显示。"`
	Max      *float64 `yaml:"max,omitempty" json:"max,omitempty" jsonschema_description:"gauge 的满刻度，省略时 percent 为 100，其余按数据。"`
	Warn     *float64 `yaml:"warn,omitempty" json:"warn,omitempty" jsonschema_description:"达到此值时 stat/gauge/trend/toplist 显示为警告色。"`
	Critical *float64 `yaml:"critical,omitempty" json:"critical,omitempty" jsonschema_description:"达到此值时显示为严重色。"`
	Hint     string   `yaml:"hint,omitempty" json:"hint,omitempty" jsonschema:"maxLength=120" jsonschema_description:"卡片标题下的一句说明。"`
}

// PanelTypes lists every card type in the order shown to agents.
var PanelTypes = []string{"stat", "gauge", "trend", "line", "area", "bar", "toplist", "pie", "status_grid", "timeline", "table", "alerts", "log"}

// DashboardThemes and DashboardIcons are the presentation choices a page may name.
var (
	DashboardThemes = []string{"aurora", "midnight", "terminal", "sunset", "ocean", "forest", "paper"}
	DashboardIcons  = []string{"pulse", "server", "globe", "cpu", "database", "shield", "bell", "layers", "rocket", "terminal"}
)

// CheckDashboard applies the load-time rules to one definition before it is written.
func CheckDashboard(dashboard Dashboard) Errors {
	file := "dashboards/" + dashboard.ID + ".yaml"
	temporary := &Config{Dashboards: []Dashboard{dashboard}, dashboardFiles: []string{file}, sources: map[string]*yaml.Node{}}
	issues := temporary.validateDashboards()
	if dashboard.HTML != "" {
		issues = append(issues, Issue{File: file, Line: 1, Message: "HTML 面板需要直接放置同名 .html 文件，不能通过接口创建"})
	}
	for _, panel := range dashboard.Panels {
		if _, err := DashboardSQLParameters(panel.Query); err != nil {
			issues = append(issues, Issue{File: file, Line: 1, Message: "卡片 " + panel.ID + "：" + err.Error(), cause: err})
		}
	}
	return issues
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
		if dashboard.Theme != "" && !slices.Contains(DashboardThemes, dashboard.Theme) {
			loc.at("theme").add(&issues, "theme 须为 %s 之一", strings.Join(DashboardThemes, "、"))
		}
		if dashboard.Icon != "" && !slices.Contains(DashboardIcons, dashboard.Icon) {
			loc.at("icon").add(&issues, "icon 须为 %s 之一", strings.Join(DashboardIcons, "、"))
		}
		if dashboard.HTML != "" || nodeAt(c.sources[file], "html") != nil {
			if dashboard.HTML != dashboard.ID+".html" || !idPattern.MatchString(dashboard.ID) {
				loc.at("html").add(&issues, "HTML 文件必须是同目录、同 ID 的 <id>.html，不能使用路径或其他文件名")
			}
			if dashboard.Panels != nil || nodeAt(c.sources[file], "panels") != nil {
				loc.at("panels").add(&issues, "HTML 面板不能同时提供 panels，请只保留 html")
			}
		} else if len(dashboard.Panels) == 0 {
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
			if !slices.Contains(PanelTypes, panel.Type) {
				item.at("type").add(&issues, "面板类型须为 %s 之一", strings.Join(PanelTypes, "、"))
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

func readDashboardHTML(files fs.FS, name string) ([]byte, error) {
	info, err := fs.Stat(files, name)
	if err != nil {
		return nil, fmt.Errorf("读取 HTML 面板文件失败，请检查同目录文件是否存在及其权限：%w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxDraftFileBytes {
		return nil, fmt.Errorf("HTML 面板必须是至多 1 MiB 的普通文件")
	}
	file, err := files.Open(name)
	if err != nil {
		return nil, fmt.Errorf("打开 HTML 面板文件失败：%w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, MaxDraftFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 HTML 面板内容失败：%w", err)
	}
	if len(content) > MaxDraftFileBytes || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, fmt.Errorf("HTML 面板必须是至多 1 MiB、没有 NUL 的 UTF-8 文本")
	}
	return content, nil
}
