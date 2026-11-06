package winsvc

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

type testPathPolicy struct {
	checks    []string
	protected string
	readable  bool
	fail      string
	err       error
}

func (p *testPathPolicy) Check(path string) error {
	p.checks = append(p.checks, path)
	if path == p.fail {
		return p.err
	}
	return nil
}
func (p *testPathPolicy) ProtectTree(path string, readable bool) error {
	p.protected, p.readable = path, readable
	return nil
}

func TestServiceChecksProgramAndDirectoryBeforeProtectingHome(t *testing.T) {
	executable, home := filepath.Join(t.TempDir(), "lgmon.exe"), t.TempDir()
	for _, failed := range []string{executable, filepath.Dir(executable), ""} {
		policy := &testPathPolicy{fail: failed, err: errors.New("controlled unsafe permissions")}
		err := prepareServicePaths(policy, executable, home)
		if failed != "" {
			if !errors.Is(err, policy.err) || policy.protected != "" {
				t.Fatalf("unsafe program reached home setup: %v %+v", err, policy)
			}
		} else if err != nil || policy.protected != home || policy.readable || !reflect.DeepEqual(policy.checks, []string{executable, filepath.Dir(executable)}) {
			t.Fatalf("service protection sequence: %v %+v", err, policy)
		}
	}
}
