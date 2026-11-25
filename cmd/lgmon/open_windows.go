//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"github.com/LFenX/lg-monitor/internal/winsvc"
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

const (
	iconInformation = 0x40
	iconQuestion    = 0x20
	buttonsYesNo    = 0x04
	answerYes       = 6
)

func messageBox(message string, style uintptr) uintptr {
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return 0
	}
	title, _ := windows.UTF16PtrFromString("lgmon")
	answer, _, _ := openMessageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), style)
	return answer
}

func showNotice(message string) { messageBox(message, iconInformation) }

// startInstalledService starts the service as this person. A service registered by an older version lets only
// administrators do that, so the person is asked first and Windows then asks for consent.
func startInstalledService(ctx context.Context) error {
	err := winsvc.StartAsUser(ctx)
	if err == nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return err
	}
	if messageBox("lgmon 的后台服务还没有运行。\n\n刚开机时 Windows 会晚一些才启动它。现在启动吗？（Windows 会请求一次管理员授权）", buttonsYesNo|iconQuestion) != answerYes {
		return errStartDeclined
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 lgmon 程序失败：%w", err)
	}
	err = launchConnectConsent(executable, []string{"service", "start"})
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return errStartDeclined
	}
	return err
}

func serviceFailure(ctx context.Context) string { return winsvc.LastFailure(ctx) }
