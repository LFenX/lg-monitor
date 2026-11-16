//go:build !windows

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
	fmt.Fprintln(stderr, "lgmon 安装器仅支持 Windows 10/11")
	return 1
}
