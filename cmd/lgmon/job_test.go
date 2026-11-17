package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/spf13/cobra"
)

type fakeJobKeys struct {
	job    string
	hash   []byte
	closed int
}

func (f *fakeJobKeys) SetJobKey(_ context.Context, job string, hash []byte) error {
	f.job, f.hash = job, append([]byte(nil), hash...)
	return nil
}

func (f *fakeJobKeys) Close() error {
	f.closed++
	return nil
}

func TestJobKeyStoresOnlyTheDigest(t *testing.T) {
	keys := &fakeJobKeys{}
	deps := jobKeyDependencies{Administrator: func() (bool, error) { return true, nil }, Entropy: bytes.NewReader(bytes.Repeat([]byte{7}, 32)), Open: func(context.Context, string) (jobKeyStore, error) { return keys, nil }}
	root := &cobra.Command{Use: "test"}
	root.PersistentFlags().String("home", t.TempDir(), "fake home")
	root.AddCommand(newJobCommand(deps))
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"job", "key", "aics-upload", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result jobKeyOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	digest := api.TokenHash(result.Key)
	if result.Status != "ok" || !strings.HasPrefix(result.Key, api.JobKeyPrefix) || keys.job != "aics-upload" || !bytes.Equal(keys.hash, digest[:]) || keys.closed != 1 {
		t.Fatalf("result=%+v stored=%s closed=%d", result, keys.job, keys.closed)
	}
}

func TestJobKeyNeedsAdministratorAndAJobID(t *testing.T) {
	opened := false
	deps := jobKeyDependencies{Administrator: func() (bool, error) { return false, nil }, Entropy: bytes.NewReader(make([]byte, 32)), Open: func(context.Context, string) (jobKeyStore, error) {
		opened = true
		return &fakeJobKeys{}, nil
	}}
	for _, args := range [][]string{{"job", "key", "aics-upload", "--json"}, {"job", "key", "Bad_ID"}} {
		root := &cobra.Command{Use: "test", SilenceUsage: true}
		root.PersistentFlags().String("home", t.TempDir(), "fake home")
		root.AddCommand(newJobCommand(deps))
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs(args)
		if err := root.Execute(); err == nil || strings.Contains(output.String(), api.JobKeyPrefix) {
			t.Fatalf("%v succeeded or printed a key: %s", args, output.String())
		}
	}
	if opened {
		t.Fatal("auth database opened without administrator rights")
	}
}
