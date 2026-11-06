package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

type ProbeTicker interface {
	Ticks() <-chan time.Time
	Stop()
}

type ProbeClock interface {
	NewTicker(time.Duration) ProbeTicker
}

type systemProbeClock struct{}
type systemProbeTicker struct{ ticker *time.Ticker }

func (systemProbeClock) NewTicker(interval time.Duration) ProbeTicker {
	return systemProbeTicker{ticker: time.NewTicker(interval)}
}
func (t systemProbeTicker) Ticks() <-chan time.Time { return t.ticker.C }
func (t systemProbeTicker) Stop()                   { t.ticker.Stop() }

func RunProbes(ctx context.Context, targets []config.Target, executor ProbeExecutor, sink ProbeSink, clock ProbeClock) error {
	if executor == nil || sink == nil {
		return fmt.Errorf("探测执行器和结果流不能为空")
	}
	type task struct {
		target   string
		index    int
		probe    config.Probe
		interval time.Duration
		timeout  time.Duration
	}
	var tasks []task
	for _, target := range targets {
		for index, probe := range target.Probes {
			interval, intervalErr := probe.Interval.Parse()
			timeout, timeoutErr := probe.Timeout.Parse()
			if intervalErr != nil || timeoutErr != nil || interval <= 0 || timeout <= 0 {
				return fmt.Errorf("目标 %s 的第 %d 个探测需要正数 interval 和 timeout", target.ID, index)
			}
			tasks = append(tasks, task{target: target.ID, index: index, probe: probe, interval: interval, timeout: timeout})
		}
	}
	if clock == nil {
		clock = systemProbeClock{}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errors := make(chan error, 1)
	var workers sync.WaitGroup
	for _, task := range tasks {
		ticker := clock.NewTicker(task.interval)
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				// One worker per probe serializes its invocations without delaying other probes.
				probeCtx, probeCancel := context.WithTimeout(ctx, task.timeout)
				result := executor.Execute(probeCtx, task.target, task.index, task.probe)
				probeCancel()
				if err := sink.WriteProbe(ctx, result); err != nil {
					if ctx.Err() == nil {
						select {
						case errors <- fmt.Errorf("写入目标 %s 的探测结果失败：%w", task.target, err):
						default:
						}
						cancel()
					}
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.Ticks():
				}
			}
		}()
	}
	workers.Wait()
	select {
	case err := <-errors:
		return err
	default:
		return nil
	}
}
