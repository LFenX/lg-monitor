package rules

import (
	"context"
	"fmt"
	"time"
)

const CollectorTargetID = "@collector"

type CollectorPresence struct {
	ID, Hostname string
	LastSeen     int64
	Connected    bool
}

type CollectorSource interface {
	Collectors(context.Context) ([]CollectorPresence, error)
}

func (e *Engine) refreshPresence(ctx context.Context) error {
	if e.collectorSource == nil {
		return nil
	}
	items, err := e.collectorSource.Collectors(ctx)
	if err != nil {
		return fmt.Errorf("查询配对采集器在线状态失败：%w", err)
	}
	next := make(map[string]CollectorPresence, len(items))
	for _, item := range items {
		if item.ID == "" || item.LastSeen <= 0 || next[item.ID].ID != "" {
			return fmt.Errorf("配对采集器在线状态无效或重复")
		}
		next[item.ID] = item
	}
	e.presence, e.presenceKnown = next, true
	for _, item := range items {
		e.collectors[item.ID] = true
		e.hostnames[item.ID] = item.Hostname
	}
	e.markRetired(e.specs)
	return nil
}

func (e *Engine) collectorCondition(spec ruleSpec, id string) (bool, bool, string) {
	item, exists := e.presence[id]
	if !exists {
		return false, false, ""
	}
	elapsed := e.now().Sub(time.UnixMilli(item.LastSeen))
	missing := !item.Connected || elapsed >= max(10*time.Second, spec.delay)
	return missing, true, "采集器失联，请检查采集器进程与网络"
}
