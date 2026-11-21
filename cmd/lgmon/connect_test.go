// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type fakeConnectElevation struct {
	requests []connectRequest
	result   connectOutput
	err      error
}

func (f *fakeConnectElevation) Connect(_ context.Context, request connectRequest) (connectOutput, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

type connectStore struct {
	fakeTokenStore
	err error
}

func (s *connectStore) AddAPIToken(ctx context.Context, hash []byte, name, scope string) (store.APIToken, error) {
	if s.err != nil {
		return store.APIToken{}, s.err
	}
	row, err := s.fakeTokenStore.AddAPIToken(ctx, hash, name, scope)
	row.ID = "fake-connect-id"
	return row, err
}

func executeConnect(t *testing.T, tokens tokenDependencies, agent agentDependencies, home string, elevation connectElevation, args ...string) (connectOutput, error) {
	t.Helper()
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("home", home, "test home")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.AddCommand(newConnectCommandWithElevation(tokens, agent, func() time.Time { return time.Unix(1234, 0) }, elevation))
	root.SetArgs(append([]string{"connect", "--url", "http://127.0.0.1:17444", "--json"}, args...))
	err := root.ExecuteContext(context.Background())
	var result connectOutput
	if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
		t.Fatalf("invalid connect JSON: %v", decodeErr)
	}
	return result, err
}

func TestConnectCreatesConfigTokenAndBoardInstructionsWithoutElevation(t *testing.T) {
	agent, local, _ := agentFixture(t)
	backend := &connectStore{}
	elevation := &fakeConnectElevation{err: errors.New("must not elevate")}
	result, err := executeConnect(t, tokenDependencies{Administrator: func() (bool, error) { return true, nil }, Entropy: bytes.NewReader(make([]byte, 32)), Open: func(context.Context, string) (tokenStore, error) { return backend, nil }}, agent, local.Home, elevation, "--elevate")
	if err != nil || result.Status != "ok" || result.Scope != api.ScopeConfig || result.TokenID == "" || result.Token == "" || len(elevation.requests) != 0 || backend.closed != 1 {
		t.Fatalf("connect failed or elevated unexpectedly: %v", err)
	}
	if !strings.Contains(result.FirstWords, "create_board") || !strings.Contains(result.FirstWords, "不要让我手动建") || result.HTTP != "http://127.0.0.1:17444/mcp" || result.Tools != "all" {
		t.Fatal("connect omitted the end-to-end workflow or the full tool set")
	}
	hash := api.TokenHash(result.Token)
	if !bytes.Equal(backend.hash, hash[:]) {
		t.Fatal("store did not receive just the token digest")
	}
}

func TestConnectAllToolsProvidesFeishuMCPWithoutExpandingScope(t *testing.T) {
	agent, local, _ := agentFixture(t)
	backend := &connectStore{}
	tokens := tokenDependencies{Administrator: func() (bool, error) { return true, nil }, Entropy: bytes.NewReader(make([]byte, 32)), Open: func(context.Context, string) (tokenStore, error) { return backend, nil }}
	result, err := executeConnect(t, tokens, agent, local.Home, nil, "--tools", "all")
	if err != nil || result.Tools != "all" || result.Scope != api.ScopeConfig || result.HTTP != "http://127.0.0.1:17444/mcp" {
		t.Fatalf("full tool set changed permissions or endpoint: %v", err)
	}
	server := result.MCPServers["lgmon"].(map[string]any)
	arguments := server["args"].([]any)
	if arguments[2] != "all" {
		t.Fatal("generated stdio configuration kept the essential tool set")
	}
	tokens.Open = func(context.Context, string) (tokenStore, error) {
		t.Fatal("invalid tools reached storage")
		return nil, nil
	}
	result, err = executeConnect(t, tokens, agent, local.Home, nil, "--tools", "invalid", "--elevate")
	if err == nil || result.Token != "" {
		t.Fatal("an invalid tool set issued credentials")
	}
}

