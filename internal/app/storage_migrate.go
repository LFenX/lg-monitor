// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
	_ "modernc.org/sqlite"
)

const (
	migrationCopyChunk   = 128 * 1024
	migrationMinHeadroom = 50 * 1024 * 1024
	logCompactKeepBytes  = 2 * 1024 * 1024
	migrationMarkerFile  = ".lgmon-migration"
)

var reusableTopEntries = map[string]bool{
	"config":                true,
	"data":                  true,
	"logs":                  true,
	"certs":                 true,
	"docs":                  true,
	"collector-id":          true,
	"storage-redirect.json": true,
	".lgmon-migration":      true,
	".lgmon-write-probe":    true,
}

type storageState struct {
	mu      sync.Mutex
	current *api.StorageMigrationState
	running bool
}

type migrationFile struct {
	rel     string
	src     string
	dst     string
	size    int64
	written int64
	isSQLDB bool
	isLog   bool
}

func (b *APIBackend) storageOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	if p, ok := api.CurrentPrincipal(ctx); ok && p.Guest {
		return nil, &api.Fault{Status: 403, Code: "guest_forbidden", Message: "内网访客模式不能查看或修改主机存储目录"}
	}
	switch op {
	case api.GetStorage:
		return b.storageStatus(), nil
	case api.CompactStorage:
		return b.compactStorage(ctx)
	default:
		var input api.StorageMigrateInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		return b.startStorageMigration(ctx, input.Target)
	}
}

func (b *APIBackend) storageStatus() api.StorageStatus {
	home := filepath.Clean(strings.TrimSpace(b.options.Home))
	defaultHome := config.DefaultBaseHome()
	redirected := false
	if resolved, ok := config.ResolveHome(defaultHome); ok && config.SamePath(resolved, home) {
		redirected = true
	} else if home != "" && !config.SamePath(home, defaultHome) {
		if _, ok := config.ReadHomeRedirect(defaultHome); ok {
			redirected = true
		}
	}
	usage := measureHomeUsage(home)
	status := api.StorageStatus{
		Home:        home,
		DefaultHome: defaultHome,
		Redirected:  redirected,
		DataBytes:   usage.total,
		DBBytes:     usage.db,
		ConfigBytes: usage.config,
		LogsBytes:   usage.logs,
		OtherBytes:  usage.other,
		FileCount:   usage.files,
		Drives:      listLocalDrives(home),
	}
	if home != "" {
		if drive, err := diskUsageForPath(home); err == nil && drive.TotalBytes > 0 {
			drive.Current = true
			status.CurrentDrive = &drive
		}
	}
	b.storage.mu.Lock()
	if b.storage.current != nil {
		copyState := *b.storage.current
		status.Migration = &copyState
	}
	b.storage.mu.Unlock()
	return status
}

type homeUsageSummary struct {
	total, db, config, logs, other int64
	files                          int
}

func measureHomeUsage(home string) homeUsageSummary {
	var out homeUsageSummary
	if home == "" {
		return out
	}
	_ = filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		size := info.Size()
		out.total += size
		out.files++
		rel, relErr := filepath.Rel(home, path)
		if relErr != nil {
			out.other += size
			return nil
		}
		slash := strings.ToLower(filepath.ToSlash(rel))
		switch {
		case strings.HasPrefix(slash, "data/") && (strings.HasSuffix(slash, ".db") || strings.HasSuffix(slash, ".db-wal") || strings.HasSuffix(slash, ".db-shm")):
			out.db += size
		case strings.HasPrefix(slash, "config/"):
			out.config += size
		case strings.HasPrefix(slash, "logs/"):
			out.logs += size
		default:
			out.other += size
		}
		return nil
	})
	return out
}

