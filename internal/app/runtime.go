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
	"github.com/LFenX/lg-monitor/internal/feishu"
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

type retentionStore interface {
	SetRetention(config.Retention) error
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
	Ingest(context.Context, pipeline.Message) (pipeline.Message, bool, error)
	PendingCollectors(context.Context) ([]string, error)
	PendingIngress(context.Context, string) (pipeline.Message, bool, error)
	CompleteIngress(context.Context, string, uint64) error
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
	NewWebhook            WebhookFactory
	NewJSONWebhook        JSONWebhookFactory
	Now                   func() time.Time
	Clock                 collector.ProbeClock
	Waiter                notify.Waiter
	Toast                 notify.ToastClient
	Logger                *slog.Logger
	Collectors            rules.CollectorSource
	Ready                 func()
	Queries               rules.QueryExecutor
	Observer              RuntimeObserver
}

type RuntimeObserver interface {
	rules.TransitionObserver
	Sample(pipeline.Message)
}

type RuntimeSource interface {
	pipeline.Source
	Close() error
}

type commitSource interface {
	Acknowledge(pipeline.Message, error)
}

type batchCommitSource interface {
	pipeline.BatchSource
	AcknowledgeBatch([]pipeline.Message, error)
}

type batchPersistence interface {
	IngestBatch(context.Context, []pipeline.Message) (store.IngressBatch, error)
	PendingIngressBatch(context.Context, string) (store.IngressBatch, error)
	CompleteIngressBatch(context.Context, string, uint64) error
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
	if options.Toast == nil {
		options.Toast = notify.NewToastClient(notify.ToastOptions{Logger: options.Logger})
	}
	if options.NewWebhook == nil {
		now := options.Now
		options.NewWebhook = func(endpoint, secret []byte) (feishu.WebhookClient, error) {
			return feishu.NewWebhookClient(endpoint, secret, feishu.WebhookOptions{Now: now})
		}
	}
	if options.NewJSONWebhook == nil {
		now, version := options.Now, options.Version
		options.NewJSONWebhook = func(endpoint, secret []byte) (notify.JSONWebhookClient, error) {
			return notify.NewJSONWebhookClient(endpoint, secret, notify.JSONWebhookOptions{Now: now, Version: version})
		}
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
	if policy, ok := opts.Store.(retentionStore); ok {
		if err := policy.SetRetention(initial.Settings.Retention); err != nil {
			return err
		}
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
		active: initial, secrets: opts.Secrets, factory: opts.NewClient, webhookFactory: opts.NewWebhook, jsonWebhookFactory: opts.NewJSONWebhook, store: opts.Store, renderer: renderer,
		host: cards.Host{Name: opts.Hostname, CollectorID: opts.ID}, now: opts.Now, logger: opts.Logger, waiter: opts.Waiter,
		jobs: make(chan notificationJob, 256), targets: make(map[targetKey]pipeline.TargetStatus),
		probes: make(map[targetKey]map[int]pipeline.ProbeResult), clients: make(map[string]cachedClient),
		observer: opts.Observer,
		toast:    opts.Toast,
	}
	engine, err := rules.New(ioCtx, rules.Options{Config: initial, Repository: opts.Store, Notifications: queue, Now: opts.Now, Clock: opts.Clock, Logger: opts.Logger, Collectors: opts.Collectors, Queries: opts.Queries, Observer: opts.Observer,
		CollectorHostname: func(ctx context.Context, id string) (string, error) {
			identity, _, err := opts.Store.Collector(ctx, id)
			return identity.Hostname, err
		}})
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
	applyPending := func(id string) error {
		if storage, ok := opts.Store.(batchPersistence); ok {
			for {
				pending, err := storage.PendingIngressBatch(ioCtx, id)
				if err != nil || pending.Through == 0 {
					return err
				}
				for _, message := range pending.Messages {
					queue.observe(message)
					if err := engine.Handle(ioCtx, message); err != nil {
						return err
					}
				}
				if err := storage.CompleteIngressBatch(ioCtx, id, pending.Through); err != nil {
					return err
				}
			}
		}
		message, exists, err := opts.Store.PendingIngress(ioCtx, id)
		if err != nil || !exists {
			return err
		}
		queue.observe(message)
		if err := engine.Handle(ioCtx, message); err != nil {
			return err
		}
		return opts.Store.CompleteIngress(ioCtx, message.CollectorID, message.Seq)
	}
	retryPending := func() {
		if _, remote := source.(commitSource); !remote {
			return
		}
		ids, err := opts.Store.PendingCollectors(ioCtx)
		if err != nil {
			opts.Logger.Warn("Could not list durable inbox; retrying next tick")
			return
		}
		for _, id := range ids {
			if err := applyPending(id); err != nil {
				opts.Logger.Warn("Durable inbox application failed; retaining message for retry", "collector", id)
			}
		}
	}
	retryPending()
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
	messages := make(chan []pipeline.Message, 1)
	go func() {
		defer close(readerDone)
		defer close(messages)
		for {
			var packet []pipeline.Message
			var err error
			if batched, ok := source.(batchCommitSource); ok {
				packet, err = batched.ReadBatch(ioCtx)
			} else {
				var message pipeline.Message
				message, err = source.Read(ioCtx)
				packet = []pipeline.Message{message}
			}
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				fail(err, true)
				return
			}
			select {
			case messages <- packet:
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
	if opts.Ready != nil {
		opts.Ready()
	}
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
			if policy, ok := opts.Store.(retentionStore); ok {
				if err := policy.SetRetention(cfg.Settings.Retention); err != nil {
					opts.Logger.Warn("Retention update failed; keeping previous policy", "error", err)
				}
			}
		case <-ticks:
			if err := engine.Evaluate(ioCtx); err != nil {
				opts.Logger.Warn("Rule evaluation failed; retrying next tick")
			}
			retryPending()
		case packet, ok := <-messages:
			if !ok {
				loop = false
				continue
			}
			if len(packet) == 0 {
				fail(fmt.Errorf("采集源返回了空批次"), true)
				continue
			}
			if remote, ok := source.(batchCommitSource); ok {
				storage, supported := opts.Store.(batchPersistence)
				if !supported {
					fail(fmt.Errorf("批次采集源需要批次持久化接口"), true)
					continue
				}
				id := packet[0].CollectorID
				err := applyPending(id)
				if err == nil {
					var accepted store.IngressBatch
					accepted, err = storage.IngestBatch(ioCtx, packet)
					if err == nil {
						for _, message := range accepted.Messages {
							queue.observe(message)
							if err = engine.Handle(ioCtx, message); err != nil {
								break
							}
						}
						if err == nil {
							err = storage.CompleteIngressBatch(ioCtx, id, accepted.Through)
						}
					}
				}
				remote.AcknowledgeBatch(packet, err)
				if err != nil {
					opts.Logger.Warn("Durable ingress batch not fully applied; withholding acknowledgement")
				}
				continue
			}
			message := packet[0]
			if remote, ok := source.(commitSource); ok {
				err := applyPending(message.CollectorID)
				if err == nil {
					canonical, needed, ingestErr := opts.Store.Ingest(ioCtx, message)
					err = ingestErr
					if err == nil && needed {
						queue.observe(canonical)
						err = engine.Handle(ioCtx, canonical)
						if err == nil {
							err = opts.Store.CompleteIngress(ioCtx, canonical.CollectorID, canonical.Seq)
						}
					}
				}
				remote.Acknowledge(message, err)
				if err != nil {
					opts.Logger.Warn("Durable ingress not fully applied; withholding acknowledgement")
				}
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
