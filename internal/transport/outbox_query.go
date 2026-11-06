package transport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/protobuf/proto"
)

func (p *Outbox) Prune(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	return p.pruneAge(ctx)
}

func (p *Outbox) pruneAge(ctx context.Context) error {
	cutoff := p.options.Now().Add(-p.options.MaxAge).UnixMilli()
	for {
		var last sql.NullInt64
		if err := p.db.QueryRowContext(ctx, `SELECT max(seq) FROM outbox_records WHERE queued_at<?`, cutoff).Scan(&last); err != nil {
			return fmt.Errorf("查找过期离线数据失败：%w", err)
		}
		if !last.Valid {
			return nil
		}
		if _, err := p.dropPrefix(ctx, uint64(last.Int64), 128); err != nil {
			return err
		}
	}
}

func (p *Outbox) freeCapacity(ctx context.Context, needed int64) error {
	for {
		var pages, free int64
		if err := p.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
			return fmt.Errorf("读取缓存页数失败：%w", err)
		}
		if err := p.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
			return fmt.Errorf("读取缓存空闲页失败：%w", err)
		}
		if p.maxPages-(pages-free) >= needed+8 {
			return nil
		}
		deficit := (needed + 8 - (p.maxPages - (pages - free))) * cachePageSize
		rows, err := p.db.QueryContext(ctx, `SELECT size FROM outbox_records ORDER BY seq LIMIT 128`)
		if err != nil {
			return fmt.Errorf("估算缓存最旧前缀失败：%w", err)
		}
		count := 0
		for rows.Next() {
			var size int64
			if err := rows.Scan(&size); err != nil {
				rows.Close()
				return fmt.Errorf("读取缓存记录大小失败：%w", err)
			}
			count++
			deficit -= size
			if deficit <= 0 {
				break
			}
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return fmt.Errorf("遍历缓存大小失败：%w", readErr)
		}
		if count == 0 {
			return fmt.Errorf("缓存没有可释放的旧记录")
		}
		dropped, err := p.dropPrefix(ctx, pipeline.MaxSequence, count)
		if err != nil {
			return err
		}
		if !dropped {
			return fmt.Errorf("缓存已经没有可丢弃的旧记录，仍无法释放所需空间")
		}
	}
}

