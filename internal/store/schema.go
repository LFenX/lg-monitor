package store

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

type DatabaseSchema struct {
	Tables []TableSchema `json:"tables"`
}
type TableSchema struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Description string         `json:"description"`
	Columns     []ColumnSchema `json:"columns"`
}
type ColumnSchema struct {
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	NotNull            bool    `json:"notNull"`
	Default            *string `json:"default" jsonschema:"nullable"`
	PrimaryKeyPosition int     `json:"primaryKeyPosition"`
	Description        string  `json:"description"`
}

type schemaDocs struct{ tables, columns map[string]string }

var schemaIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func migrationDocs(fsys fs.FS) (schemaDocs, error) {
	result := schemaDocs{tables: map[string]string{}, columns: map[string]string{}}
	files, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return result, fmt.Errorf("读取表结构说明列表失败：%w", err)
	}
	sort.Strings(files)
	for _, name := range files {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return result, fmt.Errorf("读取迁移说明 %s 失败：%w", name, err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		line := 0
		for scanner.Scan() {
			line++
			text := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(text, "--") {
				continue
			}
			fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(text, "--")))
			if len(fields) == 0 || (fields[0] != "@table" && fields[0] != "@column") {
				continue
			}
			if len(fields) < 3 {
				return result, fmt.Errorf("%s 第 %d 行：表结构说明需要名称和描述", name, line)
			}
			for _, target := range strings.Split(fields[1], ",") {
				parts := strings.Split(target, ".")
				valid := len(parts) == 1
				if fields[0] == "@column" {
					valid = len(parts) == 2
				}
				for _, part := range parts {
					valid = valid && schemaIdentifier.MatchString(part)
				}
				if !valid {
					return result, fmt.Errorf("%s 第 %d 行：表或列说明的名称无效", name, line)
				}
				destination := result.tables
				if fields[0] == "@column" {
					destination = result.columns
				}
				destination[target] = strings.Join(fields[2:], " ")
			}
		}
		if err := scanner.Err(); err != nil {
			return result, fmt.Errorf("解析迁移说明 %s 失败：%w", name, err)
		}
	}
	return result, nil
}

func (v *View) Schema(parent context.Context) (DatabaseSchema, error) {
	result := DatabaseSchema{Tables: []TableSchema{}}
	docs, err := migrationDocs(migrations)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(parent, QueryBudget)
	defer cancel()
	tx, err := v.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, fmt.Errorf("打开表结构只读快照失败：%w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT name,type FROM sqlite_schema WHERE type IN ('table','view') ORDER BY name`)
	if err != nil {
		return result, fmt.Errorf("查询数据库表清单失败：%w", err)
	}
	for rows.Next() {
		var item TableSchema
		if err := rows.Scan(&item.Name, &item.Kind); err != nil {
			rows.Close()
			return result, fmt.Errorf("读取数据库表名失败：%w", err)
		}
		item.Description = docs.tables[item.Name]
		result.Tables = append(result.Tables, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, fmt.Errorf("遍历数据库表清单失败：%w", err)
	}
	for i := range result.Tables {
		item := &result.Tables[i]
		item.Columns, err = schemaColumns(ctx, tx, item.Name, docs)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func schemaColumns(ctx context.Context, tx *sql.Tx, table string, docs schemaDocs) ([]ColumnSchema, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name,type,"notnull",dflt_value,pk FROM pragma_table_xinfo(?) ORDER BY cid`, table)
	if err != nil {
		return nil, fmt.Errorf("读取表 %s 的列信息失败：%w", table, err)
	}
	defer rows.Close()
	result := []ColumnSchema{}
	for rows.Next() {
		var item ColumnSchema
		if err := rows.Scan(&item.Name, &item.Type, &item.NotNull, &item.Default, &item.PrimaryKeyPosition); err != nil {
			return nil, fmt.Errorf("读取列字段失败：%w", err)
		}
		item.Description = docs.columns[table+"."+item.Name]
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历表列失败：%w", err)
	}
	return result, nil
}
