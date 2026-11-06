package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

type fakeHTTPListener struct {
	closed  chan struct{}
	failure chan error
	once    sync.Once
	address net.Addr
}

func (l *fakeHTTPListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case err := <-l.failure:
		return nil, err
	}
}
func (l *fakeHTTPListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *fakeHTTPListener) Addr() net.Addr { return l.address }

type httpListenCall struct {
	address  string
	listener *fakeHTTPListener
	err      error
}
type fakeHTTPNetwork struct {
	mu        sync.Mutex
	failure   error
	calls     chan httpListenCall
	listeners []*fakeHTTPListener
}

func (n *fakeHTTPNetwork) Listen(_ context.Context, address string) (net.Listener, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.failure != nil {
		n.calls <- httpListenCall{address: address, err: n.failure}
		return nil, n.failure
	}
	listener := &fakeHTTPListener{closed: make(chan struct{}), failure: make(chan error, 1), address: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 30000 + len(n.listeners)}}
	n.listeners = append(n.listeners, listener)
	n.calls <- httpListenCall{address: address, listener: listener}
	return listener, nil
}
func (n *fakeHTTPNetwork) Addresses(context.Context) ([]string, error) {
	return []string{"100.64.0.4"}, nil
}
func (n *fakeHTTPNetwork) fail(err error) { n.mu.Lock(); n.failure = err; n.mu.Unlock() }

func httpNodeFixture(t *testing.T) (*HTTPNode, *fakeHTTPNetwork, *config.Manager, <-chan string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	network := &fakeHTTPNetwork{calls: make(chan httpListenCall, 20)}
	ready := make(chan string, 10)
	broker := api.NewBroker()
	t.Cleanup(broker.Close)
	actions := map[api.Operation]api.Action{}
	for _, route := range api.Routes() {
		actions[route.ID] = func(context.Context, api.Request) (any, error) {
			return api.Confirmation{OK: true, Message: "完成"}, nil
		}
	}
	node, err := NewHTTPNode(context.Background(), HTTPNodeOptions{Config: manager, Network: network, Actions: actions, Broker: broker, Now: time.Now, Logger: logger, Entropy: bytes.NewReader(make([]byte, 32)), Ready: func(address string) { ready <- address }, Tokens: api.TokenLookupFunc(func(context.Context, [32]byte) (api.Principal, bool, error) {
		return api.Principal{Name: "fake", Scope: api.ScopeRead}, true, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		node.Close()
		if err := node.Wait(); err != nil {
			t.Error(err)
		}
	})
	return node, network, manager, ready
}

func TestHTTPListenerReloadKeepsOldListenerUntilReplacementIsReady(t *testing.T) {
	node, network, manager, ready := httpNodeFixture(t)
	first := receiveRuntime(t, network.calls)
	if first.address != "127.0.0.1:7444" {
		t.Fatal("default HTTP listener differs")
	}
	receiveRuntime(t, ready)
	local := httptest.NewRequest("GET", "http://127.0.0.1:7444/", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	value, ok, err := node.LocalSession(local)
	if err != nil || !ok {
		t.Fatal(err)
	}
	cfg := manager.Snapshot()
	cfg.Settings.HTTPListen = "100.64.0.4:7444"
	if err := manager.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	second := receiveRuntime(t, network.calls)
	receiveRuntime(t, ready)
	select {
	case <-first.listener.closed:
	case <-time.After(time.Second):
		t.Fatal("replaced listener was not closed")
	}
	if got, ok, err := node.LocalSession(local); err != nil || !ok || got != value {
		t.Fatal("listener reload rotated the local browser session")
	}
	network.fail(errors.New("fake port in use"))
	cfg.Settings.HTTPListen = "100.64.0.4:7445"
	if err := manager.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	failed := receiveRuntime(t, network.calls)
	if failed.err == nil {
		t.Fatal("failed bind was ignored")
	}
	select {
	case <-second.listener.closed:
		t.Fatal("failed rebind closed the previous listener")
	default:
	}
	network.fail(nil)
	if err := manager.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	third := receiveRuntime(t, network.calls)
	receiveRuntime(t, ready)
	node.Close()
	if err := node.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, listener := range []*fakeHTTPListener{second.listener, third.listener} {
		select {
		case <-listener.closed:
		default:
			t.Fatal("shutdown leaked a listener")
		}
	}
}

func TestHTTPPublicURLReloadUpdatesHostPolicyWithoutRebinding(t *testing.T) {
	node, network, manager, ready := httpNodeFixture(t)
	receiveRuntime(t, network.calls)
	receiveRuntime(t, ready)
	cfg := manager.Snapshot()
	cfg.Settings.PublicBaseURL = "http://new.example.test"
	if err := manager.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://new.example.test/api/v1/collectors", nil)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for node.auth.CheckHost(request) != nil {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("publicBaseUrl did not update Host policy")
		}
	}
	select {
	case call := <-network.calls:
		t.Fatalf("public URL change rebound listener: %+v", call)
	default:
	}
}

func TestHTTPUnexpectedServeFailureIsReportedAndResourcesAreReleased(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	network := &fakeHTTPNetwork{calls: make(chan httpListenCall, 2)}
	actions := map[api.Operation]api.Action{}
	for _, route := range api.Routes() {
		actions[route.ID] = func(context.Context, api.Request) (any, error) { return nil, nil }
	}
	broker := api.NewBroker()
	defer broker.Close()
	node, err := NewHTTPNode(context.Background(), HTTPNodeOptions{Config: manager, Network: network, Actions: actions, Broker: broker, Now: time.Now, Logger: logger, Entropy: bytes.NewReader(make([]byte, 32)), Tokens: api.TokenLookupFunc(func(context.Context, [32]byte) (api.Principal, bool, error) { return api.Principal{}, false, nil })})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	call := receiveRuntime(t, network.calls)
	failure := errors.New("fake accept failure")
	call.listener.failure <- failure
	if err := node.Wait(); !errors.Is(err, failure) {
		t.Fatalf("HTTP serve failure lost cause: %v", err)
	}
	select {
	case <-call.listener.closed:
	default:
		t.Fatal("failed HTTP listener leaked")
	}
}