func isSubOrSamePath(parent, child string) bool {
	p := filepath.Clean(parent)
	c := filepath.Clean(child)
	if !strings.EqualFold(filepath.VolumeName(p), filepath.VolumeName(c)) {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(p), strings.ToLower(c))
	if err != nil {
		return true
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// isReusableMigrationDir returns true when a non-empty target directory only contains lgmon's own
// top-level directories/files from an earlier migration attempt, so clicking "Retry Migration" on
// D:\lgmon-data works without forcing the user to manually delete the partial folder.
func isReusableMigrationDir(entries []os.DirEntry) bool {
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if !reusableTopEntries[name] {
			return false
		}
	}
	return true
}

func validateMigrationTarget(source, rawTarget, executable string) (string, error) {
	trimmed := strings.TrimSpace(rawTarget)
	if trimmed == "" {
		return "", invalidAPI("请填写目标数据目录路径，例如 D:\\lgmon-data")
	}
	if strings.ContainsFunc(trimmed, unicode.IsControl) {
		return "", invalidAPI("目标路径包含无效控制字符")
	}
	target := filepath.Clean(trimmed)
	volume := filepath.VolumeName(target)
	if !filepath.IsAbs(target) || strings.HasPrefix(target, `\\`) || target == volume+string(filepath.Separator) || target == string(filepath.Separator) {
		return "", invalidAPI("目标目录必须是本机绝对路径，且不能为磁盘根目录或网络共享（例如 D:\\lgmon-data）")
	}
	if source == "" {
		return "", &api.Fault{Status: 500, Code: "storage_unavailable", Message: "当前运行实例未配置数据目录"}
	}
	if config.SamePath(source, target) {
		return "", invalidAPI("目标目录与当前数据目录相同，无需迁移")
	}
	if isSubOrSamePath(source, target) || isSubOrSamePath(target, source) {
		return "", invalidAPI("目标目录不能在当前数据目录内部，也不能包含当前数据目录")
	}
	if executable != "" {
		programDir := filepath.Dir(filepath.Clean(executable))
		if isSubOrSamePath(programDir, target) || isSubOrSamePath(target, programDir) {
			return "", invalidAPI("目标目录不能与 lgmon 程序安装目录相同或互相包含")
		}
	}
	if info, err := os.Stat(target); err == nil {
		if !info.IsDir() {
			return "", invalidAPI("目标路径已存在且不是文件夹")
		}
		entries, readErr := os.ReadDir(target)
		if readErr != nil {
			return "", fmt.Errorf("无法读取目标目录：%w", readErr)
		}
		if len(entries) > 0 && !isReusableMigrationDir(entries) {
			return "", invalidAPI("目标目录非空，请选择一个空文件夹或新的目录路径（例如 D:\\lgmon-data）")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("检查目标目录失败：%w", err)
	}
	return target, nil
}

func probeDirectoryWritable(target string) (created bool, err error) {
	_, statErr := os.Stat(target)
	existed := statErr == nil
	if !existed {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return false, fmt.Errorf("无法创建目标目录，请检查磁盘是否存在以及写入权限：%w", err)
		}
		created = true
	}
	probe := filepath.Join(target, ".lgmon-write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		if created {
			_ = os.Remove(target)
		}
		return false, fmt.Errorf("目标目录不可写，请检查磁盘权限：%w", err)
	}
	_ = os.Remove(probe)
	return created, nil
}

// compactStorage reclaims disk space in the active home directory by pruning expired rows,
// vacuuming SQLite databases, truncating WAL files, and trimming oversized log files.
func (b *APIBackend) compactStorage(ctx context.Context) (api.StorageCompactResult, error) {
	b.storage.mu.Lock()
	if b.storage.running {
		b.storage.mu.Unlock()
		return api.StorageCompactResult{}, &api.Fault{Status: 409, Code: "migration_in_progress", Message: "正在执行数据迁移，请等待迁移完成后再执行瘦身"}
	}
	b.storage.mu.Unlock()

	home := filepath.Clean(strings.TrimSpace(b.options.Home))
	if home == "" {
		return api.StorageCompactResult{}, &api.Fault{Status: 500, Code: "storage_unavailable", Message: "当前运行实例未配置数据目录"}
	}
	before := measureHomeUsage(home).total
	b.compactHomeInPlace(ctx, home)
	after := measureHomeUsage(home).total
	freed := before - after
	if freed < 0 {
		freed = 0
	}
	return api.StorageCompactResult{
		BeforeBytes: before,
		AfterBytes:  after,
		FreedBytes:  freed,
		Storage:     b.storageStatus(),
	}, nil
}

func (b *APIBackend) compactHomeInPlace(ctx context.Context, home string) {
	// 1. Trim oversized log files in-place to the latest 2 MiB of log lines.
	trimDirectoryLogs(filepath.Join(home, "logs"), logCompactKeepBytes)

	// 2. Remove stale temporary files.
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

	// 3. Compact live monitor store (prunes expired rows, trims old inventory snapshots, VACUUMs lgmon.db, truncates WAL).
	if compactor, ok := b.options.Write.(interface{ Compact(context.Context) error }); ok {
		if err := compactor.Compact(ctx); err != nil {
			b.options.Logger.Warn("Live store compaction returned warning", "error", err)
		}
	} else if flusher, ok := b.options.Write.(interface{ Flush(context.Context) error }); ok {
		_ = flusher.Flush(ctx)
	}

	// 4. Compact auxiliary SQLite databases (auth.db, pairing.db, outbox.db, operations.db).
	dataDir := filepath.Join(home, "data")
	_ = filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		base := strings.ToLower(entry.Name())
		if !strings.HasSuffix(base, ".db") || base == "lgmon.db" {
			return nil
		}
		compactStandaloneSQLite(ctx, path, base == "outbox.db")
		return nil
	})
}

