package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type RuntimeConfig interface {
	CollectorConfig
	Snapshot() *config.Config
	Watch(context.Context, config.ChangeSource) error
}

type Persistence interface {
	pipeline.Sink
	rules.Repository
	notify.Recorder
	RegisterCollector(context.Context, store.CollectorRecord) error
	Collector(context.Context, string) (store.CollectorRecord, bool, error)
	PendingNotifications(context.Context, int64, int64, int64, int) ([]store.PendingNotification, error)
	Flush(context.Context) error
	Close() error
	Err() error
}

type RuntimeOptions struct {
	ID, Hostname, Version string
	Config                RuntimeConfig
	Watcher               config.ChangeSource
	Store                 Persistence
	Source                RuntimeSource
	Processes             collector.ProcessSource
	Ports                 collector.PortSource
	Probes                collector.ProbeExecutor
	Secrets               SecretReader
	NewClient             ClientFactory
	Now                   func() time.Time
	Clock                 collector.ProbeClock
	Waiter                notify.Waiter
	Logger                *slog.Logger
}

type RuntimeSource interface {
	pipeline.Source
	Close() error
}

type commitSource interface {
	Acknowledge(pipeline.Message, error)
}

type Runtime struct{ options RuntimeOptions }

type runtimeError struct {
	message string
	cause   error
}

func (e *runtimeError) Error() string { return e.message }
func (e *runtimeError) Unwrap() error { return e.cause }

