package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

func (p *Outbox) EventLogBookmark(ctx context.Context, channel string) (string, bool, error) {
	state, exists, err := p.EventLogCheckpointState(ctx, channel)
	return state.Bookmark, exists, err
}

func (p *Outbox) EventLogCheckpointState(ctx context.Context, channel string) (pipeline.EventLogCheckpoint, bool, error) {
	checkpoint := pipeline.EventLogCheckpoint{Channel: channel}
	if !collector.EventLogChannel(channel) {
		return checkpoint, false, fmt.Errorf("事件日志只允许 Application 和 System")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return checkpoint, false, fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	var exists int
	if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='eventlog_checkpoints'`).Scan(&exists); err != nil {
		return checkpoint, false, fmt.Errorf("检查事件日志书签表失败：%w", err)
	}
	if exists == 0 {
		return checkpoint, false, nil
	}
	var fences []byte
	err := p.db.QueryRowContext(ctx, `SELECT bookmark,fences FROM eventlog_checkpoints WHERE channel=?`, channel).Scan(&checkpoint.Bookmark, &fences)
	if errors.Is(err, sql.ErrNoRows) {
		return checkpoint, false, nil
	}
	if err != nil {
		return checkpoint, false, fmt.Errorf("读取事件日志书签失败：%w", err)
	}
	if err := json.Unmarshal(fences, &checkpoint.Fences); err != nil {
		return checkpoint, false, fmt.Errorf("读取事件日志订阅边界失败：%w", err)
	}
	if err := checkpoint.Validate(); err != nil {
		return checkpoint, false, err
	}
	return checkpoint, true, nil
}

func (p *Outbox) WriteEventLogCheckpoint(ctx context.Context, messages []pipeline.Message, checkpoint pipeline.EventLogCheckpoint) error {
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	if len(messages) > MaxBatchRecords {
		return fmt.Errorf("事件日志缓存批次最多 256 条")
	}
	for _, message := range messages {
		if err := message.Validate(); err != nil {
			return err
		}
		if message.CollectorID != p.options.CollectorID || message.Kind == pipeline.KindDataGap {
			return fmt.Errorf("事件日志缓存消息身份不符或包含自行分配的缺口")
		}
		if _, err := json.Marshal(message); err != nil {
			return fmt.Errorf("事件日志消息不能安全持久化：%w", err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	if err := p.pruneAge(ctx); err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		state, err := cacheState(ctx, p.db)
		if err != nil {
			return err
		}
		if uint64(len(messages)) > pipeline.MaxSequence-state.Allocated {
			return fmt.Errorf("离线缓存序号已耗尽")
		}
		data := make([][]byte, len(messages))
		var total int64
		for index, message := range messages {
			message.Seq = state.Allocated + uint64(index) + 1
			record, err := Encode(message)
			if err != nil {
				return err
			}
			data[index], err = proto.Marshal(record)
			if err != nil {
				return fmt.Errorf("编码事件日志缓存批次失败：%w", err)
			}
			total += int64(len(data[index]))
		}
		if total > MaxBatchBytes {
			return fmt.Errorf("事件日志缓存批次超过 1 MiB，请拆分批次")
		}
		err = p.insertEventLogCheckpoint(ctx, state.Allocated, data, total, checkpoint)
		if err == nil {
			for _, record := range data {
				p.writes.record(p.options.Now(), int64(len(record))+cacheRecordOverhead)
			}
			if len(data) > 0 {
				p.saveRateSnapshot(ctx)
				p.signal()
			}
			return nil
		}
		var full *sqlite.Error
		if !errors.As(err, &full) || full.Code()&255 != 13 {
			return err
		}
		fences, _ := json.Marshal(checkpoint.Fences)
		needed := (total+int64(len(checkpoint.Bookmark)+len(fences))+4079)/4080 + int64(len(data)) + 8
		var pages, free int64
		if err := p.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
			return fmt.Errorf("读取事件缓存页数失败：%w", err)
		}
		if err := p.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
			return fmt.Errorf("读取事件缓存空闲页失败：%w", err)
		}
		if p.maxPages-(pages-free) >= needed {
			return fmt.Errorf("事件日志缓存写入磁盘失败，请检查可用空间：%w", full)
		}
		if err := p.freeCapacity(ctx, needed); err != nil {
			return err
		}
	}
	return fmt.Errorf("事件日志缓存容量整理后仍无法写入")
}

func (p *Outbox) insertEventLogCheckpoint(ctx context.Context, allocated uint64, data [][]byte, total int64, checkpoint pipeline.EventLogCheckpoint) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始事件日志缓存事务失败：%w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS eventlog_checkpoints(channel TEXT PRIMARY KEY,bookmark TEXT NOT NULL,fences BLOB NOT NULL)`); err != nil {
		return fmt.Errorf("初始化事件日志书签表失败：%w", err)
	}
	for index, record := range data {
		if _, err := tx.ExecContext(ctx, `INSERT INTO outbox_records(seq,queued_at,payload,size) VALUES(?,?,?,?)`, allocated+uint64(index)+1, p.options.Now().UnixMilli(), record, len(record)); err != nil {
			return fmt.Errorf("写入事件日志缓存记录失败：%w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE outbox_meta SET allocated=?,bytes=bytes+? WHERE singleton=1`, allocated+uint64(len(data)), total); err != nil {
		return fmt.Errorf("保存事件日志连续序号失败：%w", err)
	}
	fences, err := json.Marshal(checkpoint.Fences)
	if err != nil {
		return fmt.Errorf("编码事件日志订阅边界失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO eventlog_checkpoints(channel,bookmark,fences) VALUES(?,?,?) ON CONFLICT(channel) DO UPDATE SET bookmark=excluded.bookmark,fences=excluded.fences`, checkpoint.Channel, checkpoint.Bookmark, fences); err != nil {
		return fmt.Errorf("保存事件日志书签失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事件日志和书签失败：%w", err)
	}
	return nil
}
