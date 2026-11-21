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
	_ "modernc.org/sqlite"
)

const (
	migrationCopyChunk   = 128 * 1024
	migrationMinHeadroom = 50 * 1024 * 1024
)

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
}

func (b *APIBackend) storageOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	if p, ok := api.CurrentPrincipal(ctx); ok && p.Guest {
		return nil, &api.Fault{Status: 403, Code: "guest_forbidden", Message: "内网访客模式不能查看或修改主机存储目录"}
	}
	if op == api.GetStorage {
		return b.storageStatus(), nil
	}
	var input api.StorageMigrateInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	return b.startStorageMigration(ctx, input.Target)
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
		if len(entries) > 0 {
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

func (b *APIBackend) startStorageMigration(ctx context.Context, rawTarget string) (api.StorageStatus, error) {
	source := filepath.Clean(strings.TrimSpace(b.options.Home))
	target, err := validateMigrationTarget(source, rawTarget, b.options.Executable)
	if err != nil {
		return api.StorageStatus{}, err
	}
	usage := measureHomeUsage(source)
	if drive, err := diskUsageForPath(target); err == nil && drive.TotalBytes > 0 {
		required := uint64(usage.total) + uint64(usage.total/5) + migrationMinHeadroom
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

func (b *APIBackend) runStorageMigration(ctx context.Context, source, target string) {
	// Step 1: Flush in-memory SQLite buffers so the snapshot is up to date.
	if flusher, ok := b.options.Write.(interface{ Flush(context.Context) error }); ok {
		_ = flusher.Flush(ctx)
	}
	files, totalBytes, err := collectMigrationFiles(source, target)
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

	// Step 2: Copy files (VACUUM INTO for SQLite databases, chunked stream copy for everything else).
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

	// Step 4: Write storage-redirect.json pointer so the next restart seamlessly boots into target.
	b.updateMigration(func(s *api.StorageMigrationState) {
		s.State = "switching"
		s.Percent = 95
		s.CurrentFile = config.HomeRedirectFile
	})
	redirectAnchors := b.redirectAnchorHomes(source)
	for _, anchor := range redirectAnchors {
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

// redirectAnchorHomes chooses which base home directory(ies) receive storage-redirect.json.
// For standard installations (C:\ProgramData\lg-monitor or an earlier redirect from it), updating
// the default base home guarantees Windows Services and Start Menu launches immediately switch on restart.
// For custom --home directories, writing the redirect in the source home lets that launch path follow too.
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

func collectMigrationFiles(source, target string) ([]migrationFile, int64, error) {
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
		base := strings.ToLower(entry.Name())
		// Skip pointer files, SQLite WAL/SHM sidecars (VACUUM INTO merges them cleanly), and temp files.
		if base == strings.ToLower(config.HomeRedirectFile) ||
			strings.HasSuffix(base, ".db-wal") ||
			strings.HasSuffix(base, ".db-shm") ||
			strings.HasSuffix(base, ".tmp") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		isSQLDB := strings.HasPrefix(strings.ToLower(slash), "data/") && strings.HasSuffix(base, ".db") && info.Size() > 0
		files = append(files, migrationFile{
			rel:     slash,
			src:     path,
			dst:     filepath.Join(target, rel),
			size:    info.Size(),
			isSQLDB: isSQLDB,
		})
		total += info.Size()
		return nil
	})
	return files, total, err
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
