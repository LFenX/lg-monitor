// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package osinfo

import "testing"

func TestFormatWindowsVersion(t *testing.T) {
	cases := []struct {
		product, display, build, want string
	}{
		// Windows 11 commonly reports ProductName="Windows 10 Pro" with build >= 22000.
		{"Windows 10 Pro", "24H2", "26100", "Windows 11 24H2"},
		{"Windows 11 Home", "23H2", "22631", "Windows 11 23H2"},
		{"Windows 10 Pro", "", "22000", "Windows 11"},
		{"Windows 10 Enterprise", "22H2", "19045", "Windows 10 22H2"},
		{"Windows Server 2022 Standard", "21H2", "20348", "Windows Server 2022 21H2"},
		{"Windows Server 2025 Datacenter", "", "26100", "Windows Server 2025"},
		{"", "", "", "Windows"},
	}
	for _, tc := range cases {
		if got := FormatWindowsVersion(tc.product, tc.display, tc.build); got != tc.want {
			t.Errorf("FormatWindowsVersion(%q, %q, %q) = %q, want %q", tc.product, tc.display, tc.build, got, tc.want)
		}
	}
}

func TestParseOSRelease(t *testing.T) {
	cases := []struct {
		name, content, want string
	}{
		{
			"debian 12",
			"PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"12\"\nID=debian\n",
			"Debian 12",
		},
		{
			"ubuntu 24.04",
			"PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\n",
			"Ubuntu 24.04",
		},
		{
			"alpine",
			"NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.20.3\n",
			"Alpine 3.20",
		},
		{
			"rocky",
			"NAME=\"Rocky Linux\"\nVERSION_ID=\"9.4\"\nID=\"rocky\"\n",
			"Rocky 9.4",
		},
		{
			"arch",
			"NAME=\"Arch Linux\"\nID=arch\n",
			"Arch Linux",
		},
		{
			"custom",
			"NAME=\"MyCustom GNU/Linux\"\nVERSION_ID=\"4.2\"\nID=mycustom\n",
			"MyCustom 4.2",
		},
		{
			"empty",
			"",
			"Linux",
		},
	}
	for _, tc := range cases {
		if got := ParseOSRelease(tc.content); got != tc.want {
			t.Errorf("%s: ParseOSRelease = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLabelReturnsNonEmpty(t *testing.T) {
	if got := Label(); got == "" {
		t.Fatal("Label() returned empty string")
	}
}
