package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestHTTPReadOnlySQLReturnsDataSchemaAndTruncation(t *testing.T) {
	f := newAPIFixture(t)
	f.samples(t)
	response := f.request(t, "POST", "/api/v1/query", "read", `{"sql":"SELECT collector_id,cpu FROM system_metrics ORDER BY ts"}`, 200)
	var result rules.QueryResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "fake-host" || result.Truncated {
		t.Fatalf("query result: %+v %v", result, err)
	}
	response = f.request(t, "GET", "/api/v1/schema", "read", "", 200)
	var schema store.DatabaseSchema
	if err := json.Unmarshal(response.Body.Bytes(), &schema); err != nil || len(schema.Tables) != 31 {
		t.Fatalf("HTTP schema: %+v %v", schema, err)
	}
	for _, table := range schema.Tables {
		if table.Name == "api_tokens" {
			t.Fatal("schema exposed auth.db")
		}
		if table.Description == "" {
			t.Fatal("schema missing migration description")
		}
	}
	response = f.request(t, "POST", "/api/v1/query", "read", `{"sql":"WITH RECURSIVE x(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM x WHERE n<10001) SELECT n FROM x"}`, 200)
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Rows) != 10000 || !result.Truncated {
		t.Fatalf("HTTP query cap: rows=%d truncated=%t %v", len(result.Rows), result.Truncated, err)
	}
	for _, query := range []string{"SELECT * FROM api_tokens", "SELECT 1; DELETE FROM collectors", "WITH x AS(SELECT 1) DELETE FROM collectors RETURNING id", "ATTACH DATABASE ':memory:' AS extra", "SELECT readfile('unused')", "SELECT 1e999"} {
		payload, _ := json.Marshal(map[string]string{"sql": query})
		f.request(t, "POST", "/api/v1/query", "read", string(payload), 400)
	}
	f.request(t, "POST", "/api/v1/query", "read", `{"sql":"SELECT 1","extra":true}`, 400)
}

func TestHTTPQueryCancellationReturnsStructuredTimeoutAndReleasesSQLConnection(t *testing.T) {
	f := newAPIFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", "http://127.0.0.1:7444/api/v1/query", strings.NewReader(`{"sql":"WITH RECURSIVE x(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM x) SELECT sum(n) FROM x"}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer fake-read")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != 408 || !strings.Contains(w.Body.String(), "query_timeout") || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("HTTP SQL timeout: %d %s", w.Code, w.Body.String())
	}
	f.request(t, "POST", "/api/v1/query", "read", `{"sql":"SELECT 42"}`, 200)
}
