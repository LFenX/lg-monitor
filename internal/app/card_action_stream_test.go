package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
)

type actionStreamFixture struct{ closed chan struct{} }

func (s *actionStreamFixture) Run(ctx context.Context, _ feishu.CardActionHandler, state feishu.ActionConnectionState) error {
	state("connected")
	<-ctx.Done()
	close(s.closed)
	return nil
}

type actionKeyFixture struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (k *actionKeyFixture) Get(_ context.Context, id string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]byte(nil), k.values[id]...), nil
}
func (k *actionKeyFixture) Set(_ context.Context, id string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[id] = append([]byte(nil), value...)
	return nil
}

func TestCardActionStreamsShareAppRotateCredentialAndJoinRevocation(t *testing.T) {
	f, m, _, _, _ := appCardActionFixture(t)
	keys := &actionKeyFixture{values: map[string][]byte{"fake-ref": []byte("first"), cardActionKeyName: append([]byte(nil), f.vault.values[cardActionKeyName]...)}}
	m.options.Keys = keys
	started := make(chan *actionStreamFixture, 4)
	m.options.NewStream = func(app config.FeishuApp, secret []byte) (feishu.ActionStream, error) {
		s := &actionStreamFixture{closed: make(chan struct{})}
		started <- s
		return s, nil
	}
	editor, err := config.NewEditor(f.files, f.manager)
	if err != nil {
		t.Fatal(err)
	}
	_, err = editor.Update(context.Background(), "notifiers.yaml", func(data []byte) ([]byte, error) {
		file, err := config.ParseNotifiers(data)
		if err != nil {
			return nil, err
		}
		second := file.Notifiers[0]
		second.ID = "second-channel"
		second.ChatID = "oc_fake_second"
		alias := file.FeishuApps[0]
		alias.ID = "second-app"
		file.FeishuApps = append(file.FeishuApps, alias)
		second.App = alias.ID
		file.Notifiers = append(file.Notifiers, second)
		return json.Marshal(file)
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	next := func() *actionStreamFixture {
		t.Helper()
		select {
		case s := <-started:
			return s
		case <-time.After(4 * time.Second):
			t.Fatal("stream did not start")
			return nil
		}
	}
	first := next()
	keys.Set(context.Background(), "fake-ref", []byte("second"))
	second := next()
	select {
	case <-first.closed:
	default:
		t.Fatal("old credential connection not joined")
	}
	if _, err := editor.Update(context.Background(), "notifiers.yaml", func(data []byte) ([]byte, error) {
		updated, err := config.CardActionsConfig(data, "fake-channel", false)
		if err != nil {
			return nil, err
		}
		return config.CardActionsConfig(updated, "second-channel", false)
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked stream remained active")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not join")
	}
	if _, err := m.signingKey(context.Background()); err == nil {
		t.Fatal("closed listener retained signing key")
	}
	select {
	case <-started:
		t.Fatal("unexpected extra connection")
	default:
	}
}
