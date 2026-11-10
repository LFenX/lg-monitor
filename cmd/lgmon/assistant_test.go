package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
)

func runAssistantCLI(t *testing.T, store *memorySecrets, home, input string, args ...string) (string, error) {
	t.Helper()
	command := newAssistantCommand(func(string, *slog.Logger) (secrets.Store, error) { return store, nil })
	command.PersistentFlags().String("home", home, "test home")
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetIn(strings.NewReader(input))
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}

func TestAssistantSetKeepsOtherSettingsAndHidesTheKey(t *testing.T) {
	const key = "FAKE-provider-key-for-cli-test"
	home := t.TempDir()
	path := filepath.Join(home, "config", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# keep me\npublicBaseUrl: http://127.0.0.1:7444\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &memorySecrets{values: map[string][]byte{}}
	output, err := runAssistantCLI(t, store, home, key+"\r\n", "set", "--provider", "deepseek", "--json")
	if err != nil || strings.Contains(output, key) {
		t.Fatalf("assistant set = %q, %v", output, err)
	}
	var result struct {
		OK       bool   `json:"ok"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		KeyRef   string `json:"keyRef"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || !result.OK || result.Provider != "deepseek" || result.Model != "deepseek-chat" || result.KeyRef != "assistant-deepseek-key" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if string(store.values["assistant-deepseek-key"]) != key {
		t.Fatalf("key stored under %v", store.values)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.ParseSettings(data)
	if err != nil || settings.Assistant.Provider != "deepseek" || !strings.Contains(string(data), "# keep me") || !strings.Contains(string(data), "publicBaseUrl") {
		t.Fatalf("settings.yaml = %s, %v", data, err)
	}

	// A local runtime needs no key, so nothing is read from stdin or written to the vault.
	if _, err := runAssistantCLI(t, store, home, "", "set", "--provider", "ollama", "--model", "qwen3:8b", "--json"); err != nil {
		t.Fatal(err)
	}
	if len(store.values) != 1 {
		t.Fatalf("ollama wrote a key: %v", store.values)
	}
	if _, err := runAssistantCLI(t, store, home, "", "set", "--provider", "nobody", "--json"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	data, _ = os.ReadFile(path)
	if settings, err := config.ParseSettings(data); err != nil || settings.Assistant.Provider != "ollama" || settings.Assistant.Model != "qwen3:8b" {
		t.Fatalf("failed set changed settings: %s, %v", data, err)
	}
}

func TestAssistantProvidersListsTheCatalog(t *testing.T) {
	output, err := runAssistantCLI(t, &memorySecrets{values: map[string][]byte{}}, t.TempDir(), "", "providers", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var providers []config.AssistantProvider
	if err := json.Unmarshal([]byte(output), &providers); err != nil || len(providers) != len(config.AssistantProviders) {
		t.Fatalf("providers = %q, %v", output, err)
	}
	for _, id := range []string{"anthropic", "openai", "deepseek", "qwen", "ollama", "custom"} {
		if _, ok := config.FindAssistantProvider(id); !ok {
			t.Fatalf("catalog misses %s", id)
		}
	}
}
