//go:build windows && integration && scale

package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/examples"
	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	"gopkg.in/yaml.v3"
)

const scaleCollectors = 20
const scaleDuration = 30 * time.Minute

type scaleTransition struct {
	Alert  rules.Alert
	SeenMS int64
	SentMS int64
}

type scaleMonitorReport struct {
	Records               map[string]map[pipeline.Kind]int
	LastSeq               map[string]uint64
	SequenceErrors        int
	CommitP99MS           float64
	Transitions           map[string]scaleTransition
	DatabaseBytes         int64
	DatabaseBytesPerHour  float64
	RawBytesPerHour       float64
	MinuteBytesPerDay     float64
	HourBytesPerDay       float64
	DefaultRetentionBytes float64
	DatabaseCounts        map[string]int64
}

type scaleObserver struct {
	mu     sync.Mutex
	api    RuntimeObserver
	report scaleMonitorReport
}

func (o *scaleObserver) Sample(message pipeline.Message) {
	o.api.Sample(message)
	o.mu.Lock()
	defer o.mu.Unlock()
	if message.Seq != o.report.LastSeq[message.CollectorID]+1 {
		o.report.SequenceErrors++
	}
	o.report.LastSeq[message.CollectorID] = message.Seq
	if o.report.Records[message.CollectorID] == nil {
		o.report.Records[message.CollectorID] = map[pipeline.Kind]int{}
	}
	o.report.Records[message.CollectorID][message.Kind]++
}

