// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package osinfo detects a concise, human-readable OS / Linux distribution label
// (e.g. "Windows 11 24H2", "Debian 12", "Ubuntu 24.04") for device badges.
package osinfo

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const maxLabelRunes = 32

var (
	labelOnce sync.Once
	cached    string
)

// Label returns the cached human-readable OS/distro version for this machine, capped at 32 runes.
func Label() string {
	labelOnce.Do(func() {
		cached = clampRunes(detect(), maxLabelRunes)
	})
	return cached
}

// FormatWindowsVersion turns Windows registry CurrentVersion values into a concise label.
// On Windows 11, ProductName often still reads "Windows 10 ...", so build >= 22000 overrides it.
func FormatWindowsVersion(productName, displayVersion, buildNumber string) string {
	product := strings.TrimSpace(productName)
	display := strings.TrimSpace(displayVersion)
	build, _ := strconv.Atoi(strings.TrimSpace(buildNumber))

	var base string
	lower := strings.ToLower(product)
	switch {
	case strings.Contains(lower, "server"):
		// Keep e.g. "Windows Server 2022" without verbose edition suffixes ("Standard", "Datacenter").
		parts := strings.Fields(product)
		if len(parts) >= 3 && strings.EqualFold(parts[0], "windows") && strings.EqualFold(parts[1], "server") {
			base = "Windows Server " + parts[2]
		} else {
			base = "Windows Server"
		}
	case build >= 22000:
		base = "Windows 11"
	case build >= 10240:
		base = "Windows 10"
	case strings.Contains(lower, "windows 11"):
		base = "Windows 11"
	case strings.Contains(lower, "windows 10"):
		base = "Windows 10"
	default:
		base = "Windows"
	}

	if display != "" && !strings.Contains(strings.ToLower(base), strings.ToLower(display)) {
		return clampRunes(base+" "+display, maxLabelRunes)
	}
	return clampRunes(base, maxLabelRunes)
}

// ParseOSRelease turns /etc/os-release content into a concise distro + version label
// such as "Debian 12", "Ubuntu 24.04", "Alpine 3.20", or "Rocky 9.4".
func ParseOSRelease(content string) string {
	fields := map[string]string{}
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		fields[k] = v
	}

	id := strings.ToLower(strings.TrimSpace(fields["ID"]))
	versionID := strings.TrimSpace(fields["VERSION_ID"])
	name := strings.TrimSpace(fields["NAME"])
	pretty := strings.TrimSpace(fields["PRETTY_NAME"])

	// Trim third segment on versions like Alpine "3.20.3" -> "3.20".
	if id == "alpine" {
		if parts := strings.Split(versionID, "."); len(parts) >= 2 {
			versionID = parts[0] + "." + parts[1]
		}
	}

	known := map[string]string{
		"debian":              "Debian",
		"ubuntu":              "Ubuntu",
		"alpine":              "Alpine",
		"rocky":               "Rocky",
		"almalinux":           "AlmaLinux",
		"centos":              "CentOS",
		"fedora":              "Fedora",
		"arch":                "Arch Linux",
		"manjaro":             "Manjaro",
		"opensuse-leap":       "openSUSE",
		"opensuse-tumbleweed": "openSUSE",
		"opensuse":            "openSUSE",
		"sles":                "SLES",
		"rhel":                "RHEL",
		"ol":                  "Oracle Linux",
		"nixos":               "NixOS",
		"kali":                "Kali Linux",
		"raspbian":            "Raspbian",
		"deepin":              "Deepin",
		"openeuler":           "openEuler",
		"anolis":              "Anolis",
		"alinux":              "Alibaba Cloud Linux",
		"amzn":                "Amazon Linux",
	}

	if label, ok := known[id]; ok {
		if versionID != "" && id != "arch" && id != "opensuse-tumbleweed" {
			return clampRunes(label+" "+versionID, maxLabelRunes)
		}
		return clampRunes(label, maxLabelRunes)
	}

	if name != "" {
		short := strings.TrimSpace(strings.TrimSuffix(name, "GNU/Linux"))
		if short == "" {
			short = name
		}
		if versionID != "" && !strings.Contains(short, versionID) {
			return clampRunes(short+" "+versionID, maxLabelRunes)
		}
		return clampRunes(short, maxLabelRunes)
	}
	if pretty != "" {
		return clampRunes(pretty, maxLabelRunes)
	}
	return "Linux"
}

func clampRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
