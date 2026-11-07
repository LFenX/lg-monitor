package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type queryReader interface {
	Query(context.Context, string) (rules.QueryResult, error)
	QueryParams(context.Context, string, map[string]any) (rules.QueryResult, error)
	Collector(context.Context, string) (store.CollectorRecord, bool, error)
	Schema(context.Context) (store.DatabaseSchema, error)
	Close() error
}
type queryFactory func(context.Context, string) (queryReader, error)

func openQuery(ctx context.Context, home string) (queryReader, error) {
	return store.OpenView(ctx, filepath.Join(home, "data", "lgmon.db"))
}

func newQueryCommand(open queryFactory, now func() time.Time) *cobra.Command {
	var asJSON, schema bool
	var rangeValue, collector string
	var from, to int64
	type output struct {
		rules.QueryResult
		Params config.QueryValues `json:"params"`
	}
	command := &cobra.Command{Use: "query [SQL]", Short: "以只读连接查询监控数据，或列出表结构说明", Args: func(_ *cobra.Command, args []string) error {
		if (schema && len(args) != 0) || (!schema && len(args) != 1) {
			return &usageError{fmt.Errorf("请提供一条 SQL，或单独使用 --schema 查询表结构")}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := cmd.Flags().GetString("home")
		reader, err := open(cmd.Context(), home)
		var result any
		if err == nil {
			if schema {
				result, err = reader.Schema(cmd.Context())
			} else {
				input := config.QueryParams{}
				if cmd.Flags().Changed("range") {
					span := config.Duration(rangeValue)
					input.Range = &span
				}
				if cmd.Flags().Changed("from") {
					input.From = &from
				}
				if cmd.Flags().Changed("to") {
					input.To = &to
				}
				if cmd.Flags().Changed("collector") {
					input.Collector = &collector
				}
				values, resolveErr := config.ResolveQueryParams(input, "1h", now)
				err = resolveErr
				if err == nil && values.Collector != nil {
					_, known, lookupErr := reader.Collector(cmd.Context(), *values.Collector)
					err = lookupErr
					if err == nil && !known {
						err = fmt.Errorf("采集器不存在，请使用已登记的采集器 ID")
					}
				}
				if err == nil {
					_, err = config.DashboardSQLParameters(args[0])
				}
				if err == nil {
					var data rules.QueryResult
					data, err = reader.QueryParams(cmd.Context(), args[0], values.Bindings())
					result = output{QueryResult: data, Params: values}
				}
			}
			err = errors.Join(err, reader.Close())
		}
		if err != nil {
			if asJSON {
				code := "query_failed"
				if errors.Is(err, context.DeadlineExceeded) {
					code = "query_timeout"
				}
				if writeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"error": map[string]string{"code": code, "message": err.Error()}}); writeErr != nil {
					return fmt.Errorf("输出查询错误失败：%w", writeErr)
				}
				return &reportedError{err}
			}
			return fmt.Errorf("只读查询失败：%w", err)
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("输出查询结果失败：%w", err)
			}
			return nil
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if schema {
			for _, table := range result.(store.DatabaseSchema).Tables {
				if _, err := fmt.Fprintf(writer, "%s\t%s\n", table.Name, table.Description); err != nil {
					return fmt.Errorf("输出表说明失败：%w", err)
				}
				for _, column := range table.Columns {
					if _, err := fmt.Fprintf(writer, "  %s\t%s\t%s\n", column.Name, column.Type, column.Description); err != nil {
						return fmt.Errorf("输出列说明失败：%w", err)
					}
				}
			}
		} else {
			rows := result.(output).QueryResult
			header := make([]any, len(rows.Columns))
			for i, value := range rows.Columns {
				header[i] = value
			}
			all := append([][]any{header}, rows.Rows...)
			for _, row := range all {
				for i, value := range row {
					data, err := json.Marshal(value)
					if err != nil {
						return fmt.Errorf("格式化查询单元格失败：%w", err)
					}
					separator := "\t"
					if i == len(row)-1 {
						separator = "\n"
					}
					if _, err := fmt.Fprintf(writer, "%s%s", data, separator); err != nil {
						return fmt.Errorf("输出查询单元格失败：%w", err)
					}
				}
			}
			if rows.Truncated {
				if _, err := fmt.Fprintf(writer, "结果超过 %d 行，已截断；请缩小查询范围\n", store.QueryLimit); err != nil {
					return fmt.Errorf("输出截断提示失败：%w", err)
				}
			}
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("刷新查询输出失败：%w", err)
		}
		return nil
	}}
	command.Flags().BoolVar(&asJSON, "json", false, "输出列、行和截断标记 JSON，或结构化错误")
	command.Flags().BoolVar(&schema, "schema", false, "返回全部监控表和列的结构与说明")
	command.Flags().StringVar(&rangeValue, "range", "", "相对时间范围，如 1h、7d，使用监控器时钟")
	command.Flags().Int64Var(&from, "from", 0, "固定开始时间，UTC Unix 毫秒")
	command.Flags().Int64Var(&to, "to", 0, "固定结束时间，UTC Unix 毫秒")
	command.Flags().StringVar(&collector, "collector", "", "已登记采集器 ID；省略表示全部主机")
	return command
}
