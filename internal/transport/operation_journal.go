package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/secrets"
)

type OperationJournalOptions struct {
	Path, CollectorID string
	Paths             secrets.PathPolicy
	Now               func() time.Time
}
type OperationJournal struct {
	options OperationJournalOptions
	mu      sync.Mutex
	db      *sql.DB
	closed  bool
}

func NewOperationJournal(options OperationJournalOptions) (*OperationJournal, error) {
	if options.Path == "" || options.CollectorID == "" || options.Now == nil || options.Paths == nil {
		return nil, fmt.Errorf("操作账本需要本机路径、采集器身份、路径保护和时钟")
	}
	return &OperationJournal{options: options}, nil
}
func (j *OperationJournal) open(ctx context.Context, create bool) error {
	if j.closed {
		return fmt.Errorf("采集器操作账本已关闭")
	}
	if err := j.options.Paths.Check(filepath.Dir(j.options.Path)); err != nil {
		return fmt.Errorf("操作账本目录未受保护：%w", err)
	}
	if _, err := os.Stat(j.options.Path); os.IsNotExist(err) {
		if !create {
			return collector.ErrOperationMissing
		}
	} else if err != nil {
		return fmt.Errorf("读取操作账本路径失败：%w", err)
	} else if err := j.options.Paths.Check(j.options.Path); err != nil {
		return fmt.Errorf("操作账本文件未受保护：%w", err)
	}
	if j.db != nil {
		return nil
	}
	path, err := filepath.Abs(j.options.Path)
	if err != nil {
		return fmt.Errorf("解析操作账本路径失败：%w", err)
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	uri.RawQuery = url.Values{"_pragma": {"journal_mode(WAL)", "busy_timeout(2000)", "synchronous(FULL)"}, "_txlock": {"immediate"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return fmt.Errorf("打开操作账本失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	failed := true
	defer func() {
		if failed {
			db.Close()
		}
	}()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS operation_meta(singleton INTEGER PRIMARY KEY CHECK(singleton=1),collector_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS operation_receipts(request_id TEXT PRIMARY KEY,command_hash TEXT NOT NULL,state TEXT NOT NULL,record_json TEXT NOT NULL);
INSERT OR IGNORE INTO operation_meta(singleton,collector_id) VALUES(1,?);`, j.options.CollectorID); err != nil {
		return fmt.Errorf("初始化操作账本失败：%w", err)
	}
	var owner string
	if err := db.QueryRowContext(ctx, `SELECT collector_id FROM operation_meta WHERE singleton=1`).Scan(&owner); err != nil || owner != j.options.CollectorID {
		return fmt.Errorf("操作账本的采集器身份不一致，请恢复同一份身份和账本")
	}
	if _, err := db.ExecContext(ctx, `UPDATE operation_receipts SET state='unknown',record_json=json_set(record_json,'$.state','unknown','$.finishedAt',?,'$.message','采集器上次退出前未记录操作终态，请人工核对；不得自动重试') WHERE state='running'`, j.options.Now().UnixMilli()); err != nil {
		return fmt.Errorf("恢复未完成操作记录失败：%w", err)
	}
	j.db = db
	failed = false
	return nil
}

func (j *OperationJournal) Result(ctx context.Context, id string) (collector.OperationResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var result collector.OperationResult
	if err := j.open(ctx, false); err != nil {
		return result, err
	}
	var data string
	err := j.db.QueryRowContext(ctx, `SELECT record_json FROM operation_receipts WHERE request_id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return result, collector.ErrOperationMissing
	}
	if err != nil {
		return result, fmt.Errorf("读取操作账本失败：%w", err)
	}
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return result, fmt.Errorf("读取操作记录格式失败：%w", err)
	}
	return result, nil
}

func (j *OperationJournal) Claim(ctx context.Context, command collector.OperationCommand, service string, at int64) (collector.OperationResult, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var result collector.OperationResult
	if err := j.open(ctx, true); err != nil {
		return result, false, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return result, false, fmt.Errorf("开启操作意图事务失败：%w", err)
	}
	defer tx.Rollback()
	var hash, data string
	err = tx.QueryRowContext(ctx, `SELECT command_hash,record_json FROM operation_receipts WHERE request_id=?`, command.RequestID).Scan(&hash, &data)
	if err == nil {
		if hash != command.Hash() {
			return result, false, collector.ErrOperationConflict
		}
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return result, false, fmt.Errorf("解码已登记操作失败：%w", err)
		}
		if result.Service != service {
			return result, false, collector.ErrOperationConflict
		}
		return result, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, false, fmt.Errorf("核对操作身份失败：%w", err)
	}
	result = collector.OperationResult{RequestID: command.RequestID, GrantID: command.GrantID, Version: command.Version, Service: service, StartedAt: at, State: "running", Message: "操作意图已持久保存，尚未确认最终状态"}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, false, fmt.Errorf("编码操作意图失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_receipts(request_id,command_hash,state,record_json) VALUES(?,?,'running',?)`, command.RequestID, command.Hash(), string(encoded)); err != nil {
		return collector.OperationResult{}, false, fmt.Errorf("保存操作意图失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return collector.OperationResult{}, false, fmt.Errorf("提交操作意图失败：%w", err)
	}
	return result, true, nil
}

func (j *OperationJournal) Finish(ctx context.Context, result collector.OperationResult) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.open(ctx, false); err != nil {
		return err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("编码操作终态失败：%w", err)
	}
	updated, err := j.db.ExecContext(ctx, `UPDATE operation_receipts SET state=?,record_json=? WHERE request_id=? AND state='running'`, result.State, string(data), result.RequestID)
	if err != nil {
		return fmt.Errorf("保存操作终态失败：%w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("核对操作终态提交失败：%w", err)
	}
	if count != 1 {
		return collector.ErrOperationConflict
	}
	return nil
}
func (j *OperationJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
	if j.db != nil {
		return j.db.Close()
	}
	return nil
}
