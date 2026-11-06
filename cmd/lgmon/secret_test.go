package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/secrets"
)

type memorySecrets struct {
	values map[string][]byte
	gets   int
}

func (s *memorySecrets) Set(_ context.Context, name string, value []byte) error {
	s.values[name] = append([]byte(nil), value...)
	return nil
}
func (s *memorySecrets) Get(_ context.Context, name string) ([]byte, error) {
	s.gets++
	return append([]byte(nil), s.values[name]...), nil
}
func (s *memorySecrets) List(context.Context) ([]string, error) {
	names := make([]string, 0, len(s.values))
	for name := range s.values {
		names = append(names, name)
	}
	return names, nil
}

func TestSecretCommandsNeverPrintValues(t *testing.T) {
	const value = "fake-private-app-secret-CLI-test"
	store := &memorySecrets{values: make(map[string][]byte)}
	factory := func(string, *slog.Logger) (secrets.Store, error) { return store, nil }
	for _, jsonOutput := range []bool{false, true} {
		command := newSecretCommand(factory)
		command.PersistentFlags().String("home", t.TempDir(), "test home")
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetIn(strings.NewReader(value + "\r\n"))
		args := []string{"set", "demo"}
		if jsonOutput {
			args = append(args, "--json")
		}
		command.SetArgs(args)
		if err := command.Execute(); err != nil || string(store.values["demo"]) != value || strings.Contains(output.String(), value) {
			t.Fatalf("secret set failed or echoed input: %v", err)
		}
		command = newSecretCommand(factory)
		command.PersistentFlags().String("home", t.TempDir(), "test home")
		output.Reset()
		command.SetOut(&output)
		command.SetErr(&output)
		args = []string{"list"}
		if jsonOutput {
			args = append(args, "--json")
		}
		command.SetArgs(args)
		if err := command.Execute(); err != nil || strings.Contains(output.String(), value) || store.gets != 0 {
			t.Fatalf("secret list exposed or decrypted a value: %v", err)
		}
		if jsonOutput {
			var result struct {
				Names []string `json:"names"`
			}
			if err := json.Unmarshal(output.Bytes(), &result); err != nil || !reflect.DeepEqual(result.Names, []string{"demo"}) {
				t.Fatalf("list JSON = %q, %v", output.String(), err)
			}
		}
	}
}

func TestSecretHasNoPlaintextReadCommand(t *testing.T) {
	command := newSecretCommand(func(string, *slog.Logger) (secrets.Store, error) { return &memorySecrets{}, nil })
	for _, child := range command.Commands() {
		if child.Name() != "set" && child.Name() != "list" {
			t.Fatalf("unexpected secret command: %s", child.Name())
		}
	}
}
