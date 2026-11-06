package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type IngressBatch struct {
	Messages []pipeline.Message
	Through  uint64
}

func decodeInbox(id string, seq uint64, payload string) (pipeline.Message, error) {
	var message pipeline.Message
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		return message, fmt.Errorf("解码待应用消息失败：%w", err)
	}
	if message.CollectorID != id || message.Seq != seq {
		return message, fmt.Errorf("待应用消息的身份或序号与索引不符")
	}
	return message, message.Validate()
}

func (s *Store) IngestBatch(ctx context.Context, input []pipeline.Message) (IngressBatch, error) {
	result := IngressBatch{}
	if len(input) == 0 || len(input) > 256 {
		return result, fmt.Errorf("采集批次须包含 1～256 条记录")
	}
	id := input[0].CollectorID
	owned := make([]pipeline.Message, len(input))
	for i, message := range input {
		if err := message.Validate(); err != nil {
			return result, err
		}
		if message.CollectorID != id || i > 0 && message.Seq <= input[i-1].Seq {
			return result, fmt.Errorf("采集批次的身份不一致或序号未递增")
		}
		payload, err := json.Marshal(message)
		if err != nil {
			return result, fmt.Errorf("复制接收批次失败：%w", err)
		}
		if err := json.Unmarshal(payload, &owned[i]); err != nil {
			return result, fmt.Errorf("解码接收批次副本失败：%w", err)
		}
	}
	result.Through = input[len(input)-1].Seq
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO collector_offsets(collector_id) VALUES(?) ON CONFLICT DO NOTHING`, id); err != nil {
			return fmt.Errorf("初始化采集水位失败：%w", err)
		}
		var stored, applied uint64
		if err := tx.QueryRowContext(ctx, `SELECT stored_seq,applied_seq FROM collector_offsets WHERE collector_id=?`, id).Scan(&stored, &applied); err != nil {
			return fmt.Errorf("读取批次采集水位失败：%w", err)
		}
		initialStored := stored
		var fresh []pipeline.Message
		var records []record
		for _, message := range owned {
			if message.Seq <= applied {
				continue
			}
			if message.Seq <= initialStored {
				var payload string
				var skipped bool
				if err := tx.QueryRowContext(ctx, `SELECT payload,skipped FROM collector_inbox WHERE collector_id=? AND seq=?`, id, message.Seq).Scan(&payload, &skipped); err != nil {
					return fmt.Errorf("读取重复批次待应用记录失败：%w", err)
				}
				canonical, err := decodeInbox(id, message.Seq, payload)
				if err != nil {
					return err
				}
				if !skipped {
					result.Messages = append(result.Messages, canonical)
				}
				continue
			}
			if initialStored != applied {
				return fmt.Errorf("上一批消息尚未应用，暂不接受下一批")
			}
			if message.DataGap != nil {
				if message.DataGap.FromSeq > stored+1 {
					return fmt.Errorf("缺口起点超过所需连续序号")
				}
				message.DataGap.FromSeq, message.DataGap.Dropped = stored+1, message.Seq-stored
			} else if message.Seq != stored+1 {
				return fmt.Errorf("采集序号不连续，拒绝跳过未存储数据")
			}
			row, err := encodeRecord(message)
			if err != nil {
				return err
			}
			fresh, records = append(fresh, message), append(records, row)
			stored = message.Seq
		}
		if len(fresh) == 0 {
			return nil
		}
		accepted, err := appendRecordsChecked(ctx, tx, records, s.now().UnixMilli())
		if err != nil {
			return err
		}
		statement, err := tx.PrepareContext(ctx, `INSERT INTO collector_inbox(collector_id,seq,payload,skipped) VALUES(?,?,?,?)`)
		if err != nil {
			return fmt.Errorf("准备批次待应用记录失败：%w", err)
		}
		defer statement.Close()
		prefixSkipped := true
		for i, message := range fresh {
			if prefixSkipped && !accepted[i] {
				applied = message.Seq
				continue
			}
			prefixSkipped = false
			payload, err := json.Marshal(message)
			if err != nil {
				return fmt.Errorf("编码待应用批次失败：%w", err)
			}
			if _, err := statement.ExecContext(ctx, id, message.Seq, string(payload), !accepted[i]); err != nil {
				return fmt.Errorf("保存批次待应用记录失败：%w", err)
			}
			if accepted[i] {
				result.Messages = append(result.Messages, message)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collector_offsets SET stored_seq=?,applied_seq=? WHERE collector_id=?`, stored, applied, id); err != nil {
			return fmt.Errorf("提交批次存储水位失败：%w", err)
		}
		return nil
	})
	return result, err
}

