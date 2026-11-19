package profile

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCheckNickname(t *testing.T) {
	if got, err := CheckNickname("  小  李 "); err != nil || got != "小 李" {
		t.Fatalf("CheckNickname = %q, %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("长", MaxNickname+1), "<b>"} {
		if _, err := CheckNickname(bad); err == nil {
			t.Fatalf("CheckNickname(%q) accepted", bad)
		}
	}
}

func TestCheckAvatar(t *testing.T) {
	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nrest"))
	webp := "data:image/webp;base64," + base64.StdEncoding.EncodeToString([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "))
	for _, good := range []string{"fiora-0", "huaji-36", png, webp} {
		if err := CheckAvatar(good); err != nil {
			t.Fatalf("CheckAvatar(%.40q) = %v", good, err)
		}
	}
	lying := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<svg onload=alert(1)>"))
	huge := "data:image/png;base64," + base64.StdEncoding.EncodeToString(append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, MaxAvatar)...))
	for _, bad := range []string{"", "fiora-99", "https://example.test/a.png", "data:image/svg+xml;base64,PHN2Zz4=", lying, huge, "data:image/png;base64,***"} {
		if err := CheckAvatar(bad); err == nil {
			t.Fatalf("CheckAvatar(%.40q) accepted", bad)
		}
	}
}

func TestRandomAvatarAlwaysChanges(t *testing.T) {
	for i := 0; i < 50; i++ {
		got, err := RandomAvatar(nil, "fiora-3")
		if err != nil || got == "fiora-3" || CheckAvatar(got) != nil {
			t.Fatalf("RandomAvatar = %q, %v", got, err)
		}
	}
}

func TestRandomNickname(t *testing.T) {
	name, err := RandomNickname(nil)
	if err != nil || !strings.Contains(name, "的") || utf8.RuneCountInString(name) > MaxNickname {
		t.Fatalf("RandomNickname = %q, %v", name, err)
	}
	if _, err := CheckNickname(name); err != nil {
		t.Fatal(err)
	}
}
