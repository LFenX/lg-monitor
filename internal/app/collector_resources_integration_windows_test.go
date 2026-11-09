//go:build integration && windows

package app

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeCollectorResourceLeaseRestoresCurrentProcessAndHonorsExplicitParallelism(t *testing.T) {
	get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessWorkingSetSizeEx")
	read := func() (uintptr, uintptr, uint32) {
		t.Helper()
		var minimum, maximum uintptr
		var flags uint32
		ok, _, err := get.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&minimum)), uintptr(unsafe.Pointer(&maximum)), uintptr(unsafe.Pointer(&flags)))
		if ok == 0 {
			t.Fatal(err)
		}
		return minimum, maximum, flags
	}
	beforeMin, beforeMax, beforeFlags := read()
	beforeP := runtime.GOMAXPROCS(0)
	t.Setenv("GOMAXPROCS", "")
	first, err := (nativeCollectorResources{}).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	second, err := (nativeCollectorResources{}).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	_, limit, flags := read()
	expected := uintptr(36 * 1024 * 1024)
	if beforeFlags&4 != 0 && beforeMax < expected {
		expected = beforeMax
	}
	if limit != expected || flags&4 == 0 || runtime.GOMAXPROCS(0) != min(beforeP, 2) {
		t.Fatalf("current-process budget not applied: max=%d flags=%d P=%d", limit, flags, runtime.GOMAXPROCS(0))
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, maximum, flags := read(); maximum != expected || flags&4 == 0 {
		t.Fatal("first lease removed another collector's budget")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	afterMin, afterMax, afterFlags := read()
	if beforeMin != afterMin || beforeMax != afterMax || beforeFlags&5 != afterFlags&5 || beforeP != runtime.GOMAXPROCS(0) {
		t.Fatalf("process limits not restored: before=%d/%d/%d/%d after=%d/%d/%d/%d", beforeMin, beforeMax, beforeFlags, beforeP, afterMin, afterMax, afterFlags, runtime.GOMAXPROCS(0))
	}
	t.Setenv("GOMAXPROCS", "explicit-caller-value")
	lease, err := (nativeCollectorResources{}).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Close() })
	if runtime.GOMAXPROCS(0) != beforeP {
		t.Fatal("explicit GOMAXPROCS overwritten")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
