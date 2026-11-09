package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fixtureCollectorResources struct {
	entered, closed    int
	enterErr, closeErr error
}

func (f *fixtureCollectorResources) Acquire() (collectorResourceLease, error) {
	f.entered++
	if f.enterErr != nil {
		return nil, f.enterErr
	}
	return collectorResourceRelease(func() error { f.closed++; return f.closeErr }), nil
}

func TestCollectorResourcesCoverLifetimeAndRestoreOnStartupFailure(t *testing.T) {
	for _, failAcquire := range []bool{false, true} {
		resource := &fixtureCollectorResources{closeErr: errors.New("fixture restore")}
		if failAcquire {
			resource.enterErr = errors.New("fixture quota")
		}
		err := RunLocal(context.Background(), LocalOptions{Home: t.TempDir(), Role: "collector", collectorResources: resource})
		if resource.entered != 1 {
			t.Fatal("collector did not acquire its own process budget")
		}
		if failAcquire {
			if !errors.Is(err, resource.enterErr) || resource.closed != 0 {
				t.Fatal(resource, err)
			}
		} else if !errors.Is(err, resource.closeErr) || resource.closed != 1 {
			t.Fatal("startup failure lost resource release", resource, err)
		}
	}
	for _, role := range []string{"collector", "monitor", "both"} {
		resource := &fixtureCollectorResources{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := RunLocal(ctx, LocalOptions{Role: role, collectorResources: resource}); err != nil || resource.entered != 0 {
			t.Fatal("cancelled or non-collector role changed process resources", role, err)
		}
	}
	for _, role := range []string{"monitor", "both"} {
		home := t.TempDir()
		if err := os.Mkdir(filepath.Join(home, "config"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config", "settings.yaml"), []byte("unknownSetting: true"), 0600); err != nil {
			t.Fatal(err)
		}
		resource := &fixtureCollectorResources{}
		if err := RunLocal(context.Background(), LocalOptions{Home: home, Role: role, collectorResources: resource}); err == nil || resource.entered != 0 {
			t.Fatal("non-collector role acquired dedicated process budget", role, err)
		}
	}
}
