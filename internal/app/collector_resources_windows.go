//go:build windows

package app

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func applyCollectorResidentBudget() (func() error, error) {
	dll := windows.NewLazySystemDLL("kernel32.dll")
	get, set := dll.NewProc("GetProcessWorkingSetSizeEx"), dll.NewProc("SetProcessWorkingSetSizeEx")
	var minimum, maximum uintptr
	var flags uint32
	process := uintptr(windows.CurrentProcess())
	result, _, err := get.Call(process, uintptr(unsafe.Pointer(&minimum)), uintptr(unsafe.Pointer(&maximum)), uintptr(unsafe.Pointer(&flags)))
	if result == 0 {
		return nil, fmt.Errorf("读取当前采集进程的常驻内存预算失败：%w", err)
	}
	limit := uintptr(36 * 1024 * 1024)
	if flags&4 != 0 && maximum < limit {
		limit = maximum
	}
	// Keep an existing smaller hard limit and never increase the minimum reservation.
	result, _, err = set.Call(process, min(minimum, limit), limit, uintptr(flags&^uint32(8)|4))
	if result == 0 {
		return nil, fmt.Errorf("设置当前采集进程的常驻内存预算失败：%w", err)
	}
	restoreFlags := flags
	if flags&4 == 0 {
		restoreFlags |= 8
	}
	return func() error {
		result, _, err := set.Call(process, minimum, maximum, uintptr(restoreFlags))
		if result == 0 {
			return fmt.Errorf("恢复当前采集进程的常驻内存预算失败：%w", err)
		}
		return nil
	}, nil
}
