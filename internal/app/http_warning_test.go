package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestHTTPListenerLogsExposureOnStartupAndReload(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:7444", "127.0.0.1:7444", "100.126.79.86:7444", "[fd7a:115c:a1e0::1]:7444"} {
		t.Run(listen, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			files := fstest.MapFS{"settings.yaml": {Data: fmt.Appendf(nil, "httpListen: %q\n", listen)}}
			manager, err := config.NewManager(config.FSLoader{FS: files}, logger)
			if err != nil {
				t.Fatal(err)
			}
			network := &fakeHTTPNetwork{calls: make(chan httpListenCall, 20)}
			broker := api.NewBroker()
			defer broker.Close()
			actions := map[api.Operation]api.Action{}
			for _, route := range api.Routes() {
				actions[route.ID] = func(context.Context, api.Request) (any, error) { return api.Confirmation{OK: true}, nil }
			}
			ready := make(chan string, 10)
			node, err := NewHTTPNode(context.Background(), HTTPNodeOptions{
				Config: manager, Network: network, Broker: broker, Actions: actions, Now: time.Now, Logger: logger,
				Entropy: bytes.NewReader(make([]byte, 32)),
				Ready:   func(address string) { ready <- address },
				Tokens: api.TokenLookupFunc(func(context.Context, [32]byte) (api.Principal, bool, error) {
					return api.Principal{Scope: api.ScopeRead}, true, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				node.Close()
				if err := node.Wait(); err != nil {
					t.Error(err)
				}
			}()
			receiveRuntime(t, ready)
			const message = "HTTP panel is exposed without encryption"
			want := 0
			if listen == "0.0.0.0:7444" {
				want = 1
			}
			if got := strings.Count(output.String(), message); got != want {
				t.Fatalf("startup warnings=%d, want=%d: %s", got, want, output.String())
			}
			cfg := manager.Snapshot()
			cfg.Settings.HTTPListen = "0.0.0.0:7445"
			if err := manager.Apply(cfg); err != nil {
				t.Fatal(err)
			}
			receiveRuntime(t, ready)
			if got := strings.Count(output.String(), message); got != want+1 {
				t.Fatalf("reload warnings=%d, want=%d: %s", got, want+1, output.String())
			}
		})
	}
}
