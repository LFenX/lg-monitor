//go:build windows

package desktop

import (
	"context"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

const swRestore = 9

// FocusMarked waits up to wait for a visible top-level window whose title carries marker (see RevealMarker),
// restores it if minimised and brings it to the front. It reports whether it found one: when the lgmon page is
// not the active tab of any window, its title is not the window's, and the caller opens the page instead.
func FocusMarked(ctx context.Context, marker string, wait time.Duration) bool {
	if marker == "" {
		return false
	}
	deadline := time.Now().Add(wait)
	for {
		if window := markedWindow(marker); window != 0 {
			if iconic, _, _ := procIsIconic.Call(window); iconic != 0 {
				procShowWindow.Call(window, swRestore)
			}
			// lgmon's window or tray just took the click, so Windows lets it hand the foreground on.
			procSetForegroundWindow.Call(window)
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func markedWindow(marker string) uintptr {
	var found uintptr
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		if visible, _, _ := procIsWindowVisible.Call(window); visible == 0 {
			return 1
		}
		buffer := make([]uint16, 1024)
		length, _, _ := procGetWindowTextW.Call(window, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if length > 0 && strings.Contains(windows.UTF16ToString(buffer[:length]), marker) {
			found = window
			return 0
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return found
}
