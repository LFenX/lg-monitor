package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/jobs"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

const (
	// jobFileInterval is how often a job's file is checked for new lines.
	jobFileInterval = 5 * time.Second
	// jobFileChunk bounds one read; a line longer than this cannot be read at all.
	jobFileChunk = 1 << 20
	// jobFileLine is the longest line looked at; events are far smaller.
	jobFileLine = 64 << 10
	// jobFileBatch keeps one outbox write well under its 1 MiB limit.
	jobFileBatch = 512 << 10
	// jobFileLineErrors caps per-line complaints in one pass; the rest are counted in one notice.
	jobFileLineErrors = 20
	// jobFileUnterminated is how long an unfinished last line may sit before the job is told about it.
	jobFileUnterminated = time.Minute
)

// jobTail remembers, in memory only, an unfinished last line and whether it was already reported.
type jobTail struct {
	size     int64
	since    time.Time
	reported bool
}

// runJobFiles follows the JSON Lines files of batch jobs assigned to this collector. Each line is checked and
// redacted here, then queued with the position after it, so a restart or a lost connection resumes exactly
// where it stopped. Reading never touches the job: a failed report cannot make it upload again.
func (c *Collector) runJobFiles(ctx context.Context, emitter *pipeline.Emitter) error {
	opts := c.options
	sink, ok := opts.Sink.(pipeline.JobFileCheckpointSink)
	if !ok {
		return fmt.Errorf("任务事件文件需要支持读取位置的持久缓存")
	}
	updates := opts.Config.Subscribe(ctx)
	ticker := opts.Clock.NewTicker(jobFileInterval)
	defer ticker.Stop()
	var active *config.Config
	states := map[string]pipeline.JobFileCheckpoint{}
	tails := map[string]jobTail{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case cfg, ok := <-updates:
			if !ok {
				return nil
			}
			active = cfg
		case <-ticker.Ticks():
		}
		if active == nil {
			continue
		}
		for _, target := range active.Targets {
			if target.Job == nil || target.Job.Source.Kind != "file" {
				continue
			}
			if err := c.readJobFile(ctx, emitter, sink, states, tails, target.ID, target.Job.Source.Path); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func jobNotice(jobID, path string, line uint64, text string) pipeline.Message {
	if utf8.RuneCountInString(text) > 500 {
		text = string([]rune(text)[:499]) + "…"
	}
	return pipeline.Message{Kind: pipeline.KindJobEvent, JobEvent: &collector.JobEventRecord{JobID: jobID, Path: path, Line: line, Error: text}}
}

func headHash(head []byte) string {
	sum := sha256.Sum256(head)
	return hex.EncodeToString(sum[:])
}

// replacedJobFile tells a new file from the old one grown longer: it is shorter than what was read, or starts
// differently.
func replacedJobFile(state pipeline.JobFileCheckpoint, read collector.JobFileRead) bool {
	if read.Size < state.Offset {
		return true
	}
	return state.HeadLen > 0 && (len(read.Head) < state.HeadLen || headHash(read.Head[:state.HeadLen]) != state.HeadHash)
}

// jobLine turns one line into an event message, or a message saying why the line was not used.
func jobLine(jobID, path string, number uint64, line []byte) (pipeline.Message, bool) {
	where := "第 " + strconv.FormatUint(number, 10) + " 行："
	if len(line) > jobFileLine {
		return jobNotice(jobID, path, number, where+"超过 64 KiB，已跳过"), false
	}
	event, err := jobs.Parse(line)
	if err == nil && event.JobID != "" && event.JobID != jobID {
		err = fmt.Errorf("job_id %s 与任务 %s 不一致", event.JobID, jobID)
	}
	if err != nil {
		return jobNotice(jobID, path, number, where+err.Error()), false
	}
	event.JobID = ""
	body, err := json.Marshal(event)
	if err != nil || len(body) > collector.JobEventMaxBytes {
		return jobNotice(jobID, path, number, where+"事件超过 16 KiB，已跳过"), false
	}
	return pipeline.Message{Kind: pipeline.KindJobEvent, JobEvent: &collector.JobEventRecord{JobID: jobID, Path: path, Line: number, Event: body}}, true
}

func (c *Collector) readJobFile(ctx context.Context, emitter *pipeline.Emitter, sink pipeline.JobFileCheckpointSink, states map[string]pipeline.JobFileCheckpoint, tails map[string]jobTail, jobID, path string) error {
	state, known := states[jobID]
	if !known {
		saved, exists, err := sink.JobFileCheckpointState(ctx, jobID)
		if err != nil {
			return err
		}
		state = saved
		if !exists {
			state = pipeline.JobFileCheckpoint{JobID: jobID, Path: path}
		}
	}
	if state.Path != path {
		// A different path is a different file: read it from its start.
		state = pipeline.JobFileCheckpoint{JobID: jobID, Path: path, Failing: state.Failing}
	}
	var messages []pipeline.Message
	read, err := c.options.JobFiles.ReadJobFile(ctx, path, state.Offset, jobFileChunk)
	if err == nil && replacedJobFile(state, read) {
		// Lines already sent come again from the new file; the monitor drops them by event_id.
		state.Offset, state.Line, state.HeadLen, state.HeadHash = 0, 0, 0, ""
		read, err = c.options.JobFiles.ReadJobFile(ctx, path, 0, jobFileChunk)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !state.Failing {
			state.Failing = true
			if err := emitter.EmitJobFileCheckpoint(ctx, []pipeline.Message{jobNotice(jobID, path, 0, "读取任务事件文件失败："+err.Error())}, state); err != nil {
				return err
			}
		}
		states[jobID] = state
		return nil
	}
	if len(read.Head) > state.HeadLen {
		state.HeadLen, state.HeadHash = len(read.Head), headHash(read.Head)
	}
	start, data := state.Offset, read.Data
	consumed, batchBytes, lineErrors, skipped := 0, 0, 0, 0
	recovered := state.Failing
	flush := func(force bool) error {
		if len(messages) == 0 && !force {
			return nil
		}
		if err := emitter.EmitJobFileCheckpoint(ctx, messages, state); err != nil {
			return err
		}
		messages, batchBytes = nil, 0
		return nil
	}
	for {
		index := bytes.IndexByte(data[consumed:], '\n')
		if index < 0 {
			break
		}
		line := bytes.TrimSpace(data[consumed : consumed+index])
		consumed += index + 1
		state.Line++
		state.Offset = start + int64(consumed)
		if len(line) == 0 {
			continue
		}
		message, ok := jobLine(jobID, path, state.Line, line)
		if !ok {
			lineErrors++
			if lineErrors > jobFileLineErrors {
				skipped++
				continue
			}
		}
		messages = append(messages, message)
		batchBytes += len(line)
		if len(messages) >= 200 || batchBytes >= jobFileBatch {
			if err := flush(false); err != nil {
				return err
			}
		}
	}
	if skipped > 0 {
		messages = append(messages, jobNotice(jobID, path, state.Line, fmt.Sprintf("另有 %d 行不是合法事件，已跳过", skipped)))
	}
	rest := len(data) - consumed
	tail := tails[jobID]
	switch {
	case rest == jobFileChunk:
		// No line end in a whole chunk: the file is not JSON Lines. Wait for it to be fixed or replaced.
		if !state.Failing {
			state.Failing = true
			// Reported against the file, not the line: nothing after it can be read until it changes.
			messages = append(messages, jobNotice(jobID, path, 0, fmt.Sprintf("第 %d 行超过 1 MiB 还没有换行，后面的内容无法读取；请检查这个文件是否是 JSON Lines", state.Line+1)))
		}
		recovered = false
	case rest > 0 && len(bytes.TrimSpace(data[consumed:])) > 0:
		// The job may still be writing this line; complain only when it has stopped changing for a while.
		now := c.options.Now()
		if tail.size != read.Size {
			tail = jobTail{size: read.Size, since: now}
		} else if !tail.reported && now.Sub(tail.since) >= jobFileUnterminated {
			tail.reported = true
			messages = append(messages, jobNotice(jobID, path, state.Line+1, fmt.Sprintf("第 %d 行没有以换行结束；每条事件写完要加换行，才会被读取", state.Line+1)))
		}
		tails[jobID] = tail
	default:
		delete(tails, jobID)
	}
	if recovered {
		state.Failing = false
		messages = append([]pipeline.Message{jobNotice(jobID, path, 0, "")}, messages...)
	}
	previous, wasKnown := states[jobID]
	if err := flush(!wasKnown || previous != state); err != nil {
		return err
	}
	states[jobID] = state
	return nil
}
