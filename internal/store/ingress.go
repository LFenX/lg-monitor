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
	if err := message.Validate(); err != nil {
		return pipeline.Message{}, false, err
	}
	data, err := json.Marshal(message)
	if err != nil {
		return pipeline.Message{}, false, fmt.Errorf("编码接收消息失败：%w", err)
	}
	var canonical pipeline.Message
	var apply bool
	err = s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO collector_offsets(collector_id) VALUES(?) ON CONFLICT DO NOTHING`, message.CollectorID); err != nil {
			return fmt.Errorf("初始化采集水位失败：%w", err)
		}
		var stored, applied uint64
		if err := tx.QueryRowContext(ctx, `SELECT stored_seq,applied_seq FROM collector_offsets WHERE collector_id=?`, message.CollectorID).Scan(&stored, &applied); err != nil {
			return fmt.Errorf("读取采集水位失败：%w", err)
		}
		if message.Seq <= applied {
			return nil
		}
		if message.Seq <= stored {
			var payload string
			if err := tx.QueryRowContext(ctx, `SELECT payload FROM collector_inbox WHERE collector_id=? AND seq=?`, message.CollectorID, message.Seq).Scan(&payload); err != nil {
				return fmt.Errorf("读取重复消息的待应用记录失败：%w", err)
			}
			if err := json.Unmarshal([]byte(payload), &canonical); err != nil {
				return fmt.Errorf("解码待应用消息失败：%w", err)
			}
			apply = true
			return nil
		}
		if stored != applied {
			return fmt.Errorf("上一条消息尚未应用，暂不接受下一序号")
		}
		if err := json.Unmarshal(data, &canonical); err != nil {
			return fmt.Errorf("复制采集消息失败：%w", err)
		}
		if canonical.DataGap != nil {
			if canonical.DataGap.FromSeq > stored+1 {
				return fmt.Errorf("缺口起点超过所需连续序号")
			}
			// A reconnect can expand a previously stored gap; count only positions not stored before.
			canonical.DataGap.FromSeq = stored + 1
			canonical.DataGap.Dropped = canonical.Seq - stored
		} else if canonical.Seq != stored+1 {
			return fmt.Errorf("采集序号不连续，拒绝跳过未存储数据")
		}
		row, err := encodeRecord(canonical)
		if err != nil {
			return err
		}
		if err := appendRecords(ctx, tx, []record{row}, s.now().UnixMilli()); err != nil {
			return err
		}
		payload, err := json.Marshal(canonical)
		if err != nil {
			return fmt.Errorf("编码待应用消息失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO collector_inbox(collector_id,seq,payload) VALUES(?,?,?)`, canonical.CollectorID, canonical.Seq, string(payload)); err != nil {
			return fmt.Errorf("保存待应用消息失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collector_offsets SET stored_seq=? WHERE collector_id=?`, canonical.Seq, canonical.CollectorID); err != nil {
			return fmt.Errorf("提交采集存储水位失败：%w", err)
		}
		apply = true
		return nil
	})
	return canonical, apply, err
}

func (s *Store) PendingCollectors(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT collector_id FROM collector_inbox ORDER BY collector_id`)
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
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM collector_inbox WHERE collector_id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return pipeline.Message{}, false, nil
	}
	if err != nil {
		return pipeline.Message{}, false, fmt.Errorf("查询待应用消息失败：%w", err)
	}
	var message pipeline.Message
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		return message, false, fmt.Errorf("解码待应用消息失败：%w", err)
	}
	if message.CollectorID != id {
		return message, false, fmt.Errorf("待应用消息的身份与索引不符")
	}
	if err := message.Validate(); err != nil {
		return message, false, err
	}
	return message, true, nil
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