func (o *scaleObserver) Transition(alert rules.Alert, event rules.Event) {
	o.api.Transition(alert, event)
	if event.Kind != rules.StateFiring {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.report.Transitions[alert.ID] = scaleTransition{Alert: alert, SeenMS: time.Now().UnixMilli()}
}

type scaleCardClient struct {
	*apiClient
	observer *scaleObserver
}

func (c *scaleCardClient) SendCard(_ context.Context, card feishu.CardMessage) (feishu.SendResult, error) {
	if card.State == rules.StateFiring {
		c.observer.mu.Lock()
		value := c.observer.report.Transitions[card.AlertID]
		value.SentMS = time.Now().UnixMilli()
		c.observer.report.Transitions[card.AlertID] = value
		c.observer.mu.Unlock()
	}
	return feishu.SendResult{MessageID: "om_fake_scale"}, nil
}

func scaleRequire[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func scaleWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data := scaleRequire(json.MarshalIndent(value, "", "  "))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func scaleConfig(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	var targets []config.Target
	var configured []config.Rule
	for index := 0; index < examples.WorkloadTargets; index++ {
		targets = append(targets, examples.WorkloadTarget(index))
		configured = append(configured, config.Rule{ID: fmt.Sprintf("scale-down-%02d", index), Type: "process_absent",
			Target: examples.WorkloadTargetID(index), For: "0s", Severity: "critical", Notify: []string{"fake-scale"}})
	}
	documents := map[string]any{
		"settings.yaml": config.Settings{HTTPListen: "127.0.0.1:0"},
		"targets.yaml":  config.TargetsFile{Targets: targets},
		"rules.yaml":    config.RulesFile{Rules: configured},
		"notifiers.yaml": config.NotifiersFile{
			FeishuApps: []config.FeishuApp{{ID: "fake-app", AppID: "cli_fake_scale", SecretRef: "fake-scale-secret", Domain: "feishu"}},
			Notifiers:  []config.Notifier{{ID: "fake-scale", Type: "feishu_app", App: "fake-app", ChatID: "oc_fake_scale"}},
		},
	}
	for name, document := range documents {
		data := scaleRequire(yaml.Marshal(document))
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The helper owns only temporary data and loopback listeners; no installed services are involved.
func TestScaleMonitorChild(t *testing.T) {
	home := os.Getenv("LGMON_SCALE_CHILD_HOME")
	if home == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 38*time.Minute)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configuration := filepath.Join(home, "config")
	scaleConfig(t, configuration)
	manager := scaleRequire(config.NewManager(config.FSLoader{FS: os.DirFS(configuration)}, logger))
	watcher := scaleRequire(config.NewFileWatcher(configuration))
	defer watcher.Close()
	vault := scaleRequire(secrets.New(home, logger))
	if err := vault.Set(ctx, "fake-scale-secret", []byte("fake-only-scale-secret")); err != nil {
		t.Fatal(err)
	}
	state := transport.FileState{Home: home}
	authority := scaleRequire(transport.NewAuthority(ctx, transport.AuthorityOptions{Keys: vault, State: state, SANs: offlineSANs{}, Now: time.Now}))
	pairs := scaleRequire(transport.OpenPairDB(ctx, filepath.Join(home, "data", "pairing.db")))
	defer pairs.Close()
	path := filepath.Join(home, "data", "lgmon.db")
	db := scaleRequire(store.Open(ctx, store.Options{Path: path, Now: time.Now, Logger: logger}))
	defer db.Close()
	receiver, server := transport.NewSecureServer(authority, pairs, func(ctx context.Context, hello transport.Hello) error {
		at := time.Now().UnixMilli()
		if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: hello.CollectorID, Hostname: hello.Hostname, Version: hello.Version, FirstSeen: at, LastSeen: at}); err != nil {
			return err
		}
		if hello.ClockKnown {
			return db.SetClockOffset(ctx, hello.CollectorID, hello.ClockOffsetMS)
		}
		return nil
	}, db.LastCommitted)
	defer receiver.Close()
	receiver.SetConfigDelivery(transport.ConfigDelivery{Source: manager, Logger: logger})
	receiver.SetHeartbeat(func(ctx context.Context, hello transport.Hello) error {
		if err := db.Heartbeat(ctx, hello.CollectorID, hello.Cache); err != nil {
			return err
		}
		if hello.ClockKnown {
			return db.SetClockOffset(ctx, hello.CollectorID, hello.ClockOffsetMS)
		}
		return nil
	})
	listener := scaleRequire(net.Listen("tcp4", "127.0.0.1:0"))
	defer listener.Close()
	endpoint := scaleRequire(json.Marshal(map[string]string{"address": listener.Addr().String()}))
	if err := state.Save(ctx, "endpoint", endpoint, true); err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Stop()
	presence := pairedPresence{pairs: pairs, db: db, link: receiver, now: time.Now, startedAt: time.Now().UnixMilli()}
	view := scaleRequire(store.OpenViewWithClock(ctx, path, time.Now))
	defer view.Close()
	sqlView := scaleRequire(store.OpenViewWithClock(ctx, path, time.Now))
	defer sqlView.Close()
	query := scaleRequire(store.OpenRuleQueries(ctx, path))
	defer query.Close()
	broker := api.NewBroker()
	defer broker.Close()
	observer := &scaleObserver{api: newAPIEvents(broker, logger), report: scaleMonitorReport{
		Records: map[string]map[pipeline.Kind]int{}, LastSeq: map[string]uint64{}, Transitions: map[string]scaleTransition{},
	}}
	client := &scaleCardClient{apiClient: &apiClient{}, observer: observer}
	factory := func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil }
	editor := scaleRequire(config.NewEditor(config.DirectoryDocuments{Path: configuration}, manager))
	backend := scaleRequire(NewAPIBackend(APIBackendOptions{Config: manager, Editor: editor, Read: view, SQL: sqlView, Write: db,
		Collectors: presence, Processes: receiver, Secrets: vault, NewClient: factory, Now: time.Now, Logger: logger}))
	httpNode := scaleRequire(NewHTTPNode(ctx, HTTPNodeOptions{Config: manager, Version: "fake-scale", Actions: backend.Actions(), Broker: broker,
		Now: time.Now, Entropy: rand.Reader, Logger: logger, Tokens: api.TokenLookupFunc(func(_ context.Context, hash [32]byte) (api.Principal, bool, error) {
			return api.Principal{Name: "fake-scale", Scope: api.ScopeRead}, hash == api.TokenHash("fake-scale-read"), nil
		}), Ready: func(address string) {
			scaleWriteJSON(t, filepath.Join(home, "http.json"), map[string]string{"address": address})
		}}))
	defer func() { httpNode.Close(); _ = httpNode.Wait() }()
	commitFile := scaleRequire(os.Create(filepath.Join(home, "commits.bin")))
	defer commitFile.Close()
	commitWriter := bufio.NewWriterSize(commitFile, 64*1024)
	committed := &scaleCommitStore{Store: db, output: commitWriter, collectors: map[string]uint64{}}
	for index := 0; index < scaleCollectors; index++ {
		committed.collectors[fmt.Sprintf("fake-scale-%02d", index)] = uint64(index)
	}
	runtime := scaleRequire(NewRuntime(RuntimeOptions{ID: "fake-scale-monitor", Hostname: "FakeScaleMonitor", Version: "fake-scale",
		Config: manager, Watcher: watcher, Store: committed, Queries: query, Source: receiver, Secrets: vault, NewClient: factory,
		Toast: &testToastClient{}, Now: time.Now, Logger: logger, Collectors: presence, Observer: observer,
		Ready: func() {
			scaleWriteJSON(t, filepath.Join(home, "ready.json"), map[string]string{"address": listener.Addr().String()})
		}}))
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(home, "stop")); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	if err := runtime.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := commitWriter.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := commitFile.Close(); err != nil {
		t.Fatal(err)
	}
	scaleStorageReport(t, path, &observer.report)
	scaleWriteJSON(t, filepath.Join(home, "monitor-report.json"), observer.report)
}

