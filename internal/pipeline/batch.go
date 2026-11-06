package pipeline

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type BatchSource interface {
	ReadBatch(context.Context) ([]Message, error)
}

type BatchSink interface {
	WriteBatch(context.Context, []Message) error
}

type BatchChannel struct {
	items chan []Message
	done  chan struct{}
	mu    sync.RWMutex
	once  sync.Once
}

func NewBatchChannel(capacity int) *BatchChannel {
	if capacity < 1 {
		capacity = 1
	}
	return &BatchChannel{items: make(chan []Message, capacity), done: make(chan struct{})}
}

func (c *BatchChannel) WriteBatch(ctx context.Context, messages []Message) error {
	if len(messages) == 0 || len(messages) > 256 {
		return fmt.Errorf("采集批次须包含 1～256 条记录")
	}
	owned := make([]Message, len(messages))
	for i, message := range messages {
		if err := message.Validate(); err != nil {
			return err
		}
		owned[i] = message.clone()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("写入采集批次已取消：%w", err)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	select {
	case <-c.done:
		return fmt.Errorf("采集通道已关闭：%w", io.ErrClosedPipe)
	default:
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("写入采集批次已取消：%w", ctx.Err())
	case <-c.done:
		return fmt.Errorf("采集通道已关闭：%w", io.ErrClosedPipe)
	case c.items <- owned:
		return nil
	}
}

func (c *BatchChannel) ReadBatch(ctx context.Context) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取采集批次已取消：%w", err)
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("读取采集批次已取消：%w", ctx.Err())
	case messages, ok := <-c.items:
		if !ok {
			return nil, io.EOF
		}
		return messages, nil
	}
}

func (c *BatchChannel) Close() error {
	c.once.Do(func() {
		close(c.done)
		c.mu.Lock()
		close(c.items)
		c.mu.Unlock()
	})
	return nil
}
