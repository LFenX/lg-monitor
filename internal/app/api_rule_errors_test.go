package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type ruleErrorSink struct{}

func (ruleErrorSink) Enqueue(context.Context, rules.Alert) error { return nil }

type ruleErrorApplier struct{}

func (ruleErrorApplier) Apply(context.Context, string, []byte) error { return nil }

type ruleErrorsWire chan []byte

func (wire ruleErrorsWire) Publish(name string, value any) error {
	if name != "ruleErrors" {
		return nil
	}
	data, err := json.Marshal(value)
	if err == nil {
		wire <- data
	}
	return err
}

func TestCertificateCapabilityErrorOnRealStreamAndUpgradeWithoutBlockingConfig(t *testing.T) {
	f := newAPIFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	f.files.MapFS["targets.yaml"] = &fstest.MapFile{Data: []byte("targets: [{id: web, collector: fake-legacy, match: {process: fake.exe}, probes: [{type: http, url: 'https://fake.invalid', interval: 30s, timeout: 2s}]}]\n")}
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: [{id: cert, name: 虚构证书, type: cert_expiry, target: web, severity: warning, notify: [fake-channel]}]\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	receiver := transport.NewReceiver(func(ctx context.Context, hello transport.Hello) error {
		return f.db.RegisterCollector(ctx, store.CollectorRecord{ID: hello.CollectorID, Hostname: hello.Hostname, FirstSeen: f.now.UnixMilli(), LastSeen: f.now.UnixMilli()})
	})
	defer receiver.Close()
	receiver.SetConfigDelivery(transport.ConfigDelivery{Source: f.manager, Logger: logger})
	listener := bufconn.Listen(1024 * 1024)
	server := transport.NewServer(receiver)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	connection, err := grpc.NewClient("passthrough:///fake-rule-errors", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	presence := pairedPresence{pairs: &listedPairs{items: []transport.Credential{{ID: "fake-legacy", Hostname: "FakeLegacy", IssuedAt: f.now.UnixMilli()}}},
		db: f.db, link: receiver, now: func() time.Time { return f.now }, startedAt: f.now.UnixMilli()}
	engine, err := rules.New(ctx, rules.Options{Config: f.manager.Snapshot(), Repository: f.db, Notifications: ruleErrorSink{}, Collectors: presence, Now: func() time.Time { return f.now }, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Millisecond):
			}
		}
	}
	for index, capabilities := range [][]string{nil, {transport.HTTPChecksCapability}} {
		writer, err := transport.NewWriter(ctx, connection, transport.Hello{CollectorID: "fake-legacy", Hostname: "FakeLegacy", Capabilities: capabilities}, ruleErrorApplier{})
		if err != nil {
			t.Fatal(err)
		}
		wait(func() bool { return receiver.ConfigState("fake-legacy").State == "applied" })
		if err := engine.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		response := f.request(t, "GET", "/api/v1/rule-errors", "read", "", 200)
		var rows []api.RuleError
		if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Active != (index == 0) ||
			rows[0].RuleName != "虚构证书" || rows[0].RuleType != "cert_expiry" || rows[0].Consecutive != 0 || !strings.Contains(rows[0].Error, "fake-legacy") {
			t.Fatalf("capability diagnosis: %s %v", response.Body.String(), err)
		}
		writer.Close()
		wait(func() bool { return !receiver.Connected("fake-legacy") })
		if index == 0 {
			if err := engine.Evaluate(ctx); err != nil {
				t.Fatal(err)
			}
			rows, err := f.view.RecentRuleErrors(ctx)
			if err != nil || len(rows) != 1 || !rows[0].Active {
				t.Fatalf("disconnect cleared diagnosis: %+v %v", rows, err)
			}
		}
	}
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: []\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := engine.UpdateConfigContext(ctx, f.manager.Snapshot()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.view.RecentRuleErrors(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleted rule error remains in database: %+v %v", rows, err)
	}
}

func TestRuleErrorsHTTPOrderingMCPAndLiveChanges(t *testing.T) {
	f := newAPIFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: [{id: down, name: 虚构规则, type: process_absent, target: web, for: 0s, severity: critical, notify: [fake-channel]}, {id: sql, type: sql, query: 'SELECT 1 AS firing', severity: warning, notify: [fake-channel]}]\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	backend := &APIBackend{options: APIBackendOptions{Config: f.manager, Read: f.view}}
	wire := make(ruleErrorsWire, 16)
	watchRuleErrorEvents(ctx, backend, f.manager, f.db.RuleErrorChanges(), wire, slog.New(slog.NewTextHandler(io.Discard, nil)))
	readEvent := func(check func([]api.RuleError) bool) []api.RuleError {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case data := <-wire:
				var rows []api.RuleError
				if err := json.Unmarshal(data, &rows); err != nil {
					t.Fatal(err)
				}
				if check(rows) {
					return rows
				}
			case <-timer.C:
				t.Fatal("ruleErrors event was not delivered")
			}
		}
	}
	readEvent(func(rows []api.RuleError) bool { return len(rows) == 0 })
	for _, row := range []rules.RuleError{
		{RuleID: "deleted", Error: "removed rule", At: 5000, Active: true},
		{RuleID: "down", Error: "recovered failure", At: 4000, Active: false},
		{RuleID: "sql", Error: "fake SQL failure", At: 1000, Consecutive: 2, Active: true},
	} {
		if err := f.db.RecordRuleEvaluation(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	rows := readEvent(func(rows []api.RuleError) bool {
		return len(rows) == 2 && rows[0].RuleID == "sql" && rows[1].RuleID == "down"
	})
	response := f.request(t, "GET", "/api/v1/rule-errors", "read", "", 200)
	want, _ := json.Marshal(rows)
	if strings.TrimSpace(response.Body.String()) != string(want) {
		t.Fatalf("HTTP/live mismatch: %s %s", response.Body.String(), want)
	}
	f.request(t, "GET", "/api/v1/rule-errors", "", "", 401)
	network := httptest.NewServer(f.server)
	defer network.Close()
	session := mcpSession(t, network.URL, "fake-read", false)
	content := mcpCall(t, session, "list_rule_errors", map[string]any{}, false)
	var toolRows []api.RuleError
	if err := json.Unmarshal([]byte(content), &toolRows); err != nil || !slices.Equal(toolRows, rows) {
		t.Fatalf("MCP differs from HTTP: %s %v", content, err)
	}
	if err := f.db.RecordRuleEvaluation(ctx, rules.RuleError{RuleID: "sql"}); err != nil {
		t.Fatal(err)
	}
	readEvent(func(rows []api.RuleError) bool { return len(rows) == 2 && !rows[0].Active && !rows[1].Active })
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: []\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	readEvent(func(rows []api.RuleError) bool { return len(rows) == 0 })
	if err := f.db.PruneRuleErrors(ctx, nil); err != nil {
		t.Fatal(err)
	}
	stored, err := f.view.RecentRuleErrors(ctx)
	if err != nil || len(stored) != 0 {
		t.Fatalf("pruned database: %+v %v", stored, err)
	}
}
