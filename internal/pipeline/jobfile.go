// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package pipeline

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// JobFileCheckpoint is how far a job's file has been read. It is saved in the same transaction as the events
// read up to it, so a restart neither skips nor repeats lines.
type JobFileCheckpoint struct {
	JobID  string `json:"jobId"`
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Line   uint64 `json:"line"`
	// HeadLen bytes from the start of the file hash to HeadHash; a different start means a different file.
	HeadLen  int    `json:"headLen"`
	HeadHash string `json:"headHash"`
	// Failing is set while the file cannot be read, so the problem and the recovery are each reported once.
	Failing bool `json:"failing"`
}

var jobCheckpointID = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func (c JobFileCheckpoint) Validate() error {
	hash, err := hex.DecodeString(c.HeadHash)
	switch {
	case !jobCheckpointID.MatchString(c.JobID) || c.Path == "" || utf8.RuneCountInString(c.Path) > 260 || !utf8.ValidString(c.Path):
		return fmt.Errorf("任务事件文件的读取位置缺少任务 ID 或路径")
	case c.Offset < 0 || c.HeadLen < 0 || c.HeadLen > 4096 || c.Line > MaxSequence:
		return fmt.Errorf("任务事件文件的读取位置无效")
	case err != nil || c.HeadLen > 0 && len(hash) != 32 || c.HeadLen == 0 && c.HeadHash != "":
		return fmt.Errorf("任务事件文件的开头摘要无效")
	}
	return nil
}

type JobFileCheckpointSink interface {
	JobFileCheckpointState(context.Context, string) (JobFileCheckpoint, bool, error)
	WriteJobFileCheckpoint(context.Context, []Message, JobFileCheckpoint) error
}

// EmitJobFileCheckpoint queues a job file's events and the position after them as one write.
func (e *Emitter) EmitJobFileCheckpoint(ctx context.Context, messages []Message, checkpoint JobFileCheckpoint) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("任务事件提交已取消：%w", err)
	}
	sink, ok := e.sink.(JobFileCheckpointSink)
	if !ok {
		return fmt.Errorf("任务事件文件需要支持读取位置的持久缓存")
	}
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	if len(messages) > 256 || uint64(len(messages)) > MaxSequence-e.seq {
		return fmt.Errorf("任务事件批次不能超过 256 条或耗尽连续序号")
	}
	owned := make([]Message, len(messages))
	for index, message := range messages {
		message = message.clone()
		message.CollectorID, message.Seq = e.id, e.seq+uint64(index)+1
		if message.TS == 0 {
			message.TS = e.now().UTC().UnixMilli()
		}
		if err := message.Validate(); err != nil {
			return err
		}
		owned[index] = message
	}
	if err := sink.WriteJobFileCheckpoint(ctx, owned, checkpoint); err != nil {
		return fmt.Errorf("提交任务事件和读取位置失败：%w", err)
	}
	e.seq += uint64(len(owned))
	return nil
}
