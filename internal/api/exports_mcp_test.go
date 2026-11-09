package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPExportMetricsIsReadOnlyAndReturnsSameStructuredSnapshot(t *testing.T) {
	want := exportFixture()
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server, err := NewMCPServer(MCPOptions{Caller: MCPCallerFunc(func(_ context.Context, op Operation, r Request) (MCPResponse, error) {
		if op != ExportMetrics || len(r.Body) != 0 {
			t.Fatal("MCP export invoked a write or send", op, string(r.Body))
		}
		calls++
		return MCPResponse{Status: 200, Body: body}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "fake-export-reader"}, nil)
	session, err := client.Connect(context.Background(), right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "export_metrics" {
			found = true
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatal("export tool is not marked read-only", tool)
			}
		}
	}
	if !found {
		t.Fatal("export_metrics is not discoverable")
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "export_metrics", Arguments: map[string]any{}})
	if err != nil || result.IsError || len(result.Content) != 1 || calls != 1 {
		t.Fatal(result, err, calls)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &got); err != nil || got["ts"] != float64(want.TS) {
		t.Fatal("MCP changed snapshot", got, err)
	}
}
