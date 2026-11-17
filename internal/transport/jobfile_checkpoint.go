package transport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func (p *Outbox) JobFileCheckpointState(ctx context.Context, jobID string) (pipeline.JobFileCheckpoint, bool, error) {
	checkpoint := pipeline.JobFileCheckpoint{JobID: jobID}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return checkpoint, false, fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	var exists int
	if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='job_file_checkpoints'`).Scan(&exists); err != nil {
		return checkpoint, false, fmt.Errorf("检查任务事件读取位置表失败：%w", err)
	}
	if exists == 0 {
		return checkpoint, false, nil
	}
	var failing int
	err := p.db.QueryRowContext(ctx, `SELECT path,offset,line,head_len,head_hash,failing FROM job_file_checkpoints WHERE job_id=?`, jobID).Scan(&checkpoint.Path, &checkpoint.Offset, &checkpoint.Line, &checkpoint.HeadLen, &checkpoint.HeadHash, &failing)
	if errors.Is(err, sql.ErrNoRows) {
		return checkpoint, false, nil
	}
	if err != nil {
		return checkpoint, false, fmt.Errorf("读取任务事件读取位置失败：%w", err)
	}
	checkpoint.Failing = failing != 0
	if err := checkpoint.Validate(); err != nil {
		return checkpoint, false, err
	}
	return checkpoint, true, nil
}

func (p *Outbox) WriteJobFileCheckpoint(ctx context.Context, messages []pipeline.Message, checkpoint pipeline.JobFileCheckpoint) error {
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	return p.writeWithState(ctx, "任务事件", messages, int64(len(checkpoint.Path)+len(checkpoint.HeadHash)+64), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS job_file_checkpoints(job_id TEXT PRIMARY KEY,path TEXT NOT NULL,offset INTEGER NOT NULL,line INTEGER NOT NULL,head_len INTEGER NOT NULL,head_hash TEXT NOT NULL,failing INTEGER NOT NULL)`); err != nil {
			return fmt.Errorf("初始化任务事件读取位置表失败：%w", err)
		}
		failing := 0
		if checkpoint.Failing {
			failing = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_file_checkpoints(job_id,path,offset,line,head_len,head_hash,failing) VALUES(?,?,?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET path=excluded.path,offset=excluded.offset,line=excluded.line,head_len=excluded.head_len,head_hash=excluded.head_hash,failing=excluded.failing`,
			checkpoint.JobID, checkpoint.Path, checkpoint.Offset, int64(checkpoint.Line), checkpoint.HeadLen, checkpoint.HeadHash, failing); err != nil {
			return fmt.Errorf("保存任务事件读取位置失败：%w", err)
		}
		return nil
	})
}

// jobFileAssigned reports whether the monitor gave this collector the job's file to read. Anything else is
// dropped on arrival, so one collector cannot write another job's history.
func (r *Receiver) jobFileAssigned(identity Hello, jobID string) bool {
	source, ok := r.config.Source.(interface{ Snapshot() *config.Config })
	if !ok {
		return false
	}
	for _, target := range source.Snapshot().Targets {
		if target.ID == jobID {
			return target.Job != nil && target.Job.Source.Kind == "file" && (target.Collector == identity.CollectorID || identity.Hostname != "" && strings.EqualFold(target.Collector, identity.Hostname))
		}
	}
	return false
}

// ignoredJobEvent keeps the record's sequence number, so acknowledgements stay continuous, and says why it was dropped.
func ignoredJobEvent(message pipeline.Message) pipeline.Message {
	reason := fmt.Sprintf("批处理任务 %s 的事件文件没有分配给这个采集器，已忽略", message.JobEvent.JobID)
	return pipeline.Message{CollectorID: message.CollectorID, Seq: message.Seq, TS: message.TS, Kind: pipeline.KindDataGap, DataGap: &pipeline.DataGap{FromSeq: message.Seq, ToSeq: message.Seq, Dropped: 1, Reason: reason}}
}
