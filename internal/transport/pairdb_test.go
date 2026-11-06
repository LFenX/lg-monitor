package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPairCodeConsumptionIsAtomicAcrossConnectionsAndStoresOnlyHash(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pairing.db")
	first, err := OpenPairDB(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenPairDB(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	code := "ABCD-EFGH-JKLM"
	if err := first.AddCode(ctx, codeHash(code), 2000); err != nil {
		t.Fatal(err)
	}
	var success atomic.Int32
	var wait sync.WaitGroup
	for i := range 20 {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			repository := first
			if i%2 == 1 {
				repository = second
			}
			err := repository.Consume(ctx, codeHash(code), 1000, Credential{ID: fmt.Sprintf("fake-%d", i), Hostname: "fake-host", Serial: fmt.Sprint(i + 1), IssuedAt: 1000})
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, ErrPairCode) {
				t.Errorf("consume: %v", err)
			}
		}(i)
	}
	wait.Wait()
	items, err := first.List(ctx)
	if err != nil || success.Load() != 1 || len(items) != 1 {
		t.Fatalf("atomic code: success=%d items=%+v error=%v", success.Load(), items, err)
	}
	if err := first.AddCode(ctx, codeHash("replacement"), 3000); err != nil {
		t.Fatal(err)
	}
	item := items[0]
	item.Serial = "replacement-serial"
	if err := first.Consume(ctx, codeHash("replacement"), 1000, item); !errors.Is(err, ErrRegistered) {
		t.Fatalf("active identity was replaced: %v", err)
	}
	if err := first.Revoke(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.Consume(ctx, codeHash("replacement"), 1000, item); err != nil {
		t.Fatalf("rolled-back code was consumed or revoked identity cannot re-pair: %v", err)
	}
	second.Close()
	first.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(code)) || bytes.Contains(data, []byte(normalizedCode(code))) {
			t.Fatal("pairing database contains a plaintext code")
		}
	}
}
