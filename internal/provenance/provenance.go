// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package provenance

import "fmt"

// Authorship and provenance metadata embedded into lgmon binaries, HTTP/OpenAPI
// responses, and SQLite database headers.
const (
	Author     = "LFen"
	Product    = "lgmon"
	Project    = "lg-monitor"
	Repository = "https://github.com/LFenX/lg-monitor"
	Site       = "https://lfen.ink/"
	HeaderKey  = "X-Lgmon-Origin"
	// HeaderValue identifies the original project and author in HTTP responses.
	HeaderValue = "lgmon by LFen (https://github.com/LFenX/lg-monitor)"
	// SQLiteApplicationID encodes ASCII "LFEN" (0x4C46454E) into bytes 68..71 of every SQLite DB header.
	SQLiteApplicationID uint32 = 0x4C46454E
	CopyrightHeader            = "Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor"
)

// BuildCommit and BuildTime are injected by scripts/build.ps1 via -ldflags.
var (
	BuildCommit = "dev"
	BuildTime   = "unknown"
)

// Signature returns a compact human- and machine-readable provenance line.
func Signature() string {
	return fmt.Sprintf("%s (%s) by %s <%s> [%s]", Product, Project, Author, Repository, BuildCommit)
}
