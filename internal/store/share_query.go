package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"modernc.org/sqlite"
)

const ShareResultLimit = 16 * 1024 * 1024

var ErrShareDimension = errors.New("这张卡片用到的数据不能按所选主机或目标过滤")
var ErrShareResultSize = errors.New("分享结果超过 16 MiB，请负责人缩小卡片查询")

type shareObject struct {
	collector, target string
	dependencies      []string
}

// Views need their own predicates: an outer CTE does not filter their base tables.
var shareObjects = map[string]shareObject{
	"card_action_events":   {collector: "collector_id", target: "target_id"},
	"collectors":           {collector: "id"},
	"target_status":        {collector: "collector_id", target: "target_id"},
	"probe_results":        {collector: "collector_id", target: "target_id"},
	"process_samples":      {collector: "collector_id", target: "target_id"},
	"script_metrics":       {collector: "collector_id", target: "target_id"},
	"alerts":               {collector: "collector_id", target: "target_id"},
	"system_metrics":       {collector: "collector_id"},
	"disk_metrics":         {collector: "collector_id"},
	"network_metrics":      {collector: "collector_id"},
	"service_status":       {collector: "collector_id"},
	"collector_events":     {collector: "collector_id"},
	"metric_minute":        {collector: "collector_id", target: "target_id"},
	"metric_hour":          {collector: "collector_id", target: "target_id"},
	"probe_minute":         {collector: "collector_id", target: "target_id"},
	"probe_hour":           {collector: "collector_id", target: "target_id"},
	"eventlog_events":      {collector: "collector_id"},
	"eventlog_matches":     {collector: "collector_id", target: "name"},
	"inventory_current":    {collector: "collector_id"},
	"inventory_scopes":     {collector: "collector_id"},
	"inventory_snapshots":  {collector: "collector_id"},
	"inventory_changes":    {collector: "collector_id"},
	"v_target_current":     {collector: "collector_id", target: "target_id", dependencies: []string{"target_status"}},
	"v_collectors":         {collector: "collector_id", dependencies: []string{"collectors"}},
	"v_alerts_active":      {collector: "collector_id", target: "target_id", dependencies: []string{"alerts"}},
	"v_probe_certificates": {collector: "collector_id", target: "target_id", dependencies: []string{"probe_results"}},
	"v_eventlog_targets":   {collector: "collector_id", target: "target_id", dependencies: []string{"eventlog_events", "eventlog_matches"}},
}

func shareSQLTokens(query string, names map[string]bool) ([]config.SQLToken, error) {
	if _, err := config.DashboardSQLParameters(query); err != nil {
		return nil, err
	}
	tokens, _ := config.SQLTokens(query)
	for i, t := range tokens {
		name := strings.ToLower(t.Text)
		if (name == "main" || name == "temp") && i+1 < len(tokens) && tokens[i+1].Text == "." {
			return nil, fmt.Errorf("分享查询不能指定 main. 或 temp. 库名")
		}
		if t.Kind != "word" || name != "with" {
			continue
		}
		j := i + 1
		if j < len(tokens) && strings.EqualFold(tokens[j].Text, "recursive") {
			j++
		}
		for j < len(tokens) {
			if names[strings.ToLower(tokens[j].Text)] {
				return nil, fmt.Errorf("分享查询不能用保留对象名定义 CTE")
			}
			j++
			if j < len(tokens) && tokens[j].Text == "(" {
				j = shareClosing(tokens, j) + 1
			}
			if j >= len(tokens) || !strings.EqualFold(tokens[j].Text, "as") {
				break
			}
			j++
			if j < len(tokens) && strings.EqualFold(tokens[j].Text, "not") {
				j++
			}
			if j < len(tokens) && strings.EqualFold(tokens[j].Text, "materialized") {
				j++
			}
			if j >= len(tokens) || tokens[j].Text != "(" {
				break
			}
			j = shareClosing(tokens, j) + 1
			if j >= len(tokens) || tokens[j].Text != "," {
				break
			}
			j++
		}
	}
	return tokens, nil
}

