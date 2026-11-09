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
	"golang.org/x/sys/windows"
)

type setupAuditedNativePaths struct {
	secrets.PathPolicy
	protected int
}

func (p *setupAuditedNativePaths) ProtectTree(path string, readable bool) error {
	p.protected++
	return p.PathPolicy.ProtectTree(path, readable)
}

func TestNativeSetupDoesNotBlessWritableExistingConfiguration(t *testing.T) {
	f := newSetupCommandFixture(t)
	policy := secrets.NativePathPolicy()
	if err := os.MkdirAll(filepath.Join(f.home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(f.home, "config", "settings.yaml")
	if err := os.WriteFile(settings, []byte("httpListen: 127.0.0.1:7444\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{f.home, filepath.Dir(f.service.program)} {
		if err := policy.ProtectTree(path, false); err != nil {
			t.Fatal(err)
		}
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;GW;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(settings, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if err := policy.Check(f.home); err != nil {
		t.Fatal("test root is not protected", err)
	}
	observed := &setupAuditedNativePaths{PathPolicy: policy}
	installer, err := winsvc.NewSetup(winsvc.SetupOptions{Service: f.service, Files: setupFileStore{Source: f.source, Paths: observed, Guard: setupGuardPath}, Pairer: f.service, Binding: func(context.Context) (winsvc.Definition, bool, error) {
		return winsvc.Definition{}, false, nil
	}, Lock: func(context.Context) (func(), error) { return func() {}, nil }, Now: time.Now, Source: f.source, Program: f.service.program, Version: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Install(context.Background(), winsvc.SetupRequest{Role: "both", Home: f.home, Listen: "127.0.0.1:7443", Confirmed: true}); err == nil || observed.protected != 0 || len(f.service.calls) != 0 {
		t.Fatal("unsafe configuration reached SYSTEM preparation", err, observed.protected, f.service.calls)
	}
	if err := policy.Check(settings); err == nil {
		t.Fatal("unsafe configuration was silently converted to trusted input")
	}
}

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
	if err := paths.ProtectTree(filepath.Dir(f.service.program), true); err != nil {
		t.Fatal(err)
	}
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
	// The fake SCM does not run nativeFiles.Prepare, which seals the generated identity before SYSTEM starts.
	if err := paths.ProtectTree(f.home, false); err != nil {
		t.Fatal(err)
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
