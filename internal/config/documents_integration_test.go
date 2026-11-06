//go:build integration

package config

import (
	"context"
	"errors"
	"io/fs"
	"testing"
)

func TestDirectoryDocumentsReplaceAndDeleteOwnTemporaryFiles(t *testing.T) {
	files := DirectoryDocuments{Path: t.TempDir()}
	ctx := context.Background()
	for _, text := range []string{"id: fake\nextends: standard\n", "id: fake\nextends: compact\n"} {
		if err := files.Replace(ctx, "card-templates/fake.yaml", []byte(text)); err != nil {
			t.Fatal(err)
		}
		read, err := fs.ReadFile(files, "card-templates/fake.yaml")
		if err != nil || string(read) != text {
			t.Fatalf("atomic replacement differs: %s %v", read, err)
		}
		entries, err := files.ReadDir("card-templates")
		if err != nil || len(entries) != 1 || entries[0].Name() != "fake.yaml" {
			t.Fatal("temporary configuration file leaked")
		}
	}
	if err := files.Remove(ctx, "card-templates/fake.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(files, "card-templates/fake.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("own template not deleted: %v", err)
	}
}