func compactStandaloneSQLite(ctx context.Context, dbPath string, isOutbox bool) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, _ = db.ExecContext(ctx, "PRAGMA busy_timeout = 3000")
	if isOutbox {
		// Remove already-acknowledged records before vacuuming outbox.db.
		_, _ = db.ExecContext(ctx, `DELETE FROM outbox_records WHERE seq <= COALESCE((SELECT acked FROM outbox_meta WHERE singleton=1), 0)`)
	}
	_, _ = db.ExecContext(ctx, "VACUUM")
	var busy, logPages, copied int
	_ = db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &copied)
}

func trimDirectoryLogs(logsDir string, keepBytes int64) {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
			continue
		}
		trimSingleLogInPlace(filepath.Join(logsDir, entry.Name()), keepBytes)
	}
}

func trimSingleLogInPlace(logPath string, keepBytes int64) {
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

func (b *APIBackend) startStorageMigration(ctx context.Context, rawTarget string) (api.StorageStatus, error) {
	source := filepath.Clean(strings.TrimSpace(b.options.Home))
	target, err := validateMigrationTarget(source, rawTarget, b.options.Executable)
	if err != nil {
		return api.StorageStatus{}, err
	}
	usage := measureHomeUsage(source)
	if drive, err := diskUsageForPath(target); err == nil && drive.TotalBytes > 0 {
		required := uint64(usage.total) + migrationMinHeadroom
		if drive.FreeBytes < required {
			return api.StorageStatus{}, invalidAPI(fmt.Sprintf("目标磁盘可用空间不足（需要至少约 %d MB，当前仅剩 %d MB）", required/(1024*1024)+1, drive.FreeBytes/(1024*1024)))
		}
	}
	if _, err := probeDirectoryWritable(target); err != nil {
		return api.StorageStatus{}, &api.Fault{Status: 400, Code: "target_unwritable", Message: err.Error(), Cause: err}
	}

	nowMS := b.options.Now().UnixMilli()
	b.storage.mu.Lock()
	if b.storage.running {
		b.storage.mu.Unlock()
		return api.StorageStatus{}, &api.Fault{Status: 409, Code: "migration_in_progress", Message: "正在执行数据迁移，请等待当前迁移完成"}
	}
	b.storage.running = true
	b.storage.current = &api.StorageMigrationState{
		State:      "preparing",
		Source:     source,
		Target:     target,
		Percent:    2,
		TotalBytes: usage.total,
		TotalFiles: usage.files,
		StartedAt:  nowMS,
		UpdatedAt:  nowMS,
	}
	b.storage.mu.Unlock()

	b.background.Add(1)
	go func() {
		defer b.background.Done()
		runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		b.runStorageMigration(runCtx, source, target)
	}()

	return b.storageStatus(), nil
}

func (b *APIBackend) updateMigration(fn func(*api.StorageMigrationState)) {
	nowMS := b.options.Now().UnixMilli()
	b.storage.mu.Lock()
	if b.storage.current != nil {
		fn(b.storage.current)
		b.storage.current.UpdatedAt = nowMS
	}
	b.storage.mu.Unlock()
}

func (b *APIBackend) finishMigrationError(err error) {
	nowMS := b.options.Now().UnixMilli()
	b.storage.mu.Lock()
	if b.storage.current != nil {
		b.storage.current.State = "error"
		b.storage.current.Error = err.Error()
		b.storage.current.UpdatedAt = nowMS
		b.storage.current.FinishedAt = nowMS
	}
	b.storage.running = false
	b.storage.mu.Unlock()
	b.options.Logger.Error("Storage migration failed", "error", err)
}

func cleanPreviousPartialTarget(target string) {
	entries, err := os.ReadDir(target)
	if err != nil || !isReusableMigrationDir(entries) {
		return
	}
	for _, entry := range entries {
		_ = os.RemoveAll(filepath.Join(target, entry.Name()))
	}
}

func (b *APIBackend) runStorageMigration(ctx context.Context, source, target string) {
	// Step 1: Prepare target, flush in-memory SQLite buffers, and trim oversized log files.
	cleanPreviousPartialTarget(target)
	_ = os.WriteFile(filepath.Join(target, migrationMarkerFile), []byte("migrating\n"), 0o600)
	if flusher, ok := b.options.Write.(interface{ Flush(context.Context) error }); ok {
		_ = flusher.Flush(ctx)
	}
	trimDirectoryLogs(filepath.Join(source, "logs"), logCompactKeepBytes)

	files, totalBytes, hasMonitorDB, err := collectMigrationFiles(source, target)
	if err != nil {
		b.finishMigrationError(fmt.Errorf("扫描源数据目录失败：%w", err))
		return
	}
	b.updateMigration(func(s *api.StorageMigrationState) {
		s.State = "copying"
		s.Percent = 8
		s.TotalFiles = len(files)
		s.TotalBytes = totalBytes
		s.CopiedFiles = 0
		s.CopiedBytes = 0
	})

	// Step 2: Copy files (VACUUM INTO for SQLite databases, tail copy for logs, chunked copy for everything else).
	var copiedBytes int64
	for idx, item := range files {
		if ctx.Err() != nil {
			b.finishMigrationError(fmt.Errorf("数据迁移已取消"))
			return
		}
		b.updateMigration(func(s *api.StorageMigrationState) {
			s.CurrentFile = item.rel
		})
		if err := os.MkdirAll(filepath.Dir(item.dst), 0o700); err != nil {
			b.finishMigrationError(fmt.Errorf("创建目标子目录失败（%s）：%w", item.rel, err))
			return
		}
		if item.isSQLDB {
			if err := copySQLiteSafely(ctx, item.src, item.dst); err != nil {
				b.finishMigrationError(fmt.Errorf("复制数据库 %s 失败：%w", item.rel, err))
				return
			}
			copiedBytes += item.size
			b.updateMigration(func(s *api.StorageMigrationState) {
				s.CopiedFiles = idx + 1
				s.CopiedBytes = copiedBytes
				s.Percent = computeCopyPercent(copiedBytes, totalBytes, idx+1, len(files))
			})
			continue
		}
		if item.isLog {
			written, logErr := copyLogFileTail(item.src, item.dst, logCompactKeepBytes)
			if logErr != nil {
				// A transient lock on a log file must never abort migrating user configuration and databases.
				_ = os.WriteFile(item.dst, nil, 0o600)
				written = 0
			}
			files[idx].written = written
			copiedBytes += item.size
			b.updateMigration(func(s *api.StorageMigrationState) {
				s.CopiedFiles = idx + 1
				s.CopiedBytes = copiedBytes
				s.Percent = computeCopyPercent(copiedBytes, totalBytes, idx+1, len(files))
			})
			continue
		}
		written, err := copyFileChunked(ctx, item.src, item.dst, func(delta int64) {
			copiedBytes += delta
			b.updateMigration(func(s *api.StorageMigrationState) {
				s.CopiedBytes = copiedBytes
				s.Percent = computeCopyPercent(copiedBytes, totalBytes, idx, len(files))
			})
		})
		if err != nil {
			b.finishMigrationError(fmt.Errorf("复制文件 %s 失败：%w", item.rel, err))
			return
		}
		files[idx].written = written
		b.updateMigration(func(s *api.StorageMigrationState) {
			s.CopiedFiles = idx + 1
			s.Percent = computeCopyPercent(copiedBytes, totalBytes, idx+1, len(files))
		})
	}

	// Step 2b: When migrating a monitor node, synthesize a compact, sequence-aligned outbox.db
	// matching target/data/lgmon.db's applied_seq so we neither copy 500 MiB of outbox buffer nor risk ErrCacheDiverged.
	if hasMonitorDB {
		if err := synthesizeAlignedOutbox(ctx, source, target); err != nil {
			b.options.Logger.Warn("Synthesizing aligned outbox returned warning", "error", err)
		}
	}

	// Step 3: Verify copied files and SQLite integrity.
	b.updateMigration(func(s *api.StorageMigrationState) {
		s.State = "verifying"
		s.Percent = 88
		s.CurrentFile = ""
	})
	if err := verifyMigratedFiles(ctx, files); err != nil {
		b.finishMigrationError(fmt.Errorf("校验迁移结果失败：%w", err))
		return
	}

	// Step 4: Protect target tree ACLs and write storage-redirect.json pointer so the next restart seamlessly boots into target.
	b.updateMigration(func(s *api.StorageMigrationState) {
		s.State = "switching"
		s.Percent = 95
		s.CurrentFile = config.HomeRedirectFile
	})
	_ = os.Remove(filepath.Join(target, migrationMarkerFile))
	_ = secrets.NativePathPolicy().ProtectTree(target, false)
	for _, anchor := range b.redirectAnchorHomes(source) {
		if err := config.WriteHomeRedirect(anchor, target, source, b.options.Now()); err != nil {
			b.finishMigrationError(fmt.Errorf("写入新存储路径配置失败：%w", err))
			return
		}
	}

	// Step 5: Complete.
	nowMS := b.options.Now().UnixMilli()
	b.storage.mu.Lock()
	if b.storage.current != nil {
		b.storage.current.State = "done"
		b.storage.current.Percent = 100
		b.storage.current.CurrentFile = ""
		b.storage.current.NeedsRestart = true
		b.storage.current.UpdatedAt = nowMS
		b.storage.current.FinishedAt = nowMS
	}
	b.storage.running = false
	b.storage.mu.Unlock()
	b.options.Logger.Info("Storage migration completed", "source", source, "target", target, "files", len(files), "bytes", totalBytes)
}

func (b *APIBackend) redirectAnchorHomes(source string) []string {
	defaultHome := config.DefaultBaseHome()
	if config.SamePath(source, defaultHome) {
		return []string{defaultHome}
	}
	if resolved, ok := config.ResolveHome(defaultHome); ok && config.SamePath(resolved, source) {
		return []string{defaultHome}
	}
	return []string{source}
}

func computeCopyPercent(copiedBytes, totalBytes int64, copiedFiles, totalFiles int) int {
	if totalBytes > 0 {
		p := 8 + int((copiedBytes*76)/totalBytes)
		if p > 84 {
			return 84
		}
		if p < 8 {
			return 8
		}
		return p
	}
	if totalFiles > 0 {
		return 8 + (copiedFiles*76)/totalFiles
	}
	return 84
}

func isSkippedMigrationFile(slashRel, lowerBase string, hasMonitorDB bool) bool {
	if lowerBase == strings.ToLower(config.HomeRedirectFile) ||
		strings.HasSuffix(lowerBase, ".lock") ||
		strings.HasSuffix(lowerBase, ".db-wal") ||
		strings.HasSuffix(lowerBase, ".db-shm") ||
		strings.HasSuffix(lowerBase, ".db-journal") ||
		strings.HasSuffix(lowerBase, ".tmp") ||
		strings.HasPrefix(lowerBase, ".lgmon-") {
		return true
	}
	// On a monitor node, data/outbox.db is synthesized cleanly from lgmon.db's collector_offsets
	// to avoid copying up to 500 MiB of transient buffer or triggering sequence divergence.
	if hasMonitorDB && strings.ToLower(slashRel) == "data/outbox.db" {
		return true
	}
	return false
}

func collectMigrationFiles(source, target string) ([]migrationFile, int64, bool, error) {
	_, statErr := os.Stat(filepath.Join(source, "data", "lgmon.db"))
	hasMonitorDB := statErr == nil

	var files []migrationFile
	var total int64
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		base := strings.ToLower(entry.Name())
		if isSkippedMigrationFile(slash, base, hasMonitorDB) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		lowerSlash := strings.ToLower(slash)
		isSQLDB := strings.HasPrefix(lowerSlash, "data/") && strings.HasSuffix(base, ".db") && info.Size() > 0
		isLog := strings.HasPrefix(lowerSlash, "logs/") && strings.HasSuffix(base, ".log")
		effectiveSize := info.Size()
		if isLog && effectiveSize > logCompactKeepBytes {
			effectiveSize = logCompactKeepBytes
		}
		files = append(files, migrationFile{
			rel:     slash,
			src:     path,
			dst:     filepath.Join(target, rel),
			size:    effectiveSize,
			isSQLDB: isSQLDB,
			isLog:   isLog,
		})
		total += effectiveSize
		return nil
	})
	return files, total, hasMonitorDB, err
}

