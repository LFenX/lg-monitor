package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func (s *Store) LastCommitted(ctx context.Context, id string) (uint64, error) {
	var applied uint64
	err := s.db.QueryRowContext(ctx, `SELECT applied_seq FROM collector_offsets WHERE collector_id=?`, id).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("查询采集连续确认水位失败：%w", err)
	}
	return applied, nil
}

func (s *Store) Ingest(ctx context.Context, message pipeline.Message) (pipeline.Message, bool, error) {
	batch, err := s.IngestBatch(ctx, []pipeline.Message{message})
	if err != nil || len(batch.Messages) == 0 {
		return pipeline.Message{}, false, err
	}
	return batch.Messages[0], true, nil
}
func (s *Store) PendingCollectors(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT collector_id FROM collector_inbox ORDER BY collector_id`)
	if err != nil {
		return nil, fmt.Errorf("查询待应用采集器失败：%w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取待应用采集器失败：%w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历待应用采集器失败：%w", err)
	}
	return ids, nil
}

func (s *Store) PendingIngress(ctx context.Context, id string) (pipeline.Message, bool, error) {
	for {
		message, found, skipped, err := s.firstPending(ctx, id)
		if err != nil || !found || !skipped {
			return message, found, err
		}
		if err := s.CompleteIngress(ctx, id, message.Seq); err != nil {
			return pipeline.Message{}, false, err
		}
	}
}
func (s *Store) CompleteIngress(ctx context.Context, id string, seq uint64) error {
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var stored, applied uint64
		if err := tx.QueryRowContext(ctx, `SELECT stored_seq,applied_seq FROM collector_offsets WHERE collector_id=?`, id).Scan(&stored, &applied); err != nil {
			return fmt.Errorf("读取应用水位失败：%w", err)
		}
		if seq <= applied {
			return nil
		}
		if seq > stored {
			return fmt.Errorf("不能确认尚未存储的采集序号")
		}
		var payload string
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM collector_inbox WHERE collector_id=? AND seq=?`, id, seq).Scan(&payload); err != nil {
			return fmt.Errorf("查找应用确认消息失败：%w", err)
		}
		var message pipeline.Message
		if err := json.Unmarshal([]byte(payload), &message); err != nil {
			return fmt.Errorf("解码应用确认消息失败：%w", err)
		}
		if message.DataGap != nil {
			if message.DataGap.FromSeq != applied+1 {
				return fmt.Errorf("缺口不能跨过未应用的消息")
			}
		} else if seq != applied+1 {
			return fmt.Errorf("不能跨过未应用的采集序号")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collector_offsets SET applied_seq=? WHERE collector_id=?`, seq, id); err != nil {
			return fmt.Errorf("保存应用确认水位失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collector_inbox WHERE collector_id=? AND seq=?`, id, seq); err != nil {
			return fmt.Errorf("清理已应用消息失败：%w", err)
		}
		return nil
	})
}