func TestConnectElevatesOnceOnlyForPermissionFailures(t *testing.T) {
	for _, reason := range []string{"administrator", "protected-home"} {
		t.Run(reason, func(t *testing.T) {
			agent, local, _ := agentFixture(t)
			elevation := &fakeConnectElevation{result: connectOutput{Status: "ok", Scope: api.ScopeConfig, Token: "FAKE_PIPE_TOKEN"}}
			tokens := tokenDependencies{Administrator: func() (bool, error) { return reason != "administrator", nil }, Entropy: bytes.NewReader(make([]byte, 32)), Open: func(context.Context, string) (tokenStore, error) {
				return nil, fmt.Errorf("protected: %w", os.ErrPermission)
			}}
			result, err := executeConnect(t, tokens, agent, local.Home, elevation, "--elevate")
			if err != nil || result.Status != "ok" || len(elevation.requests) != 1 {
				t.Fatalf("permission recovery failed: %v", err)
			}
			request := elevation.requests[0]
			if request.Executable != local.Executable || request.Home != local.Home || request.Scope != "config" || request.URL != "http://127.0.0.1:17444" {
				t.Fatal("elevation did not preserve this exact install and scope")
			}
		})
	}
}

func TestConnectManualModeAndCancellationDoNotRetryOrLeakTokens(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		agent, local, _ := agentFixture(t)
		elevation := &fakeConnectElevation{err: errors.New("已取消 Windows 管理员授权")}
		tokens := tokenDependencies{Administrator: func() (bool, error) { return false, nil }, Open: func(context.Context, string) (tokenStore, error) {
			t.Fatal("unapproved connect opened storage")
			return nil, nil
		}}
		args := []string{}
		if automatic {
			args = append(args, "--elevate")
		}
		result, err := executeConnect(t, tokens, agent, local.Home, elevation, args...)
		if err == nil || result.Status != "error" || result.Token != "" || len(elevation.requests) != map[bool]int{false: 0, true: 1}[automatic] {
			t.Fatal("manual mode or cancellation issued a token or retried")
		}
	}
}

func TestConnectDoesNotElevateForOtherErrorsOrAfterAnIssuanceAttempt(t *testing.T) {
	for _, step := range []string{"identity", "open", "write"} {
		t.Run(step, func(t *testing.T) {
			agent, local, _ := agentFixture(t)
			elevation := &fakeConnectElevation{}
			backend := &connectStore{err: fmt.Errorf("write failed: %w", os.ErrPermission)}
			tokens := tokenDependencies{Entropy: bytes.NewReader(make([]byte, 32)), Administrator: func() (bool, error) {
				if step == "identity" {
					return false, errors.New("identity unavailable")
				}
				return true, nil
			}, Open: func(context.Context, string) (tokenStore, error) {
				if step == "open" {
					return nil, errors.New("database locked")
				}
				return backend, nil
			}}
			result, err := executeConnect(t, tokens, agent, local.Home, elevation, "--elevate")
			if err == nil || result.Token != "" || len(elevation.requests) != 0 {
				t.Fatal("a non-permission or uncertain write failure triggered elevation")
			}
			if step == "write" && backend.closed != 1 {
				t.Fatal("failed issuance retained a storage handle")
			}
		})
	}
}

func TestElevatedConnectHelperRejectsOtherExecutableAndAdminScope(t *testing.T) {
	agent, local, _ := agentFixture(t)
	tokens := tokenDependencies{Open: func(context.Context, string) (tokenStore, error) {
		t.Fatal("invalid helper request reached storage")
		return nil, nil
	}}
	for _, request := range []connectRequest{{Executable: local.Executable + "-other", Home: local.Home, Scope: "config"}, {Executable: local.Executable, Home: local.Home, Scope: "admin"}} {
		result := elevatedConnectResult(context.Background(), request, tokens, agent, func() time.Time { return time.Unix(1234, 0) })
		if result.Status != "error" || result.Token != "" {
			t.Fatal("helper accepted an unapproved executable or scope")
		}
	}
}
