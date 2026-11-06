package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

func migrate(ctx context.Context, db *sql.DB, now func() time.Time) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("创建迁移记录失败：%w", err)
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("读取数据库迁移列表失败：%w", err)
	}
	type migration struct {
		file    string
		version int
	}
	ordered := make([]migration, 0, len(files))
	for _, file := range files {
		name := strings.TrimPrefix(file, "migrations/")
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil || version <= 0 {
			if err == nil {
				err = fmt.Errorf("版本号必须大于 0")
			}
			return fmt.Errorf("数据库迁移文件名无效：%w", err)
		}
		ordered = append(ordered, migration{file, version})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].version < ordered[j].version })
	for i, migration := range ordered {
		if i > 0 && migration.version == ordered[i-1].version {
			return fmt.Errorf("数据库迁移版本 %d 重复", migration.version)
		}
		if err := applyMigration(ctx, db, migration.file, migration.version, now); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, file string, version int, now func() time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始数据库迁移失败：%w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, version).Scan(&count); err != nil {
		return fmt.Errorf("读取迁移版本失败：%w", err)
	}
	if count != 0 {
		return nil
	}
	data, err := migrations.ReadFile(file)
	if err != nil {
		return fmt.Errorf("读取迁移文件失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, string(data)); err != nil {
		return fmt.Errorf("执行数据库迁移 %d 失败：%w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, now().UTC().UnixMilli()); err != nil {
		return fmt.Errorf("记录数据库迁移版本失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库迁移失败：%w", err)
	}
	return nil
}
