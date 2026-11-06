package secrets

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeProtector struct {
	err   error
	reads int
}

func (p *fakeProtector) Encrypt(value []byte) ([]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	result := append([]byte(nil), value...)
	for i := range result {
		result[i] ^= 0x55
	}
	return result, nil
}
func (p *fakeProtector) Decrypt(value []byte) ([]byte, error) { p.reads++; return p.Encrypt(value) }

type fakePolicy struct {
	owner   string
	secured []string
	err     error
}

func (p *fakePolicy) CurrentUser() (string, error) { return p.owner, nil }
func (p *fakePolicy) Secure(path, owner string) error {
	if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
		return errors.New("temporary file was written before access was restricted")
	}
	p.secured = append(p.secured, owner)
	return p.err
}

func TestEncryptedValuesAndOriginalCreatorArePreserved(t *testing.T) {
	home := t.TempDir()
	protector, policy := &fakeProtector{}, &fakePolicy{owner: "fake-creator"}
	var logs bytes.Buffer
	store, err := NewFileStore(home, protector, policy, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	const plain = "fake-AppSecret-do-not-log-1234"
	if err := store.Set(context.Background(), "first", []byte(plain)); err != nil {
		t.Fatal(err)
	}
	policy.owner = "fake-system-writer"
	if err := store.Set(context.Background(), "second", []byte("another-fake-value")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policy.secured, []string{"fake-creator", "fake-creator"}) {
		t.Fatalf("creator access changed after later write: %v", policy.secured)
	}
	names, err := store.List(context.Background())
	if err != nil || !reflect.DeepEqual(names, []string{"first", "second"}) || protector.reads != 0 {
		t.Fatalf("list decrypted values: %v, %v", names, err)
	}
	got, err := store.Get(context.Background(), "first")
	if err != nil || string(got) != plain {
		t.Fatalf("round trip failed: %v", err)
	}
	clear(got)
	file, err := os.ReadFile(filepath.Join(home, "data", "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file, []byte(plain)) || strings.Contains(logs.String(), plain) {
		t.Fatal("plaintext leaked into persisted data or logs")
	}
}

func TestSecretErrorsDoNotExposeInputAndDoNotReplaceGoodFile(t *testing.T) {
	const plain = "fake-sensitive-credential"
	var logs bytes.Buffer
	protector, policy := &fakeProtector{}, &fakePolicy{owner: "fake-owner"}
	store, err := NewFileStore(t.TempDir(), protector, policy, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "app", []byte(plain)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.home, "data", "secrets.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("fake encryption error echoing " + plain)
	protector.err = failure
	err = store.Set(context.Background(), "app", []byte(plain))
	if err == nil || !errors.Is(err, failure) || strings.Contains(err.Error(), plain) || strings.Contains(logs.String(), plain) {
		t.Fatal("encryption diagnostic exposed plaintext or lost its cause")
	}
	protector.err = nil
	policy.err = errors.New("fake ACL failure " + plain)
	err = store.Set(context.Background(), "app", []byte(plain))
	if err == nil || strings.Contains(err.Error(), plain) || strings.Contains(logs.String(), plain) {
		t.Fatal("ACL diagnostic exposed plaintext")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed save replaced the last valid encrypted file")
	}
}

func TestInvalidSecretFileIsNotOverwritten(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "data", "secrets.json")
	contents := []byte(`{"accidental-fake-secret-key":"fake-value"}`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(home, &fakeProtector{}, &fakePolicy{owner: "fake-owner"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "app", []byte("fake-value")); err == nil || strings.Contains(err.Error(), "accidental-fake-secret-key") {
		t.Fatal("invalid secret file was accepted or echoed")
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, contents) {
		t.Fatal("invalid existing secret file was overwritten")
	}
}
