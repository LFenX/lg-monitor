//go:build !windows

package config

import "os"

func replaceDocument(from, to string) error { return os.Rename(from, to) }