func scaleStorageReport(t *testing.T, path string, report *scaleMonitorReport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// Advance only the injected store clock to close the last partial rollup buckets.
	db := scaleRequire(store.Open(ctx, store.Options{Path: path, Now: func() time.Time { return time.Now().Add(2 * time.Hour) }, DisableMaintenance: true}))
	if err := db.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reader := scaleRequire(sql.Open("sqlite", path))
	defer reader.Close()
	if _, err := reader.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	var pageCount, pageSize int64
	if err := reader.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	report.DatabaseBytes = pageCount * pageSize
	report.DatabaseCounts = map[string]int64{}
	bytesFor := func(tables ...string) int64 {
		var result int64
		for _, table := range tables {
			var size int64
			if err := reader.QueryRowContext(ctx, "SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name IN (SELECT name FROM sqlite_schema WHERE tbl_name=?)", table).Scan(&size); err != nil {
				t.Fatal(err)
			}
			result += size
		}
		return result
	}
	var first, last int64
	if err := reader.QueryRowContext(ctx, "SELECT MIN(ts),MAX(ts) FROM process_samples").Scan(&first, &last); err != nil {
		t.Fatal(err)
	}
	hours := float64(last-first+2000) / float64(time.Hour/time.Millisecond)
	rawBytes := bytesFor("target_status", "process_samples", "probe_results", "system_metrics", "disk_metrics", "network_metrics", "service_status", "script_metrics")
	minuteBytes, hourBytes := bytesFor("metric_minute", "probe_minute"), bytesFor("metric_hour", "probe_hour")
	var minutes, hourly int64
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(DISTINCT ts) FROM metric_minute").Scan(&minutes); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(DISTINCT ts) FROM metric_hour").Scan(&hourly); err != nil {
		t.Fatal(err)
	}
	report.DatabaseBytesPerHour = float64(report.DatabaseBytes) / hours
	report.RawBytesPerHour = float64(rawBytes) / hours
	report.MinuteBytesPerDay = float64(minuteBytes) / float64(minutes) * 1440
	report.HourBytesPerDay = float64(hourBytes) / float64(hourly) * 24
	misc := report.DatabaseBytes - rawBytes - minuteBytes - hourBytes
	report.DefaultRetentionBytes = report.RawBytesPerHour*24 + report.MinuteBytesPerDay*14 + report.HourBytesPerDay*365 + float64(misc)
	for _, table := range []string{"target_status", "process_samples", "probe_results", "system_metrics", "collector_events"} {
		var count int64
		if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		report.DatabaseCounts[table] = count
	}
	var active int64
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE state='firing'").Scan(&active); err != nil {
		t.Fatal(err)
	}
	report.DatabaseCounts["active_alerts"] = active
}
