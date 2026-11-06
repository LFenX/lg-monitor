package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"modernc.org/sqlite"
)

type ruleTable struct{ predicate, columns string }

var ruleTables = map[string]ruleTable{
	"sqlite_schema": {}, "sqlite_master": {}, "sqlite_temp_schema": {}, "sqlite_temp_master": {},
	"target_status": {predicate: "ts <= :now"}, "probe_results": {predicate: "ts <= :now"}, "process_samples": {predicate: "ts <= :now"}, "system_metrics": {predicate: "ts <= :now"}, "disk_metrics": {predicate: "ts <= :now"}, "network_metrics": {predicate: "ts <= :now"}, "service_status": {predicate: "ts <= :now"}, "script_metrics": {predicate: "ts <= :now"}, "collector_events": {predicate: "ts <= :now"},
	"collectors":    {predicate: "first_seen <= :now", columns: "id,hostname,first_seen,version"},
	"metric_minute": {predicate: "ts + 60000 <= :now"}, "probe_minute": {predicate: "ts + 60000 <= :now"},
	"metric_hour": {predicate: "ts + 3600000 <= :now"}, "probe_hour": {predicate: "ts + 3600000 <= :now"},
	"rollup_dirty": {}, "rollup_state": {}, "raw_watermarks": {},
	"alerts": {}, "alert_events": {}, "notifications": {}, "collector_offsets": {}, "collector_inbox": {}, "schema_migrations": {}, "silences": {}, "alert_groups": {}, "notification_events": {}, "rule_errors": {}, "sqlite_sequence": {}, "sqlite_stat1": {}, "sqlite_stat4": {},
}

type RuleQueries struct{ db *sql.DB }
type ruleConnector struct {
	driver *sqlite.Driver
	dsn    string
}

func (c ruleConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.driver.Open(c.dsn)
}
func (c ruleConnector) Driver() driver.Driver { return c.driver }

func ruleAuthorizer(names map[string]bool) sqlite.AuthorizerFn {
	return func(action sqlite.AuthorizerActionCode, table, column, database, source string) sqlite.AuthorizerReturnCode {
		table, database, source = strings.ToLower(table), strings.ToLower(database), strings.ToLower(source)
		switch action {
		case sqlite.AuthSelect, sqlite.AuthRecursive:
			return sqlite.AuthorizerOK
		case sqlite.AuthFunction:
			if !strings.EqualFold(column, "current_timestamp") && !strings.EqualFold(column, "current_date") && !strings.EqualFold(column, "current_time") {
				return sqlite.AuthorizerOK
			}
		case sqlite.AuthRead:
			if table == "json_each" || table == "json_tree" {
				return sqlite.AuthorizerOK
			}
			if database == "main" && ruleTables[table].predicate != "" && source == table {
				return sqlite.AuthorizerOK
			}
			if database == "" && !names[table] {
				return sqlite.AuthorizerOK
			}
		}
		return sqlite.AuthorizerDeny
	}
}

func OpenRuleQueries(ctx context.Context, path string) (*RuleQueries, error) {
	dsn, err := readOnlyDSN(path)
	if err != nil {
		return nil, err
	}
	dedicated := &sqlite.Driver{}
	dedicated.RegisterConnectionHook(func(connection sqlite.ExecQuerierContext, _ string) error {
		rows, err := connection.QueryContext(context.Background(), `SELECT name FROM sqlite_schema WHERE type IN ('table','view')`, nil)
		if err != nil {
			return fmt.Errorf("读取规则授权表清单失败：%w", err)
		}
		names := map[string]bool{}
		for name := range ruleTables {
			names[name] = true
		}
		for {
			values := make([]driver.Value, 1)
			err := rows.Next(values)
			if err == io.EOF {
				break
			}
			if err != nil {
				rows.Close()
				return fmt.Errorf("读取规则授权对象失败：%w", err)
			}
			switch name := values[0].(type) {
			case string:
				names[strings.ToLower(name)] = true
			case []byte:
				names[strings.ToLower(string(name))] = true
			}
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("关闭规则对象游标失败：%w", err)
		}
		registerer, ok := connection.(sqlite.AuthorizerRegisterer)
		if !ok {
			return fmt.Errorf("SQLite 驱动不支持规则授权回调")
		}
		if err := registerer.RegisterAuthorizer(ruleAuthorizer(names)); err != nil {
			return fmt.Errorf("注册规则授权回调失败：%w", err)
		}
		return nil
	})
	db := sql.OpenDB(ruleConnector{driver: dedicated, dsn: dsn})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("打开规则只读连接失败：%w", err)
	}
	return &RuleQueries{db: db}, nil
}
func (q *RuleQueries) Close() error { return q.db.Close() }

func ruleSQL(query string) (string, error) {
	if err := config.ValidateRuleSQL(query); err != nil {
		return "", err
	}
	tokens, _ := config.SQLTokens(query)
	if len(tokens) > 0 {
		last := tokens[len(tokens)-1]
		if last.Kind == "punctuation" && last.Text == ";" {
			query = query[:last.Start] + query[last.End:]
		}
	}
	var ctes []string
	for name, table := range ruleTables {
		if table.predicate == "" {
			continue
		}
		columns := table.columns
		if columns == "" {
			columns = "*"
		}
		ctes = append(ctes, `"`+name+`" AS NOT MATERIALIZED (SELECT `+columns+` FROM main."`+name+`" WHERE `+table.predicate+`)`)
	}
	slices.Sort(ctes)
	return "WITH " + strings.Join(ctes, ",\n") + "\nSELECT * FROM (\n" + query + "\n)", nil
}

func (q *RuleQueries) QueryRule(ctx context.Context, query string, at int64) (rules.QueryResult, error) {
	wrapped, err := ruleSQL(query)
	if err != nil {
		return rules.QueryResult{}, err
	}
	result, err := queryReadOnly(ctx, q.db, wrapped, 100, sql.Named("now", at))
	if err != nil {
		if strings.Contains(err.Error(), "last_seen") {
			return result, fmt.Errorf("collectors.last_seen 只保存最新值，判断失联请使用内置 collector_offline 规则：%w", err)
		}
		if strings.Contains(err.Error(), "authoriz") || strings.Contains(err.Error(), "prohibited") {
			return result, fmt.Errorf("SQL 规则读取被拒绝：请移除 alerts 等内部表、sqlite_schema、pragma_* 或视图，改查 target_status、probe_results 等原始采样表：%w", err)
		}
		return result, fmt.Errorf("SQL 规则查询失败，请检查语法、可读表和权限限制：%w", err)
	}
	if result.Truncated {
		return result, fmt.Errorf("SQL 规则结果超过 100 行，请合并或缩小查询")
	}
	for _, row := range result.Rows {
		for i, value := range row {
			if data, ok := value.([]byte); ok {
				row[i] = string(data)
			}
		}
	}
	return result, nil
}
