package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
)

type cardActionWorker struct {
	fingerprint [32]byte
	cancel      context.CancelFunc
	done        chan struct{}
}

func actionStreamID(app config.FeishuApp) string { return app.Domain + "\x00" + app.AppID }

func (m *CardActions) state(app, state string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.states[app] = api.CardActionsStatus{State: state, UpdatedAt: m.options.Now().UnixMilli()}
}

func (m *CardActions) Status(id string) (api.CardActionsStatus, error) {
	cfg := m.options.Config.Snapshot()
	for _, channel := range cfg.Notifiers {
		if channel.ID != id {
			continue
		}
		if channel.Type != "feishu_app" {
			return api.CardActionsStatus{}, invalidAPI("只有飞书应用渠道支持卡片动作")
		}
		result := api.CardActionsStatus{Notifier: id, App: channel.App, Enabled: channel.CardActions, State: "disabled"}
		if !result.Enabled {
			return result, nil
		}
		m.stateMu.Lock()
		_, app, _ := actionChannel(cfg, id)
		state, found := m.states[actionStreamID(app)]
		m.stateMu.Unlock()
		result.State = "waiting"
		if found {
			result.State = state.State
			result.UpdatedAt = state.UpdatedAt
		}
		return result, nil
	}
	return api.CardActionsStatus{}, missingAPI("通知渠道不存在")
}

func (m *CardActions) Run(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return fmt.Errorf("卡片动作监听只能启动一次")
	}
	updates := m.options.Config.Subscribe(ctx)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	workers := map[string]*cardActionWorker{}
	stop := func(id string, w *cardActionWorker) { w.cancel(); <-w.done; delete(workers, id) }
	defer func() {
		for id, w := range workers {
			stop(id, w)
		}
		m.closeSigningKey()
	}()
	reconcile := func() {
		cfg := m.options.Config.Snapshot()
		enabled := map[string]bool{}
		for _, channel := range cfg.Notifiers {
			if channel.Type == "feishu_app" && channel.CardActions {
				enabled[channel.App] = true
			}
		}
		physical := map[string]bool{}
		for _, app := range cfg.FeishuApps {
			if enabled[app.ID] {
				physical[actionStreamID(app)] = true
			}
		}
		for id, w := range workers {
			if !physical[id] {
				stop(id, w)
				m.state(id, "stopped")
			}
		}
		visited := map[string]bool{}
		for _, app := range cfg.FeishuApps {
			if !enabled[app.ID] {
				continue
			}
			streamID := actionStreamID(app)
			if visited[streamID] {
				continue
			}
			visited[streamID] = true
			key, err := m.signingKey(ctx)
			clear(key)
			if err != nil {
				if w := workers[streamID]; w != nil {
					stop(streamID, w)
				}
				m.state(streamID, "error")
				continue
			}
			secret, err := m.options.Keys.Get(ctx, app.SecretRef)
			if err != nil || len(secret) == 0 {
				clear(secret)
				if w := workers[streamID]; w != nil {
					stop(streamID, w)
				}
				m.state(streamID, "error")
				continue
			}
			data, _ := json.Marshal(app)
			hash := sha256.New()
			_, _ = hash.Write(data)
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write(secret)
			var fingerprint [32]byte
			copy(fingerprint[:], hash.Sum(nil))
			clear(data)
			if w := workers[streamID]; w != nil {
				if w.fingerprint == fingerprint {
					clear(secret)
					continue
				}
				stop(streamID, w)
			}
			child, cancel := context.WithCancel(ctx)
			w := &cardActionWorker{fingerprint: fingerprint, cancel: cancel, done: make(chan struct{})}
			workers[streamID] = w
			go func(app config.FeishuApp, secret []byte, w *cardActionWorker) {
				defer close(w.done)
				defer clear(secret)
				for child.Err() == nil {
					m.state(streamID, "connecting")
					stream, err := m.options.NewStream(app, secret)
					if err == nil {
						err = stream.Run(child, func(ctx context.Context, event feishu.CardAction) feishu.CardActionReply {
							return m.Handle(ctx, app, event)
						}, func(state string) { m.state(streamID, state) })
					}
					if child.Err() != nil {
						break
					}
					m.state(streamID, "error")
					m.options.Logger.Warn("Card action connection interrupted; retrying", "app", app.ID)
					timer := time.NewTimer(5 * time.Second)
					select {
					case <-child.Done():
						timer.Stop()
					case <-timer.C:
					}
				}
				m.state(streamID, "stopped")
			}(app, secret, w)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-updates:
			if !ok {
				return nil
			}
			reconcile()
		case <-ticker.C:
			reconcile()
		}
	}
}
