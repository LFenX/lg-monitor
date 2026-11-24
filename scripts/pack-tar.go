//go:build ignore

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// pack-tar packages a Linux lgmon binary and README into a POSIX tar.gz archive with 0755 permissions on the binary.
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "usage: go run scripts/pack-tar.go <out.tar.gz> <lgmon-binary> <readme>\n")
		os.Exit(2)
	}
	outPath, binPath, readmePath := os.Args[1], os.Args[2], os.Args[3]
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		die(err)
	}
	out, err := os.Create(outPath)
	if err != nil {
		die(err)
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	now := time.Now().UTC()
	if err := addFile(tw, binPath, "lgmon", 0o755, now); err != nil {
		die(err)
	}
	if err := addFile(tw, readmePath, "README.md", 0o644, now); err != nil {
		die(err)
	}
	if err := tw.Close(); err != nil {
		die(err)
	}
	if err := gz.Close(); err != nil {
		die(err)
	}
}

func addFile(tw *tar.Writer, src, name string, mode int64, modTime time.Time) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    info.Size(),
		ModTime: modTime,
		Format:  tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
