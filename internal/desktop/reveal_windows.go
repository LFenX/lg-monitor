//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// not the active tab, Windows UI Automation can select its marked tab header.
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
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	// A background tab has no HWND. Ask the browser to select it, then use the
	// resulting top-level title to restore and activate exactly that window.
	if !selectMarkedTab(ctx, marker) {
		return false
	}
	if window := markedWindow(marker); window != 0 {
		if iconic, _, _ := procIsIconic.Call(window); iconic != 0 {
			procShowWindow.Call(window, swRestore)
		}
		procSetForegroundWindow.Call(window)
		return true
	}
	return false
}

type windowSearch struct {
	marker string
	found  uintptr
}

// Windows callbacks are never freed by Go. Reuse one across clicks and polls.
var findMarked = syscall.NewCallback(func(window, param uintptr) uintptr {
	search := *(**windowSearch)(unsafe.Pointer(&param))
	if visible, _, _ := procIsWindowVisible.Call(window); visible == 0 {
		return 1
	}
	buffer := make([]uint16, 1024)
	length, _, _ := procGetWindowTextW.Call(window, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length > 0 && strings.Contains(windows.UTF16ToString(buffer[:length]), search.marker) {
		search.found = window
		return 0
	}
	return 1
})

func markedWindow(marker string) uintptr {
	search := &windowSearch{marker: marker}
	procEnumWindows.Call(findMarked, uintptr(unsafe.Pointer(search)))
	runtime.KeepAlive(search)
	return search.found
}

func selectMarkedTab(ctx context.Context, marker string) bool {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return false
	}
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	const selectTab = `Add-Type -AssemblyName UIAutomationClient
$element = [System.Windows.Automation.AutomationElement]
$scope = [System.Windows.Automation.TreeScope]
$tabs = [System.Windows.Automation.PropertyCondition]::new($element::ControlTypeProperty, [System.Windows.Automation.ControlType]::TabItem)
foreach ($win in $element::RootElement.FindAll($scope::Children, [System.Windows.Automation.Condition]::TrueCondition)) {
  if ($win.Current.ClassName -notin 'Chrome_WidgetWin_1','MozillaWindowClass') { continue }
  foreach ($tab in $win.FindAll($scope::Descendants, $tabs)) {
    if ($tab.Current.Name -and $tab.Current.Name.Contains($env:LGMON_TAB_MARKER)) {
      $tab.GetCurrentPattern([System.Windows.Automation.SelectionItemPattern]::Pattern).Select()
      exit 0
    }
  }
}
exit 1`
	command := exec.CommandContext(call, filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", selectTab)
	command.Env = append(os.Environ(), "LGMON_TAB_MARKER="+marker)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return command.Run() == nil
}
