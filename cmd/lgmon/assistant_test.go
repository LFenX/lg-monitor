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

func TestAssistantSetSavesBothKeysAndKeepsOtherSettings(t *testing.T) {
	const deepseek, dashscope = "FAKE-deepseek-key-for-cli-test", "FAKE-dashscope-key-for-cli-test"
	home := t.TempDir()
	path := filepath.Join(home, "config", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// An older file that still names another vendor loads, and saving drops the retired fields.
	if err := os.WriteFile(path, []byte("# keep me\npublicBaseUrl: http://127.0.0.1:7444\nassistant:\n  provider: anthropic\n  model: claude-opus-5-5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &memorySecrets{values: map[string][]byte{}}
	output, err := runAssistantCLI(t, store, home, deepseek+"\r\n"+dashscope+"\r\n", "set", "--region", "singapore", "--json")
	if err != nil || strings.Contains(output, deepseek) || strings.Contains(output, dashscope) {
		t.Fatalf("assistant set = %q, %v", output, err)
	}
	var result struct {
		OK             bool   `json:"ok"`
		Model          string `json:"model"`
		Region         string `json:"region"`
		EmbeddingModel string `json:"embeddingModel"`
		DeepSeekKey    bool   `json:"deepseekKey"`
		DashScopeKey   bool   `json:"dashscopeKey"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || !result.OK || result.Model != config.DefaultDeepSeekModel || result.Region != "singapore" || result.EmbeddingModel != config.DefaultEmbeddingModel || !result.DeepSeekKey || !result.DashScopeKey {
		t.Fatalf("result = %+v, %v (%s)", result, err, output)
	}
	if string(store.values[config.DeepSeekKeyRef]) != deepseek || string(store.values[config.DashScopeKeyRef]) != dashscope {
		t.Fatalf("keys stored under %v", store.values)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.ParseSettings(data)
	if err != nil || settings.Assistant.Region != "singapore" || settings.Assistant.Provider != "" || strings.Contains(string(data), "claude") || !strings.Contains(string(data), "# keep me") || !strings.Contains(string(data), "publicBaseUrl") {
		t.Fatalf("settings.yaml = %s, %v", data, err)
	}

	// Changing only a model reads no key; a blank line keeps the saved key.
	if _, err := runAssistantCLI(t, store, home, "", "set", "--model", "deepseek-reasoner", "--key", "none", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := runAssistantCLI(t, store, home, "\r\n", "set", "--key", "deepseek", "--json"); err == nil {
		t.Fatal("an empty key for a single requested key was accepted")
	}
	if string(store.values[config.DeepSeekKeyRef]) != deepseek {
		t.Fatal("a blank line replaced the saved key")
	}
	if _, err := runAssistantCLI(t, store, home, "", "set", "--region", "mars", "--key", "none", "--json"); err == nil {
		t.Fatal("unknown region accepted")
	}
	data, _ = os.ReadFile(path)
	if settings, err := config.ParseSettings(data); err != nil || settings.Assistant.Model != "deepseek-reasoner" || settings.Assistant.Region != "singapore" {
		t.Fatalf("failed set changed settings: %s, %v", data, err)
	}
}
