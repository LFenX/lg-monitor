package app

import (
	"os"
	"runtime"
	"sync"
)

type collectorResourceLease interface{ Close() error }
type collectorResourceSource interface {
	Acquire() (collectorResourceLease, error)
}
type collectorResourceRelease func() error

func (f collectorResourceRelease) Close() error { return f() }

type nativeCollectorResources struct{}

var collectorResourceState struct {
	sync.Mutex
	users   int
	release func() error
}

func (nativeCollectorResources) Acquire() (collectorResourceLease, error) {
	collectorResourceState.Lock()
	defer collectorResourceState.Unlock()
	if collectorResourceState.users == 0 {
		release, err := applyCollectorResidentBudget()
		if err != nil {
			return nil, err
		}
		previous := 0
		if os.Getenv("GOMAXPROCS") == "" {
			previous = runtime.GOMAXPROCS(min(runtime.GOMAXPROCS(0), 2))
		}
		collectorResourceState.release = func() error {
			err := release()
			if previous > 0 {
				runtime.GOMAXPROCS(previous)
			}
			return err
		}
	}
	collectorResourceState.users++
	var once sync.Once
	return collectorResourceRelease(func() error {
		var err error
		once.Do(func() {
			collectorResourceState.Lock()
			defer collectorResourceState.Unlock()
			collectorResourceState.users--
			if collectorResourceState.users == 0 {
				err = collectorResourceState.release()
				collectorResourceState.release = nil
			}
		})
		return err
	}), nil
}
