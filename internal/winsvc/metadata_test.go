package winsvc

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/unicode"
)

func writeMetadata(t *testing.T, directory, text string) {
	t.Helper()
	data, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, SetupMetadataFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadSetupMetadataKeepsHomeRoleAndAddress(t *testing.T) {
	directory := t.TempDir()
	if _, ok := ReadSetupMetadata(directory); ok {
		t.Fatal("a missing setup.ini was read")
	}
	home := filepath.Join(directory, "监控数据 💾")
	writeMetadata(t, directory, "[Setup]\r\nProduct=lg-monitor\r\nHome="+home+"\r\nRole=collector\r\nMonitor=fake-monitor:7443\r\n")
	got, ok := ReadSetupMetadata(directory)
	if want := (SetupMetadata{Home: home, Role: "collector", Monitor: "fake-monitor:7443"}); !ok || got != want {
		t.Fatalf("metadata = %+v %v, want %+v", got, ok, want)
	}
	// Files written by 0.13.2 and earlier have no address; those still name the home and role.
	writeMetadata(t, directory, "[Setup]\r\nProduct=lg-monitor\r\nHome="+home+"\r\nRole=both\r\n")
	if got, ok := ReadSetupMetadata(directory); !ok || got.Role != "both" || got.Listen != "" {
		t.Fatalf("older metadata = %+v %v", got, ok)
	}
	writeMetadata(t, directory, "[Setup]\r\nProduct=another-app\r\nHome="+home+"\r\n")
	if _, ok := ReadSetupMetadata(directory); ok {
		t.Fatal("another product's setup.ini was read")
	}
	if err := os.WriteFile(filepath.Join(directory, SetupMetadataFile), []byte("[Setup]\r\nProduct=lg-monitor\r\nHome="+home+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadSetupMetadata(directory); ok {
		t.Fatal("a setup.ini without the installer's UTF-16 BOM was read")
	}
}
