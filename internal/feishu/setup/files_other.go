//go:build !windows

package setup

import "os"

func replaceFile(from, to string) error { return os.Rename(from, to) }
