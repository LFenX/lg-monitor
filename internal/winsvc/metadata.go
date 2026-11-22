// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/encoding/unicode"
)

// SetupMetadataFile is the ownership file the Inno installer writes next to lgmon.exe.
const SetupMetadataFile = "setup.ini"

// SetupMetadata is what setup.ini records: which home and role the program directory was installed for, and from
// 0.13.3 the collector address the monitor listens on or the monitor a collector reports to.
type SetupMetadata struct {
	Home, Role, Listen, Monitor, DownloadReceipt string
}

// ReadSetupMetadata reads setup.ini in directory. The installer writes it as UTF-16LE with a BOM so that paths
// outside the system code page survive; anything else, or another product's file, reads as absent.
func ReadSetupMetadata(directory string) (SetupMetadata, bool) {
	file, err := os.Open(filepath.Join(directory, SetupMetadataFile))
	if err != nil {
		return SetupMetadata{}, false
	}
	defer file.Close()
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder()
	lines := bufio.NewScanner(decoder.Reader(io.LimitReader(file, 64<<10)))
	if !lines.Scan() || lines.Text() != "[Setup]" {
		return SetupMetadata{}, false
	}
	var product string
	var metadata SetupMetadata
	for lines.Scan() {
		key, value, _ := strings.Cut(lines.Text(), "=")
		switch key {
		case "Product":
			product = value
		case "Home":
			metadata.Home = value
		case "Role":
			metadata.Role = value
		case "Listen":
			metadata.Listen = value
		case "Monitor":
			metadata.Monitor = value
		case "DownloadReceipt":
			metadata.DownloadReceipt = value
		}
	}
	if lines.Err() != nil || product != "lg-monitor" || !filepath.IsAbs(metadata.Home) {
		return SetupMetadata{}, false
	}
	return metadata, true
}
