package collector

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCollectorIdentityPersistsAcrossConcurrentFirstStarts(t *testing.T) {
	home := t.TempDir()
	store := FileIDStore{Home: home}
	var workers sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < cap(ids); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			id, err := store.LoadOrCreate()
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		}()
	}
	workers.Wait()
	close(ids)
	want, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := hex.DecodeString(strings.ReplaceAll(want, "-", ""))
	if err != nil || len(bytes) != 16 || bytes[6]>>4 != 4 || bytes[8]>>6 != 2 {
		t.Fatalf("invalid UUID v4: %q, %v", want, err)
	}
	for id := range ids {
		if id != want {
			t.Fatalf("collector identity changed: %q != %q", id, want)
		}
	}
	data, err := store.readIdentity(filepath.Join(home, "data", "collector-id"))
	if err != nil || strings.TrimSpace(string(data)) != want {
		t.Fatalf("identity file = %q, %v", data, err)
	}
	temporary, err := filepath.Glob(filepath.Join(home, "data", "collector-id-*.tmp"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary identity files were retained: %v, %v", temporary, err)
	}
}

func TestInvalidCollectorIdentityIsNotOverwritten(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "data", "collector-id")
	if err := os.WriteFile(path, []byte("invalid identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileIDStore{Home: home}).LoadOrCreate(); err == nil {
		t.Fatal("invalid identity was replaced")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "invalid identity" {
		t.Fatalf("identity overwritten: %q, %v", data, err)
	}
}
