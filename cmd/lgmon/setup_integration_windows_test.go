//go:build integration && windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/transport"
	"github.com/LFenX/lg-monitor/internal/winsvc"
)

func TestNativeSetupPairingPreservesIdentityAndRejectsWrongPinBeforeService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	monitor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(monitor, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(monitor, "config", "settings.yaml"), []byte("httpListen: 127.0.0.1:0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	startTeamCLIMonitor(t, monitor)
	data, err := nativeTeamCLI(t, monitor, "", "pair", "new", "--json")
	if err != nil {
		t.Fatal("temporary pairing code", err)
	}
	var pairing pairOutput
	if err := json.Unmarshal(data, &pairing); err != nil || pairing.Code == "" || pairing.Fingerprint == "" {
		t.Fatal("invalid temporary pairing response", err)
	}
	endpoint, err := (transport.FileState{Home: monitor}).Load(ctx, "endpoint")
	if err != nil {
		t.Fatal(err)
	}
	var address struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(endpoint, &address); err != nil || address.Address == "" {
		t.Fatal("temporary endpoint", err)
	}
	f := newSetupCommandFixture(t)
	paths := secrets.NativePathPolicy()
	files := setupFileStore{Source: f.source, Paths: paths, Guard: setupGuardPath}
	installer, err := winsvc.NewSetup(winsvc.SetupOptions{Service: f.service, Files: files, Pairer: nativeSetupPairer{now: time.Now}, Binding: func(context.Context) (winsvc.Definition, bool, error) {
		return f.service.definition, f.service.installed, nil
	}, Lock: func(context.Context) (func(), error) { return func() {}, nil }, Now: time.Now, Source: f.source, Program: f.service.program, Version: "fake-integration"})
	if err != nil {
		t.Fatal(err)
	}
	request := winsvc.SetupRequest{Role: "collector", Home: f.home, Monitor: address.Address, Code: pairing.Code, Fingerprint: strings.Repeat("0", 16), Confirmed: true}
	if _, err := installer.Install(ctx, request); err == nil || len(f.service.calls) != 0 || strings.Contains(err.Error(), pairing.Code) {
		t.Fatal("wrong pin reached service or leaked code", err, f.service.calls)
	}
	request.Fingerprint = pairing.Fingerprint
	result, err := installer.Install(ctx, request)
	if err != nil || result.Status != "ok" || !f.service.installed || f.service.definition.Monitor != address.Address {
		t.Fatal("real isolated TLS pairing failed", err)
	}
	identityFile := filepath.Join(f.home, "data", "pki", "collector.json")
	identity, err := os.ReadFile(identityFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Remove(ctx, f.home, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.service.program); err != nil {
		t.Fatal(err)
	}
	request.Code, request.Fingerprint = "", ""
	result, err = installer.Install(ctx, request)
	if err != nil || result.SetupPlan.PairingRequired || result.Status != "ok" {
		t.Fatal("existing pairing was not reused", err)
	}
	after, err := os.ReadFile(identityFile)
	if err != nil || string(after) != string(identity) {
		t.Fatal("reinstallation changed pairing identity", err)
	}
	if _, err := (nativeSetupPairer{now: time.Now}).Paired(ctx, f.home, "other-monitor.invalid:7443"); err == nil {
		t.Fatal("existing identity was assigned to another monitor")
	}
	if _, err := installer.Remove(ctx, f.home, true); err != nil {
		t.Fatal(err)
	}
}
