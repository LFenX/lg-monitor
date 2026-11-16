//go:build !windows

package main

import (
	"fmt"
	"os"
)

func detachOwnConsole() {}

func showNotice(message string) { fmt.Fprintln(os.Stderr, message) }
