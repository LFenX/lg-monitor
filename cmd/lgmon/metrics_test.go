package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type fakeMetricsReader struct {
	closed  int
	targets []config.Target
	err     error
}

func (r *fakeMetricsReader) Close() error { r.closed++; return nil }
func (r *fakeMetricsReader) ExportMetrics(_ context.Context, targets []config.Target) (store.ExportMetricSnapshot, error) {
	r.targets = targets
	return store.ExportMetricSnapshot{TS: 2000, Families: []store.ExportMetricFamily{{Name: "lgmon_target_state", Help: "Latest state.", Unit: "1", Points: []store.ExportMetricPoint{{Labels: map[string]string{"collector_id": "fake-node", "target_id": "fake-web"}, Value: -1, TS: 1000}}}}}, r.err
}

func TestMetricsCLIUsesHomeReadOnlyFactoryAndSupportsTextJSONErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte("targets: [{id: fake-web, match: {process: fake.exe}}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, asJSON := range []bool{false, true} {
		reader := &fakeMetricsReader{}
		root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
		root.PersistentFlags().String("home", home, "home")
		root.AddCommand(newMetricsCommand(func(_ context.Context, actual string) (metricsReader, error) {
			if actual != home {
				t.Fatal("lost home", actual)
			}
			return reader, nil
		}))
		var output bytes.Buffer
		root.SetOut(&output)
		args := []string{"metrics"}
		if asJSON {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil || reader.closed != 1 || len(reader.targets) != 1 {
			t.Fatal(err, reader.closed, reader.targets)
		}
		if asJSON {
			var data store.ExportMetricSnapshot
			if err := json.Unmarshal(output.Bytes(), &data); err != nil || data.Families[0].Points[0].Value != -1 {
				t.Fatal("CLI did not preserve unknown state", output.String(), err)
			}
		} else if !strings.Contains(output.String(), "# TYPE lgmon_target_state gauge\n") || !strings.Contains(output.String(), " -1\n") {
			t.Fatal("CLI returned a wrapper instead of Prometheus", output.String())
		}
	}
	root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("home", home, "home")
	root.AddCommand(newMetricsCommand(func(context.Context, string) (metricsReader, error) { return nil, errors.New("fake read failure") }))
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"metrics", "--json"})
	if err := root.Execute(); err == nil || !json.Valid(output.Bytes()) || !strings.Contains(output.String(), "metrics_unavailable") {
		t.Fatal("CLI did not report structured failure", err, output.String())
	}
}

func TestMetricsCLIReadsRealStoppedDatabaseWithoutCreatingOrMigrating(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte("targets: [{id: fake-web, match: {process: fake.exe}}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UnixMilli()
	if err := db.Write(context.Background(), pipeline.Message{CollectorID: "fake-node", Seq: 1, TS: ts, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "fake-web", State: collector.StateUnknown}}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("home", home, "home")
	root.AddCommand(newMetricsCommand(openMetrics))
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"metrics", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var snapshot store.ExportMetricSnapshot
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range snapshot.Families {
		if f.Name == "lgmon_target_state" {
			for _, p := range f.Points {
				if p.Labels["target_id"] == "fake-web" && p.Value == -1 && p.TS == ts {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("stopped CLI lost actual persisted state", output.String())
	}
}
