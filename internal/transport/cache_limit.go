package transport

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type CacheCompactor interface {
	Compact(context.Context, *sql.DB) error
}

type nativeCacheCompactor struct{}

func (nativeCacheCompactor) Compact(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("整理缓存文件失败：%w", err)
	}
	var busy, log, checkpointed int64
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
		return fmt.Errorf("缩小缓存预写日志失败：%w", err)
	}
	if busy != 0 {
		return fmt.Errorf("缓存读事务尚未结束，暂未缩小预写日志")
	}
	return nil
}

func (p *Outbox) requestedPages() int64 {
	return (p.options.MaxBytes - cacheWALReserve) / cachePageSize
}

func (p *Outbox) initializeUsage(ctx context.Context) error {
	rows, err := p.db.QueryContext(ctx, "PRAGMA table_info(outbox_meta)")
	if err != nil {
		return fmt.Errorf("读取缓存统计字段失败：%w", err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("读取缓存统计字段失败：%w", err)
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("遍历缓存统计字段失败：%w", err)
	}
	for _, column := range []struct{ name, definition string }{
		{"max_bytes", "INTEGER NOT NULL DEFAULT 0"}, {"desired_max_bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"bytes_per_hour", "REAL"}, {"resize_message", "TEXT NOT NULL DEFAULT ''"},
	} {
		if !columns[column.name] {
			if _, err := p.db.ExecContext(ctx, "ALTER TABLE outbox_meta ADD COLUMN "+column.name+" "+column.definition); err != nil {
				return fmt.Errorf("增加缓存统计字段失败：%w", err)
			}
		}
	}
	return nil
}

func (p *Outbox) saveLimit(ctx context.Context, resetRate bool) error {
	query := "UPDATE outbox_meta SET max_bytes=?,desired_max_bytes=?,resize_message=?"
	if resetRate {
		query += ",bytes_per_hour=NULL"
	}
	_, err := p.db.ExecContext(ctx, query+" WHERE singleton=1", p.effectiveMaxBytes, p.options.MaxBytes, p.resizeMessage)
	p.limitDirty = err != nil
	if err != nil {
		return fmt.Errorf("保存缓存生效上限失败：%w", err)
	}
	return nil
}

func (p *Outbox) trySmallerLimit(ctx context.Context) {
	if p.effectiveMaxBytes == p.options.MaxBytes || p.options.Now().Before(p.nextResizeTry) {
		return
	}
	used, err := cacheUsedBytes(ctx, p.db)
	if err == nil && used <= p.requestedPages()*cachePageSize {
		p.nextResizeTry = p.options.Now().Add(time.Minute)
		err = p.options.Compactor.Compact(ctx, p.db)
		if err == nil {
			var actual int64
			err = p.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", p.requestedPages())).Scan(&actual)
			if err == nil && actual <= p.requestedPages() {
				p.maxPages, p.effectiveMaxBytes, p.resizeMessage = actual, p.options.MaxBytes, ""
				if err := p.saveLimit(ctx, false); err != nil {
					p.options.Logger.Warn("Cache limit status could not be saved")
				}
				return
			}
			if err == nil {
				err = fmt.Errorf("缓存整理后文件仍超过新的页数上限")
			}
		}
	}
	p.resizeMessage = fmt.Sprintf("新的缓存上限 %.1f MB 小于未发送的数据，暂按 %.1f MB 运行，补发完成后自动生效。", float64(p.options.MaxBytes)/1e6, float64(p.effectiveMaxBytes)/1e6)
	if err != nil {
		p.resizeMessage = fmt.Sprintf("新的缓存上限 %.1f MB 暂未生效，暂按 %.1f MB 运行；缓存整理失败：%s。缓存清空且可整理后自动生效。", float64(p.options.MaxBytes)/1e6, float64(p.effectiveMaxBytes)/1e6, strings.TrimSpace(err.Error()))
	}
	p.options.Logger.Warn("Smaller cache limit is deferred until pending records drain", "requestedBytes", p.options.MaxBytes, "effectiveBytes", p.effectiveMaxBytes, "reason", p.resizeMessage)
	if err := p.saveLimit(ctx, false); err != nil {
		p.options.Logger.Warn("Deferred cache limit status could not be saved")
	}
}

func (p *Outbox) applyEmptyLimit(ctx context.Context) {
	if p.effectiveMaxBytes == p.options.MaxBytes {
		return
	}
	state, err := cacheState(ctx, p.db)
	if err == nil && state.Acked == state.Allocated {
		p.trySmallerLimit(ctx)
	}
}
