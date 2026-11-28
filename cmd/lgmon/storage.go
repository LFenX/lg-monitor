// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/update"
	"github.com/spf13/cobra"
)

func resolveStorageHome(cmd *cobra.Command) string {
	home, _ := cmd.Flags().GetString("home")
	if resolved, ok := config.ResolveHome(home); ok {
		return resolved
	}
	return filepath.Clean(home)
}

func parseStorageTimeArg(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	if ms, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		if ms < 0 {
			return 0, fmt.Errorf("时间不能为负数：%s", raw)
		}
		return ms, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t.UTC().UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("无效时间 %q：请使用 UTC Unix 毫秒或 RFC3339（如 2026-11-28T00:00:00Z）", raw)
}

type cliHomeUsage struct {
	Total   int64 `json:"dataBytes"`
	DB      int64 `json:"dbBytes"`
	WAL     int64 `json:"walBytes"`
	Archive int64 `json:"archiveBytes"`
	Config  int64 `json:"configBytes"`
	Logs    int64 `json:"logsBytes"`
	Other   int64 `json:"otherBytes"`
	Files   int   `json:"fileCount"`
	Top     []struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Category string `json:"category"`
	} `json:"topFiles,omitempty"`
}

func measureCLIHomeUsage(home string) cliHomeUsage {
	var out cliHomeUsage
	type fileEntry struct {
		path, cat string
		size      int64
	}
	var all []fileEntry
	_ = filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		size := info.Size()
		out.Total += size
		out.Files++
		rel, relErr := filepath.Rel(home, path)
		if relErr != nil {
			out.Other += size
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		lower := strings.ToLower(slashRel)
		cat := "other"
		switch {
		case strings.HasPrefix(lower, "data/archives/"):
			out.Archive += size
			cat = "archive"
		case strings.HasPrefix(lower, "data/") && (strings.HasSuffix(lower, ".db-wal") || strings.HasSuffix(lower, ".db-shm")):
			out.DB += size
			out.WAL += size
			cat = "wal"
		case strings.HasPrefix(lower, "data/") && strings.HasSuffix(lower, ".db"):
			out.DB += size
			cat = "db"
		case strings.HasPrefix(lower, "config/"):
			out.Config += size
			cat = "config"
		case strings.HasPrefix(lower, "logs/"):
			out.Logs += size
			cat = "logs"
		default:
			out.Other += size
		}
		if size > 0 {
			all = append(all, fileEntry{slashRel, cat, size})
		}
		return nil
	})
	sort.Slice(all, func(i, j int) bool {
		if all[i].size != all[j].size {
			return all[i].size > all[j].size
		}
		return all[i].path < all[j].path
	})
	if len(all) > 8 {
		all = all[:8]
	}
	for _, item := range all {
		out.Top = append(out.Top, struct {
			Path     string `json:"path"`
			Size     int64  `json:"size"`
			Category string `json:"category"`
		}{item.path, item.size, item.cat})
	}
	return out
}

func newStorageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "查看磁盘占用、清理压缩数据库并按需提取历史归档",
	}

	var statusJSON bool
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "统计数据目录文件与数据库表占用明细",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := resolveStorageHome(cmd)
			usage := measureCLIHomeUsage(home)
			var dbStats *store.DBStorageStats
			var archives *store.ArchiveSummary
			dbPath := filepath.Join(home, "data", "lgmon.db")
			if view, err := store.OpenView(cmd.Context(), dbPath); err == nil {
				if st, sErr := view.DBStorageStats(cmd.Context()); sErr == nil {
					dbStats = &st
				}
				if sum, _, aErr := view.ListArchives(cmd.Context(), 0, 0, ""); aErr == nil {
					archives = &sum
				}
				_ = view.Close()
			} else if !errors.Is(err, store.ErrNoData) {
				return err
			}
			if statusJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"home":     home,
					"usage":    usage,
					"db":       dbStats,
					"archives": archives,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "数据目录：%s\n总占用：%d 字节（数据库 %d，WAL %d，归档 %d，日志 %d，配置 %d，其他 %d，共 %d 个文件）\n",
				home, usage.Total, usage.DB, usage.WAL, usage.Archive, usage.Logs, usage.Config, usage.Other, usage.Files)
			if dbStats != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "SQLite 空闲页：%d 字节（%d / %d 页）\n", dbStats.FreelistBytes, dbStats.FreelistCount, dbStats.PageCount)
				for _, t := range dbStats.Tables {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  表 %-22s 行数=%d  估算载荷=%d 字节\n", t.Name, t.Rows, t.PayloadBytes)
				}
			}
			if archives != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "历史归档：%d 块，%d 条记录，压缩后 %d 字节（原 %d 字节）\n",
					archives.ChunkCount, archives.TotalRecords, archives.CompressedBytes, archives.UncompressedBytes)
			}
			return nil
		},
	}
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "以 JSON 输出存储占用统计")

	var archFrom, archTo, archTable string
	var archJSON bool
	archivesCmd := &cobra.Command{
		Use:   "archives",
		Short: "列出已压缩验证的历史归档块",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := resolveStorageHome(cmd)
			from, err := parseStorageTimeArg(archFrom)
			if err != nil {
				return &usageError{err}
			}
			to, err := parseStorageTimeArg(archTo)
			if err != nil {
				return &usageError{err}
			}
			view, err := store.OpenView(cmd.Context(), filepath.Join(home, "data", "lgmon.db"))
			if err != nil {
				return err
			}
			defer view.Close()
			summary, chunks, err := view.ListArchives(cmd.Context(), from, to, strings.TrimSpace(archTable))
			if err != nil {
				return err
			}
			if archJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"summary": summary, "chunks": chunks})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "归档目录：%s\n块数：%d，总记录：%d，压缩体积：%d 字节（原 %d 字节）\n",
				summary.ArchiveDir, summary.ChunkCount, summary.TotalRecords, summary.CompressedBytes, summary.UncompressedBytes)
			for _, c := range chunks {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s  [%s ~ %s]  记录=%d  压缩=%dB\n",
					c.ID, time.UnixMilli(c.MinTS).UTC().Format(time.RFC3339), time.UnixMilli(c.MaxTS).UTC().Format(time.RFC3339), c.RecordCount, c.CompressedBytes)
			}
			return nil
		},
	}
	archivesCmd.Flags().StringVar(&archFrom, "from", "", "开始时间（UTC 毫秒或 RFC3339）")
	archivesCmd.Flags().StringVar(&archTo, "to", "", "结束时间（UTC 毫秒或 RFC3339）")
	archivesCmd.Flags().StringVar(&archTable, "table", "", "按表名筛选")
	archivesCmd.Flags().BoolVar(&archJSON, "json", false, "以 JSON 输出归档清单")

	var exFrom, exTo, exTables, exCollector, exTarget string
	var exLimit, exOffset int
	var exJSON bool
	extractCmd := &cobra.Command{
		Use:   "extract",
		Short: "按时间段和条件从历史归档中提取原始记录",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := resolveStorageHome(cmd)
			from, err := parseStorageTimeArg(exFrom)
			if err != nil {
				return &usageError{err}
			}
			to, err := parseStorageTimeArg(exTo)
			if err != nil {
				return &usageError{err}
			}
			var tables []string
			for _, part := range strings.Split(exTables, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					tables = append(tables, trimmed)
				}
			}
			view, err := store.OpenView(cmd.Context(), filepath.Join(home, "data", "lgmon.db"))
			if err != nil {
				return err
			}
			defer view.Close()
			res, err := view.ExtractArchive(cmd.Context(), store.ArchiveExtractQuery{
				From:        from,
				To:          to,
				Tables:      tables,
				CollectorID: strings.TrimSpace(exCollector),
				TargetID:    strings.TrimSpace(exTarget),
				Limit:       exLimit,
				Offset:      exOffset,
			})
			if err != nil {
				return err
			}
			if exJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "扫描归档块：%d，匹配总数：%d，本次返回：%d\n", res.ChunksScanned, res.Total, res.Returned)
			for _, rec := range res.Records {
				line, _ := json.Marshal(rec)
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(line))
			}
			return nil
		},
	}
	extractCmd.Flags().StringVar(&exFrom, "from", "", "开始时间（UTC 毫秒或 RFC3339）")
	extractCmd.Flags().StringVar(&exTo, "to", "", "结束时间（UTC 毫秒或 RFC3339）")
	extractCmd.Flags().StringVar(&exTables, "table", "", "表名（多个用英文逗号分隔）")
	extractCmd.Flags().StringVar(&exCollector, "collector", "", "采集器 ID")
	extractCmd.Flags().StringVar(&exTarget, "target", "", "目标或服务 ID")
	extractCmd.Flags().IntVar(&exLimit, "limit", 500, "最多返回条数（1～5000）")
	extractCmd.Flags().IntVar(&exOffset, "offset", 0, "偏移量")
	extractCmd.Flags().BoolVar(&exJSON, "json", false, "以 JSON 输出提取结果")

	var compactJSON bool
	compactCmd := &cobra.Command{
		Use:   "compact",
		Short: "对指定数据目录执行归档清理、数据库 VACUUM、WAL 截断与日志裁剪",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := resolveStorageHome(cmd)
			var retention config.Retention
			configDir := filepath.Join(home, "config")
			if info, err := os.Stat(configDir); err == nil && info.IsDir() {
				if cfg, loadErr := config.Load(os.DirFS(configDir)); loadErr == nil {
					retention = cfg.Settings.Retention
				}
			}
			before := measureCLIHomeUsage(home)

			// Trim logs (> 2 MiB), clean stale .tmp/.lgmon-write-probe, and prune old installers inside <home>/updates.
			if entries, err := os.ReadDir(filepath.Join(home, "logs")); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
						trimCLILogInPlace(filepath.Join(home, "logs", entry.Name()), 2*1024*1024)
					}
				}
			}
			_ = filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry == nil || entry.IsDir() {
					return nil
				}
				name := strings.ToLower(entry.Name())
				if strings.HasSuffix(name, ".tmp") || name == ".lgmon-write-probe" {
					_ = os.Remove(path)
				}
				return nil
			})
			_, _ = update.PruneInstallers(filepath.Join(home, "updates"), "", version)

			var archivedRecords int
			dbPath := filepath.Join(home, "data", "lgmon.db")
			if _, statErr := os.Stat(dbPath); statErr == nil {
				s, err := store.Open(cmd.Context(), store.Options{
					Path:               dbPath,
					Retention:          retention,
					DisableMaintenance: true,
				})
				if err != nil {
					return err
				}
				compactErr := s.Compact(cmd.Context())
				archivedRecords = s.MaintenanceStats().LastArchivedRecords
				closeErr := s.Close()
				if compactErr != nil {
					return errors.Join(compactErr, closeErr)
				}
				if closeErr != nil {
					return closeErr
				}
			}
			after := measureCLIHomeUsage(home)
			freed := max(int64(0), before.Total-after.Total)
			if compactJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"home":            home,
					"beforeBytes":     before.Total,
					"afterBytes":      after.Total,
					"freedBytes":      freed,
					"archivedRecords": archivedRecords,
					"archiveBytes":    after.Archive,
					"before":          before,
					"after":           after,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "存储瘦身完成：%s\n压缩前：%d 字节，压缩后：%d 字节（含归档 %d 字节），净释放：%d 字节，本次归档记录：%d 条\n",
				home, before.Total, after.Total, after.Archive, freed, archivedRecords)
			return nil
		},
	}
	compactCmd.Flags().BoolVar(&compactJSON, "json", false, "以 JSON 输出瘦身与归档结果")

	cmd.AddCommand(statusCmd, archivesCmd, extractCmd, compactCmd)
	return cmd
}

func trimCLILogInPlace(logPath string, keepBytes int64) {
	info, err := os.Stat(logPath)
	if err != nil || info.Size() <= keepBytes {
		return
	}
	file, err := os.OpenFile(logPath, os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	offset := info.Size() - keepBytes
	tail := make([]byte, info.Size()-offset)
	n, readErr := file.ReadAt(tail, offset)
	if readErr != nil && n == 0 {
		return
	}
	tail = tail[:n]
	for i, b := range tail {
		if b == '\n' && i+1 < len(tail) {
			tail = tail[i+1:]
			break
		}
	}
	if _, err := file.WriteAt(tail, 0); err == nil {
		_ = file.Truncate(int64(len(tail)))
	}
}
