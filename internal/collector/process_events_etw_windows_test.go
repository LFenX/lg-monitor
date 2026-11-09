//go:build windows && amd64

package collector

import (
	"testing"
	"unsafe"
)

func TestProcessETWWindowsABI(t *testing.T) {
	var properties etwTraceProperties
	var logfile etwLogFile
	var record etwRecord
	for _, check := range []struct {
		name             string
		actual, expected uintptr
	}{
		{"WNODE_HEADER size", unsafe.Sizeof(etwWnode{}), 48},
		{"EVENT_TRACE_PROPERTIES size", unsafe.Sizeof(properties), 120},
		{"logger name offset", unsafe.Offsetof(properties.LoggerNameOffset), 116},
		{"TRACE_LOGFILE_HEADER size", unsafe.Sizeof(etwLogHeader{}), 280},
		{"EVENT_TRACE_LOGFILEW size", unsafe.Sizeof(logfile), 448},
		{"event callback offset", unsafe.Offsetof(logfile.EventCallback), 424},
		{"consumer context offset", unsafe.Offsetof(logfile.Context), 440},
		{"EVENT_RECORD size", unsafe.Sizeof(record), 112},
		{"record context offset", unsafe.Offsetof(record.Context), 104},
		{"property descriptor size", unsafe.Sizeof(etwPropertyDescriptor{}), 16},
	} {
		if check.actual != check.expected {
			t.Errorf("%s: got %d, expected Windows SDK %d", check.name, check.actual, check.expected)
		}
	}
}

func TestProcessETWImageNamesAcceptWindowsPaths(t *testing.T) {
	for _, name := range []string{"Demo.exe", `C:\fake\DEMO.EXE`, "C:/fake/demo.exe"} {
		if got := eventProcessName(name); got != "demo" {
			t.Fatal(name, got)
		}
	}
}