func (s *Store) PendingIngressBatch(ctx context.Context, id string) (IngressBatch, error) {
	result := IngressBatch{}
	rows, err := s.db.QueryContext(ctx, `SELECT seq,payload,skipped FROM collector_inbox WHERE collector_id=? ORDER BY seq LIMIT 256`, id)
	if err != nil {
		return result, fmt.Errorf("查询待应用批次失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		var skipped bool
		var seq uint64
		if err := rows.Scan(&seq, &payload, &skipped); err != nil {
			return result, fmt.Errorf("读取待应用批次失败：%w", err)
		}
		message, err := decodeInbox(id, seq, payload)
		if err != nil {
			return result, err
		}
		result.Through = seq
		if !skipped {
			result.Messages = append(result.Messages, message)
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("遍历待应用批次失败：%w", err)
	}
	return result, nil
}

func (s *Store) CompleteIngressBatch(ctx context.Context, id string, through uint64) error {
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var stored, applied uint64
		if err := tx.QueryRowContext(ctx, `SELECT stored_seq,applied_seq FROM collector_offsets WHERE collector_id=?`, id).Scan(&stored, &applied); err != nil {
			return fmt.Errorf("读取批次应用水位失败：%w", err)
		}
		if through <= applied {
			return nil
		}
		if through > stored {
			return fmt.Errorf("不能确认尚未存储的采集序号")
		}
		rows, err := tx.QueryContext(ctx, `SELECT seq,payload FROM collector_inbox WHERE collector_id=? AND seq>? AND seq<=? ORDER BY seq`, id, applied, through)
		if err != nil {
			return fmt.Errorf("读取批次确认记录失败：%w", err)
		}
		for rows.Next() {
			var seq uint64
			var payload string
			if err := rows.Scan(&seq, &payload); err != nil {
				rows.Close()
				return fmt.Errorf("读取确认记录字段失败：%w", err)
			}
			message, err := decodeInbox(id, seq, payload)
			if err != nil {
				rows.Close()
				return err
			}
			if message.DataGap != nil && message.DataGap.FromSeq != applied+1 || message.DataGap == nil && seq != applied+1 {
				rows.Close()
				return fmt.Errorf("批次确认不能跨过未应用的采集序号")
			}
			applied = seq
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("遍历批次确认失败：%w", err)
		}
		if applied != through {
			return fmt.Errorf("批次缺少所需待应用记录")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collector_offsets SET applied_seq=? WHERE collector_id=?`, through, id); err != nil {
			return fmt.Errorf("保存批次确认水位失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collector_inbox WHERE collector_id=? AND seq<=?`, id, through); err != nil {
			return fmt.Errorf("清理已应用批次失败：%w", err)
		}
		return nil
	})
}

func (s *Store) firstPending(ctx context.Context, id string) (pipeline.Message, bool, bool, error) {
	var seq uint64
	var payload string
	var skipped bool
	err := s.db.QueryRowContext(ctx, `SELECT seq,payload,skipped FROM collector_inbox WHERE collector_id=? ORDER BY seq LIMIT 1`, id).Scan(&seq, &payload, &skipped)
	if errors.Is(err, sql.ErrNoRows) {
		return pipeline.Message{}, false, false, nil
	}
	if err != nil {
		return pipeline.Message{}, false, false, fmt.Errorf("查询首条待应用记录失败：%w", err)
	}
	message, err := decodeInbox(id, seq, payload)
	return message, true, skipped, err
}
