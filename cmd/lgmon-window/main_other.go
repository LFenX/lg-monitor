//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"fmt"
	"io"
)

func run(args []string, stderr io.Writer) int {
	if _, err := parseOptions(args, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintln(stderr, "lgmon 窗口仅支持 Windows 10/11")
	return 1
}
