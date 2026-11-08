package pipeline

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/collector"
)

type EventLogCheckpoint struct {
	Channel  string
	Bookmark string
	Fences   map[string]EventLogFence
}

type EventLogFence struct {
	Signature string `json:"signature"`
	AfterID   uint64 `json:"afterId"`
}

func (c EventLogCheckpoint) Validate() error {
	if !collector.EventLogChannel(c.Channel) || c.Bookmark == "" || len(c.Bookmark) > 65536 || !utf8.ValidString(c.Bookmark) || strings.ContainsRune(c.Bookmark, 0) {
		return fmt.Errorf("事件日志书签需要允许的日志和不超过 64 KiB 的有效文本")
	}
	for id, fence := range c.Fences {
		signature, err := hex.DecodeString(fence.Signature)
		if id == "" || !utf8.ValidString(id) || strings.ContainsRune(id, 0) || err != nil || len(signature) != 32 || fence.AfterID > MaxSequence {
			return fmt.Errorf("事件日志订阅生效边界无效")
		}
	}
	return nil
}

type EventLogCheckpointSink interface {
	EventLogCheckpointState(context.Context, string) (EventLogCheckpoint, bool, error)
	WriteEventLogCheckpoint(context.Context, []Message, EventLogCheckpoint) error
}

func (e *Emitter) EmitEventLogCheckpoint(ctx context.Context, messages []Message, checkpoint EventLogCheckpoint) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("事件日志提交已取消：%w", err)
	}
	sink, ok := e.sink.(EventLogCheckpointSink)
	if !ok {
		return fmt.Errorf("事件日志需要支持原子书签的持久缓存")
	}
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	if len(messages) > 256 || uint64(len(messages)) > MaxSequence-e.seq {
		return fmt.Errorf("事件日志批次不能超过 256 条或耗尽连续序号")
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
	if err := sink.WriteEventLogCheckpoint(ctx, owned, checkpoint); err != nil {
		return fmt.Errorf("提交事件日志和书签失败：%w", err)
	}
	e.seq += uint64(len(owned))
	return nil
}
