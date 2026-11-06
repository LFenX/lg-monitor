package config

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
)

type ChangeSource interface {
	Changes() <-chan struct{}
	Errors() <-chan error
	Close() error
}

type Manager struct {
	loader      Loader
	logger      *slog.Logger
	reloadMu    sync.Mutex
	mu          sync.RWMutex
	active      *Config
	subscribers map[chan *Config]struct{}
}

func NewManager(loader Loader, logger *slog.Logger) (*Manager, error) {
	initial, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("加载初始配置失败：%w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{loader: loader, logger: logger, active: initial, subscribers: make(map[chan *Config]struct{})}, nil
}

func (m *Manager) Subscribe(ctx context.Context) <-chan *Config {
	updates := make(chan *Config, 1)
	m.mu.Lock()
	if ctx.Err() != nil {
		close(updates)
		m.mu.Unlock()
		return updates
	}
	m.subscribers[updates] = struct{}{}
	updates <- m.active.clone()
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		delete(m.subscribers, updates)
		close(updates)
		m.mu.Unlock()
	}()
	return updates
}

func (m *Manager) Snapshot() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active.clone()
}

func (m *Manager) Reload() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	updated, err := m.loader.Load()
	if err != nil {
		m.logger.Error("Configuration reload failed; keeping the previous snapshot", "error", err)
		return fmt.Errorf("重新加载配置失败，已保留旧配置：%w", err)
	}
	m.publish(updated)
	m.logger.Info("Configuration reloaded")
	return nil
}

func (m *Manager) Apply(updated *Config) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if err := Validate(updated); err != nil {
		return fmt.Errorf("应用配置失败，已保留旧配置：%w", err)
	}
	m.publish(updated.clone())
	return nil
}

func (m *Manager) publish(updated *Config) {
	m.mu.Lock()
	m.active = updated
	for subscriber := range m.subscribers {
		// Subscribers rebuild from a complete snapshot, so only the newest revision matters.
		select {
		case <-subscriber:
		default:
		}
		subscriber <- updated.clone()
	}
	m.mu.Unlock()
}

func (m *Manager) Watch(ctx context.Context, source ChangeSource) (err error) {
	defer func() {
		if closeErr := source.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("关闭配置监听失败：%w", closeErr)
		}
	}()
	changes, sourceErrors := source.Changes(), source.Errors()
	for changes != nil || sourceErrors != nil {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			// A rejected reload must not stop the watcher from accepting the next fix.
			_ = m.Reload()
		case failure, ok := <-sourceErrors:
			if !ok {
				sourceErrors = nil
				continue
			}
			m.logger.Error("Configuration watcher error; keeping the previous snapshot", "error", failure)
		}
	}
	return nil
}

func (c *Config) clone() *Config {
	copy := *c
	copy.Targets = slices.Clone(c.Targets)
	for i := range copy.Targets {
		target := &copy.Targets[i]
		target.Probes, target.Tags = slices.Clone(target.Probes), slices.Clone(target.Tags)
		for j := range target.Probes {
			target.Probes[j].Args = slices.Clone(target.Probes[j].Args)
		}
		if target.Match.Port != nil {
			port := *target.Match.Port
			target.Match.Port = &port
		}
	}
	copy.Rules = slices.Clone(c.Rules)
	for i := range copy.Rules {
		copy.Rules[i].Notify = slices.Clone(copy.Rules[i].Notify)
		if copy.Rules[i].Threshold != nil {
			value := *copy.Rules[i].Threshold
			copy.Rules[i].Threshold = &value
		}
		if copy.Rules[i].Probe != nil {
			value := *copy.Rules[i].Probe
			copy.Rules[i].Probe = &value
		}
	}
	copy.FeishuApps, copy.Notifiers = slices.Clone(c.FeishuApps), slices.Clone(c.Notifiers)
	copy.Templates = slices.Clone(c.Templates)
	for i := range copy.Templates {
		template := &copy.Templates[i]
		template.Colors = maps.Clone(template.Colors)
		template.Fields = slices.Clone(template.Fields)
		template.ExtraFields = slices.Clone(template.ExtraFields)
		template.Buttons = slices.Clone(template.Buttons)
		if template.Mention != nil {
			template.Mention = cloneJSON(template.Mention).(map[string]any)
		}
		template.Raw = cloneJSON(template.Raw)
	}
	return &copy
}

func cloneJSON(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = cloneJSON(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = cloneJSON(item)
		}
		return result
	default:
		return value
	}
}
