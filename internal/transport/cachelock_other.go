//go:build !windows

package transport

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func lockCache(path string) (io.Closer, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
