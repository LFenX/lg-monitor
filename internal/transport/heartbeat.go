package transport

import (
	"context"
	"fmt"
	"time"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

const HeartbeatInterval = 10 * time.Second

func (r *Receiver) SetHeartbeat(handler func(context.Context, Hello) error) { r.heartbeat = handler }

func (r *Receiver) Connected(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.active[id]
	return session != nil && session.connected
}

func (w *Writer) Heartbeat(ctx context.Context, at int64) error {
	if at <= 0 {
		return fmt.Errorf("心跳时间必须为 UTC 毫秒")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发送心跳已取消：%w", err)
	}
	stop := context.AfterFunc(ctx, w.cancel)
	defer stop()
	if err := w.send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Heartbeat{Heartbeat: &lgmonv1.Heartbeat{Ts: at}}}); err != nil {
		w.cancel()
		return fmt.Errorf("发送采集器心跳失败：%w", err)
	}
	return nil
}

func (d *Delivery) heartbeats(ctx context.Context, writer *Writer) error {
	ticker := d.options.Clock.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-writer.Done():
			return nil
		case <-ticker.Ticks():
			if err := writer.Heartbeat(ctx, d.options.Outbox.options.Now().UnixMilli()); err != nil {
				return err
			}
		}
	}
}

func (d *Delivery) session(parent context.Context, writer *Writer) (bool, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		err := d.heartbeats(ctx, writer)
		if err != nil {
			writer.cancel()
		}
		finished <- err
	}()
	progress, err := d.deliver(ctx, writer)
	cancel()
	writer.cancel()
	heartbeatErr := <-finished
	if err == nil {
		err = heartbeatErr
	}
	return progress, err
}