func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if options.ID == "" || options.Config == nil || options.Store == nil || options.Secrets == nil || options.NewClient == nil || (options.Source == nil && (options.Processes == nil || options.Ports == nil || options.Probes == nil)) {
		return nil, fmt.Errorf("运行监控需要采集身份、配置、存储、采集源、密钥库和通知客户端工厂")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Clock == nil {
		options.Clock = collector.NewProbeClock()
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Runtime{options: options}, nil
}

func (r *Runtime) Run(parent context.Context) (runErr error) {
	opts := r.options
	defer func() {
		if err := opts.Store.Close(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	if parent.Err() != nil {
		return nil
	}
	workCtx, stopWork := context.WithCancel(parent)
	defer stopWork()
	ioCtx, stopIO := context.WithCancel(context.WithoutCancel(parent))
	defer stopIO()
	updates := opts.Config.Subscribe(workCtx)
	var initial *config.Config
	select {
	case <-workCtx.Done():
		return nil
	case cfg, ok := <-updates:
		if !ok || cfg == nil {
			return fmt.Errorf("运行配置未提供初始快照")
		}
		initial = cfg
	}
	at := opts.Now().UnixMilli()
	if opts.Source == nil {
		if err := opts.Store.RegisterCollector(ioCtx, store.CollectorRecord{ID: opts.ID, Hostname: opts.Hostname, Version: opts.Version, FirstSeen: at, LastSeen: at}); err != nil {
			return err
		}
	}
	renderer, err := cards.New(opts.Config, cards.Options{Now: opts.Now, Logger: opts.Logger})
	if err != nil {
		return err
	}
	queue := &notificationQueue{
		active: initial, secrets: opts.Secrets, factory: opts.NewClient, store: opts.Store, renderer: renderer,
		host: cards.Host{Name: opts.Hostname, CollectorID: opts.ID}, now: opts.Now, logger: opts.Logger, waiter: opts.Waiter,
		jobs: make(chan notificationJob, 256), targets: make(map[targetKey]pipeline.TargetStatus),
		probes: make(map[targetKey]map[int]pipeline.ProbeResult), clients: make(map[string]cachedClient),
	}
	engine, err := rules.New(ioCtx, rules.Options{Config: initial, Repository: opts.Store, Notifications: queue, Now: opts.Now, Clock: opts.Clock, Logger: opts.Logger})
	if err != nil {
		return err
	}
	source := opts.Source
	var local *Collector
	if source == nil {
		channel := pipeline.NewChannel(256)
		source = channel
		local, err = NewCollector(CollectorOptions{ID: opts.ID, Config: opts.Config, Processes: opts.Processes, Ports: opts.Ports, Probes: opts.Probes, Sink: channel, Now: opts.Now, Clock: opts.Clock, Logger: opts.Logger})
		if err != nil {
			return err
		}
	}
	defer source.Close()
	var firstErr error
	var failureMu sync.Mutex
	fail := func(err error, interruptIO bool) {
		if err == nil {
			return
		}
		failureMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		failureMu.Unlock()
		stopWork()
		if interruptIO {
			stopIO()
		}
	}
	collectorDone, readerDone, notificationsDone, watcherDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(notificationsDone)
		fail(queue.run(ioCtx), true)
	}()
	if err := queue.replay(workCtx); err != nil {
		close(queue.jobs)
		<-notificationsDone
		if parent.Err() != nil {
			return nil
		}
		return fmt.Errorf("启动补发通知失败：%w", err)
	}
	go func() {
		defer close(collectorDone)
		if local != nil {
			fail(local.Run(workCtx), false)
		} else {
			<-workCtx.Done()
		}
		source.Close()
		stopWork()
	}()
	messages := make(chan pipeline.Message, 1)
	go func() {
		defer close(readerDone)
		defer close(messages)
		for {
			message, err := source.Read(ioCtx)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				fail(err, true)
				return
			}
			select {
			case messages <- message:
			case <-ioCtx.Done():
				return
			}
		}
	}()
	if opts.Watcher != nil {
		go func() {
			defer close(watcherDone)
			err := opts.Config.Watch(workCtx, opts.Watcher)
			if err == nil && workCtx.Err() == nil {
				err = fmt.Errorf("配置监听提前停止")
			}
			fail(err, false)
		}()
	} else {
		close(watcherDone)
	}
	ticker := opts.Clock.NewTicker(time.Second)
	defer ticker.Stop()
	ticks, stopping := ticker.Ticks(), workCtx.Done()
	loop := true
	for loop {
		select {
		case <-stopping:
			opts.Logger.Info("Stopping collectors; draining records and notifications")
			ticker.Stop()
			ticks, stopping, updates = nil, nil, nil
		case cfg, ok := <-updates:
			if !ok || cfg == nil {
				if workCtx.Err() == nil {
					fail(fmt.Errorf("运行配置订阅提前关闭"), true)
					loop = false
				}
				updates = nil
				continue
			}
			if err := engine.UpdateConfigContext(ioCtx, cfg); err != nil {
				opts.Logger.Warn("Rule retirement persistence failed; evaluation will retry")
			}
			queue.setConfig(cfg)
		case <-ticks:
			if err := engine.Evaluate(ioCtx); err != nil {
				opts.Logger.Warn("Rule evaluation failed; retrying next tick")
			}
		case message, ok := <-messages:
			if !ok {
				loop = false
				continue
			}
			if err := opts.Store.Write(ioCtx, message); err != nil {
				if remote, ok := source.(commitSource); ok {
					remote.Acknowledge(message, err)
				}
				fail(err, true)
				loop = false
				continue
			}
			queue.observe(message)
			if err := engine.Handle(ioCtx, message); err != nil {
				opts.Logger.Warn("Rule message evaluation failed; retrying next tick")
			}
			if remote, ok := source.(commitSource); ok {
				err := opts.Store.Flush(ioCtx)
				remote.Acknowledge(message, err)
				if err != nil {
					opts.Logger.Warn("Remote record commit failed; withholding acknowledgement")
				}
			}
		}
	}
	stopWork()
	<-collectorDone
	<-readerDone
	<-watcherDone
	close(queue.jobs)
	<-notificationsDone
	if err := opts.Store.Flush(context.WithoutCancel(parent)); err != nil {
		fail(err, false)
	}
	failureMu.Lock()
	runErr = firstErr
	failureMu.Unlock()
	return runErr
}
