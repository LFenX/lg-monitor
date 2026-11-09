package collector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type operationProgramFixture struct{}

func (operationProgramFixture) Path() (string, error) { return "fake-protected-program", nil }

type operationPathFixture struct {
	err     error
	checked []string
}

func (p *operationPathFixture) Check(path string) error {
	p.checked = append(p.checked, path)
	return p.err
}
func (p *operationPathFixture) ProtectTree(string, bool) error {
	return errors.New("policy reader must never change ACLs")
}
func TestOperationPolicyRequiresServiceIdentityAndCurrentProtectedFiles(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "settings.yaml")
	if err := os.WriteFile(path, []byte("remoteOperations: {enabled: true, services: [{id: restart-demo, service: FakeService}]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := &operationPathFixture{}
	reader := DiskOperationPolicy{Home: home, Identity: "LocalSystem", Paths: paths, Program: operationProgramFixture{}}
	if policy, err := reader.ReadOperationPolicy(context.Background()); err != nil || !policy.Enabled || len(paths.checked) != 5 {
		t.Fatal(policy, err, paths.checked)
	}
	reader.Identity = "CurrentUser"
	if _, err := reader.ReadOperationPolicy(context.Background()); err == nil {
		t.Fatal("non-service identity enabled operations")
	}
	reader.Identity = "LocalSystem"
	paths.err = errors.New("fake writable directory")
	if _, err := reader.ReadOperationPolicy(context.Background()); err == nil {
		t.Fatal("unsafe path enabled operations")
	}
	paths.err = nil
	if err := os.WriteFile(path, []byte("remoteOperations: {enabled: true, services: [{id: bad, service: '*'}]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadOperationPolicy(context.Background()); err == nil {
		t.Fatal("invalid changed policy retained old grant")
	}
}
