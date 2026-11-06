//go:build integration && windows

package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type offlineSANs struct{}

func (offlineSANs) Discover(context.Context) (transport.SANs, error) {
	return transport.SANs{Names: []string{"monitor.test"}, IPs: []net.IP{net.ParseIP("127.0.0.1")}}, nil
}

type switchConnector struct {
	mu       sync.Mutex
	listener *bufconn.Listener
}

func (c *switchConnector) Connect(_ context.Context, _ string, configuration *tls.Config) (transport.RPCConnection, error) {
	configuration = configuration.Clone()
	configuration.ServerName = "monitor.test"
	return grpc.NewClient("passthrough:///monitor.test:7443", grpc.WithTransportCredentials(credentials.NewTLS(configuration)), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		c.mu.Lock()
		listener := c.listener
		c.mu.Unlock()
		if listener == nil {
			return nil, fmt.Errorf("own test connection is offline")
		}
		return listener.DialContext(ctx)
	}))
}
func (c *switchConnector) set(listener *bufconn.Listener) {
	c.mu.Lock()
	c.listener = listener
	c.mu.Unlock()
}

func openOfflineReader(t *testing.T, path string) *sql.DB {
	t.Helper()
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(1000)"}}.Encode()}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOfflineTwoMinutesPreservesThreeNativeTargetChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { listener.Close() })
	files := fstest.MapFS{"targets.yaml": {Data: []byte(fmt.Sprintf("targets:\n- id: own-offline\n  match: {port: %d}\n", port))}}
	manager, err := config.NewManager(config.FSLoader{FS: files}, logger)
	if err != nil {
		t.Fatal(err)
	}
	monitorHome, collectorHome := t.TempDir(), t.TempDir()
	monitorKeys, err := secrets.New(monitorHome, logger)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := transport.NewAuthority(ctx, transport.AuthorityOptions{Keys: monitorKeys, State: transport.FileState{Home: monitorHome}, SANs: offlineSANs{}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := transport.OpenPairDB(ctx, filepath.Join(monitorHome, "data", "pairing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pairing.Close() })
	connector := &switchConnector{}
	backendPath := filepath.Join(monitorHome, "data", "lgmon.db")
	type node struct {
		server   *grpc.Server
		receiver *transport.Receiver
		listener *bufconn.Listener
		cancel   context.CancelFunc
		finished chan error
		db       *store.Store
	}
	var current *node
	stopMonitor := func() {
		if current == nil {
			return
		}
		connector.set(nil)
		current.cancel()
		current.server.Stop()
		current.receiver.Close()
		current.listener.Close()
		select {
		case err := <-current.finished:
			if err != nil {
				t.Errorf("monitor shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("monitor did not stop")
		}
		current = nil
	}
	t.Cleanup(stopMonitor)
	startMonitor := func() {
		t.Helper()
		db, err := store.Open(ctx, store.Options{Path: backendPath, Now: time.Now, Logger: logger})
		if err != nil {
			t.Fatal(err)
		}
		receiver, server := transport.NewSecureServer(authority, pairing, func(ctx context.Context, hello transport.Hello) error {
			at := time.Now().UnixMilli()
			return db.RegisterCollector(ctx, store.CollectorRecord{ID: hello.CollectorID, Hostname: hello.Hostname, FirstSeen: at, LastSeen: at})
		}, db.LastCommitted)
		memory := bufconn.Listen(1024 * 1024)
		runCtx, runCancel := context.WithCancel(ctx)
		runtime, err := NewRuntime(RuntimeOptions{ID: "monitor", Hostname: "MonitorHost", Config: manager, Store: db, Source: receiver, Secrets: runtimeVault{}, NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return &runtimeClient{}, nil }, Now: time.Now, Logger: logger})
		if err != nil {
			t.Fatal(err)
		}
		current = &node{server: server, receiver: receiver, listener: memory, cancel: runCancel, finished: make(chan error, 1), db: db}
		active := current
		go server.Serve(memory)
		go func() { active.finished <- runtime.Run(runCtx) }()
		connector.set(memory)
	}
	startMonitor()
	admin, err := connector.Connect(ctx, "monitor.test:7443", authority.AdminTLS())
	if err != nil {
		t.Fatal(err)
	}
	code, err := lgmonv1.NewPairingClient(admin).NewCode(ctx, &lgmonv1.NewCodeRequest{TtlMillis: 60000})
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	collectorKeys, err := secrets.New(collectorHome, logger)
	if err != nil {
		t.Fatal(err)
	}
	id := "fake-offline-collector"
	state := transport.FileState{Home: collectorHome}
	if _, err := transport.Join(ctx, transport.JoinOptions{Address: "monitor.test:7443", CollectorID: id, Hostname: "CollectorHost", Code: code.Code, Fingerprint: code.Fingerprint, Keys: collectorKeys, State: state, Connector: connector, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	identity, err := transport.LoadClient(ctx, state, collectorKeys, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	boxPath := filepath.Join(collectorHome, "data", "outbox.db")
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: boxPath, CollectorID: id, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { box.Close() })
	delivery, err := transport.NewDelivery(transport.DeliveryOptions{Outbox: box, Identity: identity, Hostname: "CollectorHost", Connector: connector, MaxBackoff: 2 * time.Second, Jitter: func(base time.Duration) time.Duration { return base }, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	deliveryDone := make(chan error, 1)
	go func() { deliveryDone <- delivery.Run(deliveryCtx) }()
	t.Cleanup(func() {
		stopDelivery()
		select {
		case err := <-deliveryDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("delivery did not stop")
		}
	})
	prober := collector.NewProber(time.Now)
	t.Cleanup(prober.Close)
	local, err := NewCollector(CollectorOptions{ID: id, Config: manager, Processes: collector.NewProcessSource(time.Now, logger), Ports: collector.NewPortSource(time.Now, logger), Probes: prober, Sink: box, Now: time.Now, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	collectCtx, stopCollect := context.WithCancel(ctx)
	collectDone := make(chan error, 1)
	go func() { collectDone <- local.Run(collectCtx) }()
	collectStopped := false
	t.Cleanup(func() {
		if !collectStopped {
			stopCollect()
			select {
			case err := <-collectDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("native collector did not stop")
			}
		}
	})
	waitFor := func(label string, condition func() bool) {
		t.Helper()
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			if condition() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("%s: context expired", label)
			case <-timer.C:
				t.Fatalf("%s exceeded 15 seconds", label)
			case <-ticker.C:
			}
		}
	}
	waitFor("initial committed up", func() bool {
		targets, err := current.db.CurrentTargets(ctx)
		return err == nil && len(targets) == 1 && targets[0].State == "up"
	})
	stopMonitor()
	outageStart := time.Now()
	t.Log("Own collector connection is offline; starting the full two-minute window")
	cacheReader := openOfflineReader(t, boxPath)
	cachedState := func(want string) bool {
		rows, err := cacheReader.QueryContext(ctx, `SELECT payload FROM outbox_records ORDER BY seq DESC LIMIT 128`)
		if err != nil {
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if rows.Scan(&data) != nil {
				return false
			}
			var record lgmonv1.Record
			if proto.Unmarshal(data, &record) != nil {
				return false
			}
			if status := record.GetTargetStatus(); status != nil && status.TargetId == "own-offline" {
				return status.State == want
			}
		}
		return false
	}
	listener.Close()
	waitFor("first offline change down", func() bool { return cachedState("down") })
	t.Log("Buffered change 1/3: down")
	waitUntil := func(offset time.Duration) {
		t.Helper()
		remaining := time.Until(outageStart.Add(offset))
		if remaining <= 0 {
			return
		}
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			t.Fatal("offline window expired")
		case <-timer.C:
		}
	}
	waitUntil(40 * time.Second)
	listener, err = net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	waitFor("second offline change up", func() bool { return cachedState("up") })
	t.Log("Buffered change 2/3: up")
	waitUntil(80 * time.Second)
	listener.Close()
	waitFor("third offline change down", func() bool { return cachedState("down") })
	t.Log("Buffered change 3/3: down")
	waitUntil(2 * time.Minute)
	if time.Since(outageStart) < 2*time.Minute {
		t.Fatal("connection interruption was shorter than two minutes")
	}
	startMonitor()
	t.Logf("Reopened own monitor after %s; replaying durable outbox", time.Since(outageStart))
	stopCollect()
	if err := <-collectDone; err != nil {
		t.Fatal(err)
	}
	collectStopped = true
	waitFor("all messages acknowledged", func() bool {
		state, err := box.State(ctx)
		return err == nil && state.Allocated > 0 && state.Acked == state.Allocated
	})
	watermark, err := box.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reader := openOfflineReader(t, backendPath)
	rows, err := reader.QueryContext(ctx, `SELECT state FROM target_status WHERE collector_id=? AND target_id='own-offline' ORDER BY ts`, id)
	if err != nil {
		t.Fatal(err)
	}
	var changes []string
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			t.Fatal(err)
		}
		if len(changes) == 0 || changes[len(changes)-1] != state {
			changes = append(changes, state)
		}
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	want := []string{"up", "down", "up", "down"}
	if len(changes) != len(want) {
		t.Fatalf("missing/duplicated changes: %v", changes)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("out-of-order target history: %v", changes)
		}
	}
	var stored uint64
	if err := reader.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM target_status WHERE collector_id=?)+(SELECT count(*) FROM process_samples WHERE collector_id=?)+(SELECT count(*) FROM probe_results WHERE collector_id=?)`, id, id, id).Scan(&stored); err != nil || stored != watermark.Allocated {
		t.Fatalf("duplicate/lost raw records: stored=%d allocated=%d error=%v", stored, watermark.Allocated, err)
	}
	if applied, err := current.db.LastCommitted(ctx, id); err != nil || applied != stored {
		t.Fatalf("continuous watermark: %d %v", applied, err)
	}
	t.Logf("Two-minute outage preserved all three changes in order; %d raw records stored exactly once", stored)
}
