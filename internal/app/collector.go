package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/collector/match"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type CollectorConfig interface {
	Subscribe(context.Context) <-chan *config.Config
}

type CollectorOptions struct {
	ID               string
	Config           CollectorConfig
	Processes        collector.ProcessSource
	Ports            collector.PortSource
	Probes           collector.ProbeExecutor
	Sink             pipeline.Sink
	Now              func() time.Time
	Clock            collector.ProbeClock
	Interval         time.Duration
	SnapshotInterval time.Duration
	Logger           *slog.Logger
}

type Collector struct{ options CollectorOptions }

func NewCollector(options CollectorOptions) (*Collector, error) {
	if options.Config == nil || options.Processes == nil || options.Ports == nil || options.Probes == nil || options.Sink == nil || options.ID == "" {
		return nil, fmt.Errorf("采集器需要身份、配置源、进程源、端口源、探测器和消息接收端")
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
	if options.Interval == 0 {
		options.Interval = 2 * time.Second
	}
	if options.SnapshotInterval == 0 {
		options.SnapshotInterval = 10 * time.Second
	}
	if options.Interval < 0 || options.SnapshotInterval < options.Interval {
		return nil, fmt.Errorf("采集间隔必须为正数，全量快照间隔不能小于采集间隔")
	}
	return &Collector{options: options}, nil
}

type probeForwarder struct{ emitter *pipeline.Emitter }

func (f probeForwarder) WriteProbe(ctx context.Context, result collector.ProbeResult) error {
	return f.emitter.Emit(ctx, pipeline.Message{Kind: pipeline.KindProbeResult, TS: result.TS, ProbeResult: &result})
}

func (c *Collector) Run(parent context.Context) (err error) {
	if parent.Err() != nil {
		return nil
	}
	options := c.options
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	emitter, err := pipeline.NewEmitter(options.ID, options.Sink, options.Now)
	if err != nil {
		return err
	}
	updates := options.Config.Subscribe(ctx)
	var active *config.Config
	select {
	case <-ctx.Done():
		return nil
	case initial, ok := <-updates:
		if ctx.Err() != nil {
			return nil
		}
		if !ok || initial == nil {
			return fmt.Errorf("配置订阅未提供初始快照")
		}
		active = initial
	}
	matcher, err := match.New(active.Targets)
	if err != nil {
		return fmt.Errorf("创建监控目标匹配器失败：%w", err)
	}
	ticker := options.Clock.NewTicker(options.Interval)
	defer ticker.Stop()
	last := make(map[string]match.Result)
	var lastFull time.Time
	consecutiveFailures := 0
	sourceFailed := false
	collect := func(force bool) error {
		sourceFailed = false
		processes, err := options.Processes.Snapshot(ctx)
		if err != nil {
			sourceFailed = true
			return fmt.Errorf("采集进程失败：%w", err)
		}
		ports, err := options.Ports.ListeningTCP(ctx)
		if err != nil {
			sourceFailed = true
			return fmt.Errorf("采集端口失败：%w", err)
		}
		at := options.Now()
		full := force || lastFull.IsZero() || at.Sub(lastFull) >= options.SnapshotInterval
		results := matcher.Match(processes, ports)
		next := make(map[string]match.Result, len(results))
		for _, result := range results {
			previous, exists := last[result.TargetID]
			if full || !exists || !sameTargetStatus(previous, result) {
				refs := make([]pipeline.ProcessRef, 0, len(result.Processes))
				for _, process := range result.Processes {
					refs = append(refs, pipeline.Ref(process))
				}
				status := &pipeline.TargetStatus{TargetID: result.TargetID, State: result.State, Process: result.Process, Port: result.Port, Reason: result.Reason, Processes: refs, Ports: result.Ports, Full: full}
				if err := emitter.Emit(ctx, pipeline.Message{Kind: pipeline.KindTargetStatus, TS: at.UTC().UnixMilli(), TargetStatus: status}); err != nil {
					return err
				}
			}
			for _, process := range result.Processes {
				sample := &pipeline.ProcessSample{TargetID: result.TargetID, PID: process.PID, CreatedAt: process.CreatedAt, CPUPercent: process.CPUPercent, RSS: process.RSS}
				if err := emitter.Emit(ctx, pipeline.Message{Kind: pipeline.KindProcessSample, TS: at.UTC().UnixMilli(), ProcessSample: sample}); err != nil {
					return err
				}
			}
			next[result.TargetID] = result
		}
		last = next
		if full {
			lastFull = at
		}
		return nil
	}
	runCollect := func(force bool) error {
		failure := collect(force)
		if failure == nil {
			consecutiveFailures = 0
			return nil
		}
		if ctx.Err() != nil || (!sourceFailed && errors.Is(failure, io.ErrClosedPipe)) {
			return failure
		}
		consecutiveFailures++
		options.Logger.Warn("Collection cycle failed; retrying next cycle", "consecutiveFailures", consecutiveFailures)
		return nil
	}
	var probeDone chan error
	var cancelProbes context.CancelFunc
	stopProbes := func() error {
		if cancelProbes != nil {
			cancelProbes()
			cancelProbes = nil
		}
		if probeDone != nil {
			failure := <-probeDone
			probeDone = nil
			return failure
		}
		return nil
	}
	startProbes := func() {
		probeCtx, stop := context.WithCancel(ctx)
		cancelProbes = stop
		probeDone = make(chan error, 1)
		done, targets := probeDone, slices.Clone(active.Targets)
		go func() {
			failure := collector.RunProbes(probeCtx, targets, options.Probes, probeForwarder{emitter}, options.Clock)
			done <- failure
			if errors.Is(failure, io.ErrClosedPipe) {
				cancel()
			}
		}()
	}
	defer func() {
		cancel()
		if failure := stopProbes(); failure != nil && (err == nil || errors.Is(err, context.Canceled)) {
			err = failure
		}
		if parent.Err() != nil {
			err = nil
		}
	}()
	if err := runCollect(true); err != nil {
		return err
	}
	startProbes()
	for {
		select {
		case <-ctx.Done():
			return nil
		case failure := <-probeDone:
			probeDone = nil
			if failure != nil {
				if errors.Is(failure, io.ErrClosedPipe) {
					return fmt.Errorf("探测消息接收端已关闭：%w", failure)
				}
				options.Logger.Warn("Probe forwarding failed; restarting next cycle")
			}
		case updated, ok := <-updates:
			if !ok {
				updates = nil
				continue
			}
			if updated == nil {
				return fmt.Errorf("配置订阅返回空快照")
			}
			if reflect.DeepEqual(active.Targets, updated.Targets) {
				continue
			}
			nextMatcher, err := match.New(updated.Targets)
			if err != nil {
				return fmt.Errorf("更新监控目标匹配器失败：%w", err)
			}
			if err := stopProbes(); err != nil {
				if ctx.Err() != nil || errors.Is(err, io.ErrClosedPipe) {
					return fmt.Errorf("停止旧探测调度失败：%w", err)
				}
				options.Logger.Warn("Previous probe forwarding failed; applying updated configuration")
			}
			active, matcher = updated, nextMatcher
			if err := runCollect(true); err != nil {
				return err
			}
			startProbes()
		case <-ticker.Ticks():
			if probeDone == nil {
				startProbes()
			}
			if err := runCollect(false); err != nil {
				return err
			}
		}
	}
}

func sameTargetStatus(a, b match.Result) bool {
	if a.State != b.State || a.Process != b.Process || a.Port != b.Port || a.Reason != b.Reason || len(a.Processes) != len(b.Processes) || !slices.Equal(a.Ports, b.Ports) {
		return false
	}
	for i := range a.Processes {
		if a.Processes[i].Identity() != b.Processes[i].Identity() {
			return false
		}
	}
	return true
}