func (p *Outbox) dropPrefix(ctx context.Context, through uint64, count int) (bool, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开始缓存整理失败：%w", err)
	}
	defer tx.Rollback()
	// Small deletion transactions also bound the WAL when a large cache expires at once.
	var boundary sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(seq) FROM (SELECT seq FROM outbox_records WHERE seq<=? ORDER BY seq LIMIT ?)`, through, count).Scan(&boundary); err != nil {
		return false, fmt.Errorf("查找缓存最旧前缀失败：%w", err)
	}
	if !boundary.Valid {
		return false, nil
	}
	state, err := cacheState(ctx, tx)
	if err != nil {
		return false, err
	}
	var removedBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(size),0) FROM outbox_records WHERE seq<=?`, boundary.Int64).Scan(&removedBytes); err != nil {
		return false, fmt.Errorf("计算丢弃缓存大小失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM outbox_records WHERE seq<=?`, boundary.Int64); err != nil {
		return false, fmt.Errorf("丢弃最旧缓存失败：%w", err)
	}
	from := state.GapFrom
	if from == 0 {
		from = state.Acked + 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE outbox_meta SET gap_from=?,gap_to=?,gap_ts=?,bytes=bytes-? WHERE singleton=1`, from, max(state.GapTo, uint64(boundary.Int64)), p.options.Now().UnixMilli(), removedBytes); err != nil {
		return false, fmt.Errorf("记录数据缺口失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("提交缓存整理失败：%w", err)
	}
	p.signal()
	return true, nil
}

func (p *Outbox) Next(ctx context.Context) (pipeline.Message, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return pipeline.Message{}, false, fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	if err := p.pruneAge(ctx); err != nil {
		return pipeline.Message{}, false, err
	}
	state, err := cacheState(ctx, p.db)
	if err != nil {
		return pipeline.Message{}, false, err
	}
	if state.GapTo > state.Acked {
		gap := &pipeline.DataGap{FromSeq: state.Acked + 1, ToSeq: state.GapTo, Dropped: state.GapTo - state.Acked, Reason: "离线缓存超过保留时间或磁盘上限，已丢弃最旧数据"}
		return pipeline.Message{CollectorID: p.options.CollectorID, Seq: state.GapTo, TS: state.GapTS, Kind: pipeline.KindDataGap, DataGap: gap}, true, nil
	}
	var seq uint64
	var payload []byte
	err = p.db.QueryRowContext(ctx, `SELECT seq,payload FROM outbox_records WHERE seq>? ORDER BY seq LIMIT 1`, state.Acked).Scan(&seq, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		if state.Allocated != state.Acked {
			return pipeline.Message{}, false, fmt.Errorf("缓存序号存在但记录和缺口均缺失，请恢复完整缓存")
		}
		return pipeline.Message{}, false, nil
	}
	if err != nil {
		return pipeline.Message{}, false, fmt.Errorf("读取待补发消息失败：%w", err)
	}
	if seq != state.Acked+1 {
		return pipeline.Message{}, false, fmt.Errorf("缓存序号不连续且没有缺口记录，请恢复缓存")
	}
	var record lgmonv1.Record
	if err := proto.Unmarshal(payload, &record); err != nil {
		return pipeline.Message{}, false, fmt.Errorf("缓存消息损坏，请恢复备份：%w", err)
	}
	message, err := Decode(p.options.CollectorID, &record)
	if err != nil {
		return pipeline.Message{}, false, err
	}
	if message.Seq != seq {
		return pipeline.Message{}, false, fmt.Errorf("缓存记录头与索引序号不符")
	}
	return message, true, nil
}

func (p *Outbox) SyncResume(ctx context.Context, next uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if next == 0 || next > pipeline.MaxSequence {
		return fmt.Errorf("监控器断点序号无效：%w", ErrCacheDiverged)
	}
	state, err := cacheState(ctx, p.db)
	if err != nil {
		return err
	}
	if next-1 < state.Acked {
		return fmt.Errorf("监控器确认水位低于已确认缓存，请恢复一致的数据库备份：%w", ErrCacheDiverged)
	}
	if next-1 > state.Allocated {
		return fmt.Errorf("本地缓存序号落后于监控器，请恢复原 outbox 与采集器身份，不要删除未确认数据：%w", ErrCacheDiverged)
	}
	return p.acknowledge(ctx, next-1)
}

func (p *Outbox) Acknowledge(ctx context.Context, seq uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.acknowledge(ctx, seq)
}

func (p *Outbox) acknowledge(ctx context.Context, seq uint64) error {
	if p.closed {
		return fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始缓存确认事务失败：%w", err)
	}
	defer tx.Rollback()
	state, err := cacheState(ctx, tx)
	if err != nil {
		return err
	}
	if seq > state.Allocated {
		return fmt.Errorf("确认序号超过已持久化消息，拒绝删除")
	}
	if seq <= state.Acked {
		return nil
	}
	var removedBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(size),0) FROM outbox_records WHERE seq<=?`, seq).Scan(&removedBytes); err != nil {
		return fmt.Errorf("计算已确认缓存失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM outbox_records WHERE seq<=?`, seq); err != nil {
		return fmt.Errorf("删除已确认缓存失败：%w", err)
	}
	from, to := state.GapFrom, state.GapTo
	if seq >= to {
		from, to = 0, 0
	} else if from > 0 {
		from = seq + 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE outbox_meta SET acked=?,gap_from=?,gap_to=?,bytes=bytes-? WHERE singleton=1`, seq, from, to, removedBytes); err != nil {
		return fmt.Errorf("保存已确认水位失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交缓存确认失败：%w", err)
	}
	return nil
}
