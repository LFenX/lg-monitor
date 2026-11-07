package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestHTTPWarningAddressRanges(t *testing.T) {
	for _, listen := range []string{"", "127.0.0.1:7444", "127.23.45.67:0", "LOCALHOST.:7444", "[::1]:7444", "[::ffff:127.0.0.1]:7444", "100.64.0.0:7444", "100.127.255.255:7444", "[fd7a:115c:a1e0::1]:7444", "[FD7A:115C:A1E0:ffff:ffff:ffff:ffff:ffff]:7444", "[::ffff:100.126.79.86]:7444"} {
		if got := (Settings{HTTPListen: listen}).HTTPWarning(); got != "" {
			t.Errorf("%s: unexpected warning %q", listen, got)
		}
	}
	for _, listen := range []string{"0.0.0.0:7444", ":7444", "[::]:7444", "192.168.1.10:7444", "100.63.255.255:7444", "100.128.0.0:7444", "[fd7a:115c:a1df::1]:7444", "[fd7a:115c:a1e1::1]:7444", "[::ffff:192.168.1.10]:7444", "monitor.example:7444", "localhost.example:7444"} {
		if got := (Settings{HTTPListen: listen}).HTTPWarning(); got != HTTPWarningText {
			t.Errorf("%s: missing warning %q", listen, got)
		}
	}
}

func TestLoadSettingsIgnoresUnrelatedBrokenDocuments(t *testing.T) {
	files := fstest.MapFS{
		"settings.yaml": {Data: []byte("httpListen: 0.0.0.0:7444\n")},
		"rules.yaml":    {Data: []byte("broken: [\n")},
	}
	settings, err := LoadSettings(files)
	if err != nil || settings.HTTPWarning() != HTTPWarningText {
		t.Fatalf("HTTP exposure was hidden by unrelated errors: %+v %v", settings, err)
	}
	if _, err := Load(files); err == nil {
		t.Fatal("fixture must be invalid as a complete configuration")
	}
	files["settings.yaml"] = &fstest.MapFile{Data: []byte("httpListen: [\n")}
	if _, err := LoadSettings(files); err == nil || !strings.Contains(err.Error(), "settings.yaml") {
		t.Fatalf("invalid settings were ignored: %v", err)
	}
}
