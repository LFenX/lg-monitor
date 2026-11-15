// Package docs carries lgmon's user guides inside the program, so the in-page assistant can answer from them
// on a machine that has only the executable.
package docs

import "embed"

// Guides are the documents meant for people using lgmon; plans, reviews and acceptance records stay out.
//
//go:embed web-panel.md agent-guide.md teams.md mcp.md rules.md cli.md card-templates.md dashboards.md collection.md service-switch.md windows-service.md installer.md webhooks.md multi-monitor.md
var Guides embed.FS