func shareClosing(tokens []config.SQLToken, start int) int {
	depth := 0
	for i := start; i < len(tokens); i++ {
		if tokens[i].Kind != "punctuation" {
			continue
		}
		switch tokens[i].Text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(tokens)
}

func shareAllowed(scope ShareScope) map[string]bool {
	allowed := map[string]bool{}
	for name, object := range shareObjects {
		allowed[name] = (scope.Collectors == nil || object.collector != "") && (scope.Targets == nil || object.target != "")
	}
	return allowed
}

func shareAuthorizer(allowed, names map[string]bool, scoped bool) sqlite.AuthorizerFn {
	return func(action sqlite.AuthorizerActionCode, table, column, database, source string) sqlite.AuthorizerReturnCode {
		table, database, source = strings.ToLower(table), strings.ToLower(database), strings.ToLower(source)
		switch action {
		case sqlite.AuthSelect, sqlite.AuthRecursive:
			return sqlite.AuthorizerOK
		case sqlite.AuthFunction:
			if !slices.Contains([]string{"load_extension", "readfile", "writefile"}, strings.ToLower(column)) {
				return sqlite.AuthorizerOK
			}
		case sqlite.AuthRead:
			if table == "operation_requests" {
				return sqlite.AuthorizerDeny
			}
			if (table == "json_each" || table == "json_tree") && database == "" {
				return sqlite.AuthorizerOK
			}
			if database == "main" {
				if !scoped && names[table] {
					return sqlite.AuthorizerOK
				}
				if allowed[table] && (!scoped || source == table) {
					return sqlite.AuthorizerOK
				}
				if allowed[source] && slices.Contains(shareObjects[source].dependencies, table) {
					return sqlite.AuthorizerOK
				}
			}
			if database == "" && !names[table] && !strings.HasPrefix(table, "pragma_") && !strings.HasPrefix(table, "sqlite_") {
				return sqlite.AuthorizerOK
			}
		}
		return sqlite.AuthorizerDeny
	}
}

func shareWrap(query string, tokens []config.SQLToken, scope ShareScope, names map[string]bool) (string, []any) {
	if scope.Collectors == nil && scope.Targets == nil {
		return query, nil
	}
	if len(tokens) > 0 && tokens[len(tokens)-1].Kind == "punctuation" && tokens[len(tokens)-1].Text == ";" {
		t := tokens[len(tokens)-1]
		query = query[:t.Start] + query[t.End:]
	}
	args := []any{}
	bound := map[string]bool{}
	predicate := func(column, prefix string, values []string) string {
		if values == nil {
			return "1"
		}
		list := []string{}
		for i, id := range values {
			name := fmt.Sprintf("lgshare_%s%d", prefix, i)
			list = append(list, ":"+name)
			if !bound[name] {
				args = append(args, sql.Named(name, id))
				bound[name] = true
			}
		}
		return `"` + column + `" IN (` + strings.Join(list, ",") + `)`
	}
	ctes := []string{}
	allowed := shareAllowed(scope)
	for name, object := range shareObjects {
		if !names[name] || !allowed[name] {
			continue
		}
		filter := predicate(object.collector, "c", scope.Collectors) + " AND " + predicate(object.target, "t", scope.Targets)
		ctes = append(ctes, `"`+name+`" AS NOT MATERIALIZED (SELECT * FROM main."`+name+`" WHERE `+filter+`)`)
	}
	slices.Sort(ctes)
	if len(ctes) == 0 {
		return query, args
	}
	return "WITH " + strings.Join(ctes, ",\n") + "\nSELECT * FROM (\n" + query + "\n)", args
}

func (v *View) QueryShare(parent context.Context, query string, scope ShareScope, params map[string]any) (rules.QueryResult, error) {
	if err := scope.Validate(); err != nil {
		return rules.QueryResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, QueryBudget)
	defer cancel()
	dsn, err := readOnlyDSN(v.path)
	if err != nil {
		return rules.QueryResult{}, err
	}
	names := map[string]bool{}
	for name := range shareObjects {
		names[name] = false
	}
	dedicated := &sqlite.Driver{}
	allowed := shareAllowed(scope)
	dedicated.RegisterConnectionHook(func(connection sqlite.ExecQuerierContext, _ string) error {
		rows, err := connection.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type IN ('table','view')`, nil)
		if err != nil {
			return fmt.Errorf("读取分享授权对象失败：%w", err)
		}
		for {
			values := make([]driver.Value, 1)
			err := rows.Next(values)
			if err == io.EOF {
				break
			}
			if err != nil {
				rows.Close()
				return fmt.Errorf("读取分享对象名称失败：%w", err)
			}
			name, ok := values[0].(string)
			if !ok {
				if data, yes := values[0].([]byte); yes {
					name = string(data)
				}
			}
			names[strings.ToLower(name)] = true
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("关闭分享授权对象游标失败：%w", err)
		}
		registerer, ok := connection.(sqlite.AuthorizerRegisterer)
		if !ok {
			return fmt.Errorf("SQLite 驱动不支持分享授权回调")
		}
		if err := registerer.RegisterAuthorizer(shareAuthorizer(allowed, names, scope.Collectors != nil || scope.Targets != nil)); err != nil {
			return fmt.Errorf("注册分享授权回调失败：%w", err)
		}
		return nil
	})
	db := sql.OpenDB(ruleConnector{driver: dedicated, dsn: dsn})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return rules.QueryResult{}, fmt.Errorf("打开分享只读连接失败：%w", err)
	}
	protected := map[string]bool{}
	for name := range names {
		protected[name] = true
	}
	tokens, err := shareSQLTokens(query, protected)
	if err != nil {
		return rules.QueryResult{}, err
	}
	wrapped, args := shareWrap(query, tokens, scope, names)
	for name, value := range params {
		if !slices.Contains([]string{"from", "to", "collector"}, name) {
			return rules.QueryResult{}, fmt.Errorf("分享不接受此查询参数")
		}
		args = append(args, sql.Named(name, value))
	}
	result, err := queryReadOnly(ctx, db, wrapped, QueryLimit, args...)
	if err != nil {
		if strings.Contains(err.Error(), "authoriz") || strings.Contains(err.Error(), "prohibited") {
			return rules.QueryResult{}, fmt.Errorf("分享数据授权被拒绝：%w", errors.Join(ErrShareDimension, err))
		}
		return rules.QueryResult{}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return rules.QueryResult{}, fmt.Errorf("编码分享结果失败：%w", err)
	}
	if len(data) > ShareResultLimit {
		return rules.QueryResult{}, ErrShareResultSize
	}
	return result, nil
}
