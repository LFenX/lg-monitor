package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPEntryEnforcesHTTPHostOriginAndHeaderAuthentication(t *testing.T) {
	server := serverFixture(t, nil)
	for _, test := range []struct {
		host, origin, token, method string
		want                        int
	}{
		{"127.0.0.1:7444", "", "", "POST", 401},
		{"127.0.0.1:7444", "", "fake-invalid", "POST", 401},
		{"evil.example.test", "", "fake-read", "POST", 403},
		{"127.0.0.1:7444", "https://evil.example.test", "fake-read", "POST", 403},
		{"127.0.0.1:7444", "", "fake-read", "GET", 405},
	} {
		request := httptest.NewRequest(test.method, "http://"+test.host+"/mcp?token=fake-admin", strings.NewReader("{}"))
		request.RemoteAddr = "127.0.0.1:4321"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		if test.origin != "" {
			request.Header.Set("Origin", test.origin)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != test.want || response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("entry status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
		}
	}
}

func TestMCPHTTPProxyDoesNotFollowRedirectOrExposeRawErrors(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { redirected.Add(1); w.Write([]byte("{}")) }))
	defer target.Close()
	for _, test := range []struct {
		status   int
		body     string
		redirect bool
	}{
		{302, "", true},
		{500, "FAKE_PRIVATE_RAW_RESPONSE", false},
		{200, "FAKE_PRIVATE_NOT_JSON", false},
	} {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if test.redirect {
				w.Header().Set("Location", target.URL)
			}
			w.WriteHeader(test.status)
			io.WriteString(w, test.body)
		}))
		client, err := NewMCPHTTPClient(MCPHTTPClientOptions{BaseURL: source.URL, Token: "fake-read"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Call(context.Background(), ListCollectors, Request{})
		source.Close()
		if err == nil || strings.Contains(err.Error(), "FAKE_PRIVATE") {
			t.Fatalf("raw response exposed: %v", err)
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("proxy followed a redirect with credentials")
	}
}

func TestMCPResultsPreserveStructuredAPIErrorIssues(t *testing.T) {
	caller := MCPCallerFunc(func(context.Context, Operation, Request) (MCPResponse, error) {
		return MCPResponse{Status: 422, Body: json.RawMessage(`{"error":{"code":"invalid_config","message":"配置错误","issues":[{"file":"rules.yaml","line":4,"message":"目标不存在"}]}}`)}, nil
	})
	server, err := NewMCPServer(MCPOptions{Caller: caller})
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "fake-client", Version: "fake"}, nil)
	session, err := client.Connect(context.Background(), right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	response, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_config", Arguments: map[string]any{"kind": "rules"}})
	if err != nil || !response.IsError || len(response.Content) == 0 || !strings.Contains(response.Content[0].(*mcp.TextContent).Text, "rules.yaml") {
		t.Fatalf("result=%+v err=%v", response, err)
	}
}
