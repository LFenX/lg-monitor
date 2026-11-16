//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	openKernel32           = windows.NewLazySystemDLL("kernel32.dll")
	openConsoleProcessList = openKernel32.NewProc("GetConsoleProcessList")
	openFreeConsole        = openKernel32.NewProc("FreeConsole")
	openUser32             = windows.NewLazySystemDLL("user32.dll")
	openMessageBox         = openUser32.NewProc("MessageBoxW")
)

// detachOwnConsole lets go of a console window that exists only because lgmon was double-clicked. A console
// shared with a terminal is left alone, so output still reaches whoever typed the command.
func detachOwnConsole() {
	var processes [2]uint32
	count, _, _ := openConsoleProcessList.Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes)))
	if count == 1 {
		_, _, _ = openFreeConsole.Call()
	}
}

func showNotice(message string) {
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	title, _ := windows.UTF16PtrFromString("lgmon")
	const informationIcon = 0x40
	_, _, _ = openMessageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), informationIcon)
}