// synthesizeAlignedOutbox creates a clean 16 KiB target/data/outbox.db whose outbox_meta
// watermarks (allocated, acked) exactly match target/data/lgmon.db's collector_offsets.applied_seq.
func synthesizeAlignedOutbox(ctx context.Context, source, target string) error {
	if _, err := os.Stat(filepath.Join(source, "data", "outbox.db")); err != nil {
		return nil
	}
	rawID, err := os.ReadFile(filepath.Join(source, "collector-id"))
	if err != nil {
		return nil
	}
	collectorID := strings.TrimSpace(string(rawID))
	if collectorID == "" {
		return nil
	}
	var appliedSeq uint64
	lgmonDB, err := sql.Open("sqlite", filepath.Join(target, "data", "lgmon.db"))
	if err == nil {
		_ = lgmonDB.QueryRowContext(ctx, `SELECT COALESCE(applied_seq, 0) FROM collector_offsets WHERE collector_id=?`, collectorID).Scan(&appliedSeq)
		_ = lgmonDB.Close()
	}
	outboxPath := filepath.Join(target, "data", "outbox.db")
	_ = os.Remove(outboxPath)
	outDB, err := sql.Open("sqlite", outboxPath)
	if err != nil {
		return err
	}
	defer outDB.Close()
	outDB.SetMaxOpenConns(1)
	stmts := []string{
		`PRAGMA page_size = 4096`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS outbox_meta(singleton INTEGER PRIMARY KEY CHECK(singleton=1),collector TEXT NOT NULL,allocated INTEGER NOT NULL,acked INTEGER NOT NULL,gap_from INTEGER NOT NULL,gap_to INTEGER NOT NULL,gap_ts INTEGER NOT NULL,bytes INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS outbox_records(seq INTEGER PRIMARY KEY,queued_at INTEGER NOT NULL,payload BLOB NOT NULL,size INTEGER NOT NULL)`,
	}
	for _, stmt := range stmts {
		if _, err := outDB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := outDB.ExecContext(ctx, `INSERT OR REPLACE INTO outbox_meta(singleton,collector,allocated,acked,gap_from,gap_to,gap_ts,bytes) VALUES(1,?,?,?,0,0,0,0)`, collectorID, appliedSeq, appliedSeq); err != nil {
		return err
	}
	var busy, logPages, copied int
	_ = outDB.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logPages, &copied)
	return nil
}

func copyLogFileTail(src, dst string, keepBytes int64) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return 0, err
	}
	offset := int64(0)
	if info.Size() > keepBytes {
		offset = info.Size() - keepBytes
	}
	buf := make([]byte, info.Size()-offset)
	n, readErr := in.ReadAt(buf, offset)
	if readErr != nil && readErr != io.EOF && n == 0 {
		return 0, readErr
	}
	buf = buf[:n]
	if offset > 0 {
		for i, b := range buf {
			if b == '\n' && i+1 < len(buf) {
				buf = buf[i+1:]
				break
			}
		}
	}
	if err := os.WriteFile(dst, buf, 0o600); err != nil {
		return 0, err
	}
	return int64(len(buf)), nil
}

func copySQLiteSafely(ctx context.Context, src, dst string) error {
	_ = os.Remove(dst)
	db, err := sql.Open("sqlite", src)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		_, copyErr := copyFileChunked(ctx, src, dst, nil)
		return copyErr
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		_ = os.Remove(dst)
		_, copyErr := copyFileChunked(ctx, src, dst, nil)
		return copyErr
	}
	return nil
}

func copyFileChunked(ctx context.Context, src, dst string, onBytes func(int64)) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, migrationCopyChunk)
	var written int64
	for {
		if ctx.Err() != nil {
			out.Close()
			return written, ctx.Err()
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				out.Close()
				return written, writeErr
			}
			written += int64(n)
			if onBytes != nil {
				onBytes(int64(n))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			return written, readErr
		}
	}
	return written, out.Close()
}

func verifyMigratedFiles(ctx context.Context, files []migrationFile) error {
	for _, item := range files {
		info, err := os.Stat(item.dst)
		if err != nil {
			return fmt.Errorf("目标文件缺失（%s）：%w", item.rel, err)
		}
		if !item.isSQLDB && info.Size() != item.written {
			return fmt.Errorf("目标文件大小不一致（%s）", item.rel)
		}
		if item.isSQLDB {
			if info.Size() == 0 {
				return fmt.Errorf("迁移后的数据库为空（%s）", item.rel)
			}
			db, err := sql.Open("sqlite", item.dst)
			if err != nil {
				return fmt.Errorf("打开迁移后的数据库失败（%s）：%w", item.rel, err)
			}
			var check string
			err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check)
			_ = db.Close()
			if err != nil || !strings.EqualFold(check, "ok") {
				return fmt.Errorf("数据库完整性校验未通过（%s）：%v %s", item.rel, err, check)
			}
		}
	}
	return nil
}
