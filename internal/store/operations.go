package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/collector"
)

type OperationRequest struct {
	RequestID           string `json:"requestId"`
	CollectorID         string `json:"collectorId"`
	GrantID             string `json:"grantId"`
	Version             string `json:"version"`
	Service             string `json:"service"`
	Actor               string `json:"actor"`
	Reason              string `json:"reason"`
	State               string `json:"state" jsonschema:"enum=running,enum=succeeded,enum=failed,enum=denied,enum=unknown"`
	StartedAt           int64  `json:"startedAt"`
	FinishedAt          int64  `json:"finishedAt"`
	CollectorStartedAt  int64  `json:"collectorStartedAt"`
	CollectorFinishedAt int64  `json:"collectorFinishedAt"`
	Message             string `json:"message"`
}

const operationColumns = `request_id,collector_id,grant_id,policy_version,service,actor,reason,state,started_at,finished_at,collector_started_at,collector_finished_at,message`

func scanOperation(row interface{ Scan(...any) error }) (OperationRequest, error) {
	var v OperationRequest
	err := row.Scan(&v.RequestID, &v.CollectorID, &v.GrantID, &v.Version, &v.Service, &v.Actor, &v.Reason, &v.State, &v.StartedAt, &v.FinishedAt, &v.CollectorStartedAt, &v.CollectorFinishedAt, &v.Message)
	return v, err
}

func (s *Store) OperationRequest(ctx context.Context, id, hash string) (OperationRequest, error) {
	value, err := scanOperation(s.db.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operation_requests WHERE request_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return value, collector.ErrOperationMissing
	}
	if err != nil {
		return value, fmt.Errorf("读取监控操作账本失败：%w", err)
	}
	if hash != "" {
		var stored string
		if err := s.db.QueryRowContext(ctx, `SELECT request_hash FROM operation_requests WHERE request_id=?`, id).Scan(&stored); err != nil {
			return value, fmt.Errorf("核对操作请求身份失败：%w", err)
		}
		if stored != hash {
			return OperationRequest{}, collector.ErrOperationConflict
		}
	}
	return value, nil
}

func (s *Store) ClaimOperation(ctx context.Context, value OperationRequest, hash string) (OperationRequest, bool, error) {
	var stored OperationRequest
	fresh := false
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var previousHash string
		err := tx.QueryRowContext(ctx, `SELECT request_hash FROM operation_requests WHERE request_id=?`, value.RequestID).Scan(&previousHash)
		if err == nil {
			if previousHash != hash {
				return collector.ErrOperationConflict
			}
			stored, err = scanOperation(tx.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operation_requests WHERE request_id=?`, value.RequestID))
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("核对操作请求去重失败：%w", err)
		}
		value.State, value.StartedAt, value.Message = "running", s.now().UnixMilli(), "操作意图已保存，正在等待采集器结果"
		_, err = tx.ExecContext(ctx, `INSERT INTO operation_requests(request_id,request_hash,collector_id,grant_id,policy_version,service,actor,reason,state,started_at,message) VALUES(?,?,?,?,?,?,?,?,'running',?,?)`, value.RequestID, hash, value.CollectorID, value.GrantID, value.Version, value.Service, value.Actor, value.Reason, value.StartedAt, value.Message)
		if err != nil {
			return fmt.Errorf("保存监控操作意图失败：%w", err)
		}
		stored = value
		fresh = true
		return nil
	})
	return stored, fresh, err
}

func (s *Store) FinishOperation(ctx context.Context, id string, result collector.OperationResult) (OperationRequest, error) {
	var stored OperationRequest
	if !slices.Contains([]string{"running", "succeeded", "failed", "denied", "unknown"}, result.State) || result.StartedAt < 0 || result.FinishedAt < 0 || len(result.Message) > 4096 || !utf8.ValidString(result.Message) || (result.State == "succeeded" && (result.Service == "" || result.StartedAt == 0 || result.FinishedAt == 0)) {
		return stored, fmt.Errorf("采集器操作终态字段无效，未登记成功")
	}
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		stored, err = scanOperation(tx.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operation_requests WHERE request_id=?`, id))
		if err != nil {
			return fmt.Errorf("读取待记录操作失败：%w", err)
		}
		if result.RequestID != id || result.GrantID != stored.GrantID || result.Version != stored.Version || (result.Service != "" && result.Service != stored.Service) {
			return collector.ErrOperationConflict
		}
		if stored.State != "running" && stored.State != "unknown" {
			return nil
		}
		if result.State == "running" {
			return nil
		}
		stored.State, stored.Message = result.State, result.Message
		stored.FinishedAt = s.now().UnixMilli()
		stored.CollectorStartedAt, stored.CollectorFinishedAt = result.StartedAt, result.FinishedAt
		_, err = tx.ExecContext(ctx, `UPDATE operation_requests SET state=?,message=?,finished_at=?,collector_started_at=?,collector_finished_at=? WHERE request_id=?`, stored.State, stored.Message, stored.FinishedAt, stored.CollectorStartedAt, stored.CollectorFinishedAt, id)
		if err != nil {
			return fmt.Errorf("保存监控操作结果失败：%w", err)
		}
		return nil
	})
	return stored, err
}

func (s *Store) RecoverOperations(ctx context.Context) error {
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE operation_requests SET state='unknown',finished_at=?,message='监控器上次退出前未确认结果，请查询原请求或人工核对；不得自动重试' WHERE state='running'`, s.now().UnixMilli())
		if err != nil {
			return fmt.Errorf("恢复未完成操作意图失败：%w", err)
		}
		return nil
	})
}
