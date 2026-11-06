//go:build !windows

package transport

import "os"

func secureStateDirectory(path string, _ bool) error { return os.Chmod(path, 0o700) }

func publishState(temporary, destination string, overwrite bool) error {
	if overwrite {
		return os.Rename(temporary, destination)
	}
	return os.Link(temporary, destination)
}
