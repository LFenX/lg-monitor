// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package provenance

import (
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProvenanceConstants(t *testing.T) {
	if Author != "LFen" || Repository != "https://github.com/LFenX/lg-monitor" || Site != "https://lfen.ink/" {
		t.Fatalf("unexpected provenance constants: author=%q repo=%q site=%q", Author, Repository, Site)
	}
	var magic [4]byte
	binary.BigEndian.PutUint32(magic[:], SQLiteApplicationID)
	if string(magic[:]) != "LFEN" {
		t.Fatalf("SQLiteApplicationID decoded to %q, want LFEN", string(magic[:]))
	}
	if !strings.Contains(Signature(), Author) || !strings.Contains(Signature(), Repository) {
		t.Fatalf("unexpected signature: %q", Signature())
	}
}

func TestSourceFilesCarryCopyrightHeader(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	var missing []string

	checkDir := func(base string, exts map[string]bool) {
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == ".claude" || name == "node_modules" || name == "dist" || name == "bin" || name == "dev-home" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, ".d.ts") {
				return nil
			}
			if !exts[filepath.Ext(name)] {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			head := string(raw)
			if len(head) > 512 {
				head = head[:512]
			}
			if !strings.Contains(head, CopyrightHeader) {
				rel, _ := filepath.Rel(root, path)
				missing = append(missing, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}

	checkDir(root, map[string]bool{".go": true})
	checkDir(filepath.Join(root, "web", "src"), map[string]bool{".ts": true, ".tsx": true, ".css": true})

	if len(missing) > 0 {
		t.Fatalf("%d source files missing copyright header %q (first 10: %v)", len(missing), CopyrightHeader, missing[:min(10, len(missing))])
	}
}
