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

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type fakeQueryReader struct {
	result              rules.QueryResult
	schema              store.DatabaseSchema
	query               string
	schemaCalls, closed int
	err                 error
}

func (r *fakeQueryReader) Query(_ context.Context, query string) (rules.QueryResult, error) {
	r.query = query
	return r.result, r.err
}
func (r *fakeQueryReader) Schema(context.Context) (store.DatabaseSchema, error) {
	r.schemaCalls++
	return r.schema, r.err
}
func (r *fakeQueryReader) Close() error { r.closed++; return nil }

func executeQueryFixture(t *testing.T, reader *fakeQueryReader, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("home", "fake-home", "fake home")
	var output bytes.Buffer
	root.SetOut(&output)
	root.AddCommand(newQueryCommand(func(_ context.Context, home string) (queryReader, error) {
		if home != "fake-home" {
			t.Fatal("query lost home")
		}
		return reader, nil
	}))
	root.SetArgs(append([]string{"query"}, args...))
	err := root.Execute()
	return output.String(), err
}

func TestQueryCommandSupportsRowsSchemaAndStructuredErrors(t *testing.T) {
	reader := &fakeQueryReader{result: rules.QueryResult{Columns: []string{"value"}, Rows: [][]any{{int64(42)}}, Truncated: true}}
	output, err := executeQueryFixture(t, reader, "SELECT 42", "--json")
	if err != nil || reader.query != "SELECT 42" || reader.closed != 1 || !strings.Contains(output, `"truncated":true`) || !json.Valid([]byte(output)) {
		t.Fatalf("query JSON: %s %v %+v", output, err, reader)
	}
	reader = &fakeQueryReader{schema: store.DatabaseSchema{Tables: []store.TableSchema{{Name: "fake", Kind: "table", Description: "Fake table.", Columns: []store.ColumnSchema{{Name: "id", Type: "TEXT", Description: "Fake identity."}}}}}}
	output, err = executeQueryFixture(t, reader, "--schema", "--json")
	if err != nil || reader.schemaCalls != 1 || reader.query != "" || reader.closed != 1 || !strings.Contains(output, `"description":"Fake identity."`) {
		t.Fatalf("schema JSON: %s %v", output, err)
	}
	reader = &fakeQueryReader{err: context.DeadlineExceeded}
	output, err = executeQueryFixture(t, reader, "SELECT 1", "--json")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(output, `"code":"query_timeout"`) || !json.Valid([]byte(output)) || reader.closed != 1 {
		t.Fatalf("query error not structured or wrapped: %s %v", output, err)
	}
	for _, args := range [][]string{{}, {"SELECT 1", "SELECT 2"}, {"--schema", "SELECT 1"}} {
		reader = &fakeQueryReader{}
		if _, err := executeQueryFixture(t, reader, args...); err == nil || reader.closed != 0 {
			t.Fatalf("invalid query usage opened data: %v", args)
		}
	}
}

func TestQueryTextEscapesCellsAndReportsTruncation(t *testing.T) {
	reader := &fakeQueryReader{result: rules.QueryResult{Columns: []string{"label"}, Rows: [][]any{{"line\nvalue"}}, Truncated: true}}
	output, err := executeQueryFixture(t, reader, "SELECT 1")
	if err != nil || !strings.Contains(output, `"line\nvalue"`) || !strings.Contains(output, "已截断") {
		t.Fatalf("query text: %q %v", output, err)
	}
}

func TestQueryRootReadsLiveAndStoppedDataAndNeverCreatesMissingHome(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db"), DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RegisterCollector(context.Background(), store.CollectorRecord{ID: "fake-query", Hostname: "虚构主机", FirstSeen: 1, LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	for _, stopped := range []bool{false, true} {
		if stopped {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		code := execute([]string{"query", "SELECT id,hostname FROM collectors", "--home", home, "--json"}, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "fake-query") {
			t.Fatalf("query stopped=%t: code=%d %s %s", stopped, code, stdout.String(), stderr.String())
		}
		stdout.Reset()
		stderr.Reset()
		code = execute([]string{"query", "--schema", "--home", home, "--json"}, &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String(), "raw_watermarks") || !json.Valid(stdout.Bytes()) {
			t.Fatalf("real schema command: %d %s %s", code, stdout.String(), stderr.String())
		}
	}
	missing := filepath.Join(t.TempDir(), "missing-home")
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"query", "SELECT 1", "--home", missing, "--json"}, &stdout, &stderr); code != 1 || !json.Valid(stdout.Bytes()) || !strings.Contains(stdout.String(), "尚无监控数据") {
		t.Fatalf("missing query data: %d %s %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only query created missing home")
	}
}
