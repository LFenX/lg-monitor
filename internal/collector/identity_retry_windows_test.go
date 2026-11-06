package collector

import (
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type identityReaderFunc func(string) ([]byte, error)

func (f identityReaderFunc) ReadFile(path string) ([]byte, error) { return f(path) }

type identityWaits struct{ delays []time.Duration }

func (w *identityWaits) Wait(delay time.Duration) { w.delays = append(w.delays, delay) }

func TestIdentityReadRetriesOnlySharingViolations(t *testing.T) {
	const id = "00112233-4455-4677-8899-aabbccddeeff"
	for _, test := range []struct {
		name     string
		failure  error
		failures int
		calls    int
		waits    int
	}{
		{"recovers", windows.ERROR_SHARING_VIOLATION, 2, 3, 2},
		{"bounded", windows.ERROR_SHARING_VIOLATION, 99, 10, 9},
		{"permission denied", windows.ERROR_ACCESS_DENIED, 2, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			waiter := &identityWaits{}
			store := FileIDStore{Home: t.TempDir(), Waiter: waiter, Reader: identityReaderFunc(func(path string) ([]byte, error) {
				calls++
				if calls <= test.failures {
					return nil, &os.PathError{Op: "open", Path: path, Err: test.failure}
				}
				return []byte(id + "\n"), nil
			})}
			got, err := store.LoadOrCreate()
			if calls != test.calls || len(waiter.delays) != test.waits {
				t.Fatalf("retry attempts=%d waits=%v", calls, waiter.delays)
			}
			for _, delay := range waiter.delays {
				if delay != 20*time.Millisecond {
					t.Fatal("sharing retry delay must be twenty milliseconds")
				}
			}
			if test.name == "recovers" {
				if err != nil || got != id {
					t.Fatalf("identity recovery: %q %v", got, err)
				}
			} else if !errors.Is(err, test.failure) {
				t.Fatalf("original read failure was not preserved: %v", err)
			}
		})
	}
}
