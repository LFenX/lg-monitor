package pipeline

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type Channel struct {
	messages chan Message
	done     chan struct{}
	mu       sync.RWMutex
	once     sync.Once
}

func NewChannel(capacity int) *Channel {
	if capacity < 1 {
		capacity = 256
	}
	return &Channel{messages: make(chan Message, capacity), done: make(chan struct{})}
}

func (c *Channel) Write(ctx context.Context, message Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("写入采集通道已取消：%w", err)
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
		return fmt.Errorf("写入采集通道已取消：%w", ctx.Err())
	case <-c.done:
		return fmt.Errorf("采集通道已关闭：%w", io.ErrClosedPipe)
	case c.messages <- message.clone():
		return nil
	}
}

func (c *Channel) Read(ctx context.Context) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, fmt.Errorf("读取采集通道已取消：%w", err)
	}
	select {
	case <-ctx.Done():
		return Message{}, fmt.Errorf("读取采集通道已取消：%w", ctx.Err())
	case message, ok := <-c.messages:
		if !ok {
			return Message{}, io.EOF
		}
		return message, nil
	}
}

func (c *Channel) Close() error {
	c.once.Do(func() {
		// Wake blocked writers before waiting for their read locks, then allow readers to drain.
		close(c.done)
		c.mu.Lock()
		close(c.messages)
		c.mu.Unlock()
	})
	return nil
}
