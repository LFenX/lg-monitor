package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type collectorMonitorLink struct {
	Name            string
	Identity        *transport.ClientIdentity
	AllowOperations bool
}

type disabledMonitorOperations struct{}

func sameCollectorMonitors(a, b []config.CollectorMonitor) bool {
	if len(a) != len(b) {
		return false
	}
	before := make(map[string]config.CollectorMonitor, len(a))
	for _, monitor := range a {
		before[monitor.ID] = monitor
	}
	for _, monitor := range b {
		old, ok := before[monitor.ID]
		if !ok || old.IsEnabled() != monitor.IsEnabled() || old.AllowOperations != monitor.AllowOperations {
			return false
		}
	}
	return true
}

func (disabledMonitorOperations) ReadOperationPolicy(context.Context) (config.RemoteOperations, error) {
	return config.RemoteOperations{}, nil
}

func transportWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func superviseCollectorMonitors(ctx context.Context, links []collectorMonitorLink, logger *slog.Logger, run func(context.Context, collectorMonitorLink) error, wait func(context.Context, time.Duration) error) error {
	var group sync.WaitGroup
	for _, link := range links {
		group.Go(func() {
			for ctx.Err() == nil {
				err := run(ctx, link)
				if ctx.Err() != nil {
					return
				}
				logger.Error("Collector pipeline stopped; retrying independently", "monitor", link.Name, "error", err)
				if wait(ctx, 10*time.Second) != nil {
					return
				}
			}
		})
	}
	group.Wait()
	return nil
}
