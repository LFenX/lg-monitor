package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"google.golang.org/grpc/status"
)

type RetryWaiter interface {
	Wait(context.Context, time.Duration) error
}
type retryTimer struct{}

func (retryTimer) Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type DeliveryOptions struct {
	Outbox            *Outbox
	Identity          *ClientIdentity
	Hostname, Version string
	Connector         TLSConnector
	Waiter            RetryWaiter
	Clock             collector.ProbeClock
	Jitter            func(time.Duration) time.Duration
	MaxBackoff        time.Duration
	Logger            *slog.Logger
	Config            ConfigApplier
	Processes         ProcessResponder
}

type Delivery struct{ options DeliveryOptions }

func NewDelivery(options DeliveryOptions) (*Delivery, error) {
	if options.Outbox == nil || options.Identity == nil || options.Identity.ID != options.Outbox.options.CollectorID {
		return nil, fmt.Errorf("持久发送需要同一采集器的缓存和配对身份")
	}
	if options.Connector == nil {
		options.Connector = NativeConnector{}
	}
	if options.Waiter == nil {
		options.Waiter = retryTimer{}
	}
	if options.Clock == nil {
		options.Clock = collector.NewProbeClock()
	}
	if options.MaxBackoff == 0 {
		options.MaxBackoff = time.Minute
	}
	if options.MaxBackoff < time.Second || options.MaxBackoff > time.Minute {
		return nil, fmt.Errorf("重连退避上限须在 1～60 秒之间")
	}
	if options.Jitter == nil {
		options.Jitter = func(base time.Duration) time.Duration {
			return base + time.Duration(mathrand.Int64N(max(1, int64(base)/5)))
		}
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Delivery{options: options}, nil
}

func (d *Delivery) Run(ctx context.Context) error {
	opts := d.options
	base, attempt := time.Second, 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-opts.Outbox.Done():
			return fmt.Errorf("持久发送缓存已关闭：%w", io.ErrClosedPipe)
		default:
		}
		state, err := opts.Outbox.State(ctx)
		var progress bool
		if err == nil {
			err = opts.Outbox.Prune(ctx)
		}
		if err == nil {
			var connection RPCConnection
			connection, err = opts.Connector.Connect(ctx, opts.Identity.Monitor, opts.Identity.TLS())
			if err == nil {
				var writer *Writer
				writer, err = NewWriterWithProcesses(ctx, connection, Hello{CollectorID: opts.Identity.ID, Hostname: opts.Hostname, Version: opts.Version, LastAckedSeq: state.Acked, Capabilities: []string{MatchDirectoryCapability, HTTPChecksCapability, EventLogCapability}}, opts.Config, opts.Processes, opts.Outbox.options.Now)
				if err == nil {
					err = opts.Outbox.SyncResume(ctx, writer.ResumeFrom())
					connectedAt := opts.Outbox.options.Now()
					if err == nil {
						progress, err = d.session(ctx, writer)
					}
					if opts.Outbox.options.Now().Sub(connectedAt) >= 10*time.Second {
						progress = true
					}
					writer.Close()
				}
				connection.Close()
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrCacheDiverged) {
			return err
		}
		if progress {
			base, attempt = time.Second, 0
		}
		attempt++
		delay := min(opts.MaxBackoff, max(time.Second, opts.Jitter(base)))
		opts.Logger.Warn("Collector delivery interrupted; retaining outbox and retrying", "attempt", attempt, "delay", delay, "code", status.Code(err).String())
		if err := opts.Waiter.Wait(ctx, delay); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("等待采集重连失败：%w", err)
		}
		base = min(opts.MaxBackoff, base*2)
	}
}

func (d *Delivery) deliver(ctx context.Context, writer *Writer) (bool, error) {
	opts := d.options
	ticker := opts.Clock.NewTicker(time.Minute)
	defer ticker.Stop()
	progress := false
	for {
		messages, err := opts.Outbox.NextBatch(ctx)
		if err != nil {
			return progress, err
		}
		if len(messages) > 0 {
			if err := writer.WriteBatch(ctx, messages); err != nil {
				return progress, err
			}
			if err := opts.Outbox.Acknowledge(ctx, messages[len(messages)-1].Seq); err != nil {
				return progress, err
			}
			progress = true
			continue
		}
		select {
		case <-ctx.Done():
			return progress, ctx.Err()
		case <-opts.Outbox.Done():
			return progress, fmt.Errorf("发送缓存已关闭：%w", io.ErrClosedPipe)
		case <-writer.Done():
			return progress, writer.Err()
		case <-opts.Outbox.Changes():
		case <-ticker.Ticks():
		}
	}
}
