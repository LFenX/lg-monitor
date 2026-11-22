<div align="center">

<img src="docs/images/logo.svg" width="92" alt="lgmon">

# lgmon

### AI-Native Service Monitor, Architecture Map, Living Knowledge Base & Team LAN Hub — in a Single Windows Exe.

Websites · APIs · Ports · Windows Services · Processes · Scheduled Tasks · Batch Jobs · Living Docs · Team Sharing

**English** · [简体中文](README.zh-CN.md)

[![Release](https://img.shields.io/github/v/release/LFenX/lg-monitor?label=latest&color=c96442)](https://github.com/LFenX/lg-monitor/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/LFenX/lg-monitor/total?color=2f855a)](https://github.com/LFenX/lg-monitor/releases)
[![Stars](https://img.shields.io/github/stars/LFenX/lg-monitor?style=flat&color=d69e2e)](https://github.com/LFenX/lg-monitor/stargazers)
![Windows 10/11](https://img.shields.io/badge/Windows-10%20%7C%2011-0078D4)
![Bilingual](https://img.shields.io/badge/UI-%E4%B8%AD%E6%96%87%20%2F%20English-1f6feb)
![MCP](https://img.shields.io/badge/MCP-98%20tools-6e56cf)
![Go 1.27](https://img.shields.io/badge/Go-1.27%20%7C%20CGO%3D0-00ADD8)
[![License: PolyForm Noncommercial](https://img.shields.io/badge/license-PolyForm%20Noncommercial-8a6d3b)](LICENSE.md)

[Download Latest](https://license.lfen.ink/download/readme) · [Why lgmon](#why-lgmon) · [Living Docs & Knowledge Base](#living-docs--knowledge-base) · [Team & LAN Sharing](#team-collaboration--lan-sharing) · [AI Agents & Bohemia](#ai-agents--bohemia) · [Dependency Map](#dependency-map) · [Quick Start](#quick-start)

<img src="docs/images/v0.13.10/en/board.png" alt="lgmon board: service list with 24-hour heartbeat, uptime and failure reasons" width="880">

<sub>One single executable — live service status, architecture graph, runbooks, and team sharing without Docker or external databases.</sub>

</div>

---

## Why lgmon?

Most monitoring tools stop at *"is port 3000 up?"* — leaving your architecture diagrams stale in Notion, your runbooks scattered across chat threads, and your teammates constantly pinging *"hey, is the staging API down or is it just me?"*.

**lgmon brings four workflows together into one lightweight Windows executable (`lgmon.exe`):**

1. **Real-Time Service Monitoring** — Watches websites, APIs, ports, Windows services, processes (with ETW instant events), scheduled tasks, and daily batch jobs with built-in SQLite storage and Feishu/Lark interactive alerting.
2. **Live Architecture & Dependency Map** — Visualizes 16 typed relationships (*calls, reads/writes, via tunnel, sends/consumes, fallback, backup…*) so you immediately see blast radius when anything breaks.
3. **Living Service Docs & RAG Knowledge Base** — Every service carries its own Markdown runbook right next to its live heartbeat, authored by your AI agent from real machine configs and searchable via **Bohemia** (embeddings + rerank Q&A).
4. **Zero-Friction Team Collaboration & LAN Read-Only Sharing** — Share live dashboards over your LAN or Tailscale with **zero installation for browser guests**, fine-grained board visibility controls, peer-to-peer departments, and secure AI model sharing.

> **Bilingual by design**: Switch seamlessly between **English** and **简体中文** in the dashboard, Settings, or compact startup window — language state syncs instantaneously across every open window, including all Agent prompts and Bohemia chat.

### Is lgmon right for you?

| lgmon is built for you if you… | Look elsewhere if you… |
|---|---|
| run websites, APIs, tunnels, databases, or scheduled jobs on Windows 10/11 | run your production fleet on Linux or Kubernetes (collector is Windows-only) |
| want **monitoring + dependency map + living runbooks + team status page** in one exe | need multi-region public-internet synthetic probes |
| prefer telling Claude Code / Codex / Cursor what to watch instead of clicking forms | need heavy distributed tracing / APM profiling |
| want teammates on your LAN / Tailscale to check status & ask AI without installing anything | plan commercial / for-profit use (see [License](#license)) |

---

## Living Docs & Knowledge Base

Documentation rots the moment it leaves your operations tool. In **lgmon**, every service has a **living Markdown document** stored right alongside its monitor (`config/docs/`):

- **AI-authored from ground truth**: Click *"Let your agent write this doc"* (or pick **Write / edit service docs** from the Agent prompt sheet). Your agent inspects actual configs, ports, startup commands, and dependencies on the machine and writes a structured runbook with a table of contents and embedded live dependency diagram.
- **Always one click from an incident**: When an alert fires, the service page shows 24h/30d uptime, response latency, event timeline, sub-services, dependency neighborhood, **and** its runbook side-by-side.
- **RAG-powered Knowledge Base via Bohemia (<kbd>Ctrl</kbd>+<kbd>J</kbd>)**: Bohemia indexes your service docs and lgmon guides using vector embeddings + rerank (DashScope) and answers questions with DeepSeek — ask *"How is the orders API started?"*, *"Which services depend on Postgres?"*, or *"Summarize last night's outage for my team"* and get grounded answers with clickable links.
- **Shared with your team automatically**: Anyone viewing your board over LAN sharing or read-only browser access can read your service docs and ask Bohemia about them — turning your monitor into an instant internal engineering portal.

---

## Team Collaboration & LAN Sharing

lgmon makes sharing operational visibility across a small team effortless, private, and safe — with **two complementary modes** on your LAN or Tailscale network:

### 1. Zero-Install LAN Read-Only Web Access
Turn on **Read-only LAN Web Access** on the **LAN** page:
- **Open in any browser**: Teammates open your LAN / Tailscale URL directly — no installation, no accounts, no token pasting.
- **Strictly read-only**: Guests can inspect live status, dependency maps, service docs, and even use **read-only Bohemia Q&A** to ask about services. Every write, edit, delete, or admin endpoint is hard-blocked (`403`) on the backend.
- **Scoped board visibility (`lan.webBoards`)**: Choose whether LAN guests can see **All boards** or **Selected boards only** — hidden boards, monitors, docs, and alerts are strictly filtered out on the server (including inside Bohemia).
- **Live visitor presence**: See who is viewing your dashboard right now at the bottom of your sidebar, with charming deterministic avatars and an IP activity dialog.

### 2. Peer-to-Peer Board Sharing, Departments & Shared AI Models
- **6-digit pairing approval**: Colleagues running lgmon on the same LAN / Tailscale can request to view a board; both screens show a 6-digit code, you approve once confirmed, and you can revoke access instantly at any time.
- **Nested Departments**: Organize colleagues into peer-to-peer departments (hosted on the creator's machine with multi-lead approval). Shared boards group neatly by department in the sidebar, and your compact startup window shows a live **Shared Boards health summary** right beside your own status!
- **Share Bohemia's AI model safely**: Teammates who don't have a DeepSeek API key can use yours over the encrypted peer link — requests forward through your machine and **your API key never leaves your computer**.
- **Automatic scoped Windows Firewall rules**: Turning on LAN visibility creates a tight inbound rule for `lgmon.exe` (7445/TCP & 7446/UDP, private & Tailscale ranges only) and removes it cleanly when turned off. See [LAN sharing](docs/teams.md).

---

## AI Agents & Bohemia

### Built for AI Agents — 98 MCP Tools
Everything lgmon can do is exposed over **MCP** (Model Context Protocol). Copy one prompt from the startup window and paste it into **Claude Code, Codex, Cursor, Gemini CLI, Trae, Qwen, or Cline** — the agent connects itself and gets to work:

```powershell
# Print ready-to-run `mcp add` commands for Claude Code & Codex + generic mcpServers JSON
lgmon agent connect

# Give smaller models a focused 27-tool subset (boards, monitors, assistant) instead of all 98
lgmon agent connect --tools essential
```

| What you tell your agent | What lgmon MCP does |
|---|---|
| *"Discover what's running on this PC and monitor the important ones"* | `start_here` → `explore` → `add_monitor` |
| *"Write deployment & troubleshooting docs for every service on my board"* | Inspects local configs/code → `put_monitor_doc` |
| *"Map how the web app, API gateway, Postgres, and Cloudflare tunnel connect"* | `edit_monitor` (`depends`, `role`) |
| *"The orders API is down — investigate why and give me a summary"* | `list_alerts` → `get_alert_diagnostics` |
| *"Send critical alerts to our Feishu on-call group with interactive buttons"* | Feishu setup & card-template tools |
| *"Share this board with teammates on LAN in read-only mode"* | LAN sharing & visibility tools |

The built-in **Other uses** prompt sheet includes ready-to-copy prompts for every scenario: *Watch services · Add services · Edit services & sub-services · Write / edit service docs · Map dependencies · Diagnose issues · Configure alerts · Connect Feishu · LAN sharing & team boards · Install agent skill*.

### Built-in Assistant: Bohemia (<kbd>Ctrl</kbd>+<kbd>J</kbd>)
**Bohemia (波西米亚)** lives inside every dashboard page:
- Answers questions about live status, 7-day incident trends, service docs, and how to use lgmon.
- Performs safe dashboard edits (rename services, change card styles, organize boards, acknowledge/silence alerts) and **always asks for confirmation** before deleting anything or sending external messages.
- Works in **read-only mode** for LAN web guests and supports **English & Chinese** seamlessly.

---

## Dependency Map

Switch any board to **Map** to see a live top-down architecture graph: user-facing services at the top, gateways, APIs, databases, queues, and tunnels below. **16 semantic relation types** each have a distinct line style and arrowhead:

| Relation | Meaning | Visual style |
|---|---|---|
| `uses` · `calls` · `forwards` | Direct request / call | Solid line, filled arrow |
| `data` · `writes` | Stores or writes data | Solid line, diamond |
| `reads` | Reads data | Solid line, open arrow |
| `via` | Routed through tunnel, gateway, or proxy | Dashed line |
| `runs` | Started or scheduled by | Dotted line |
| `sends` · `consumes` | Async queue / pub-sub messages | Dashed / dotted line, open arrow |
| `auth` · `monitors` | Authentication or health checks | Circle |
| `syncs` | Bidirectional sync | Double-headed arrow |
| `replicates` · `backup` | Data replication or backup | Square |
| `fallback` | Failover target | Dashed line, open arrow |
| `optional: true` | Degraded gracefully if unavailable | Thin dashed line labeled "optional" |

Healthy sub-services without external links fold neatly under their parent card and expand on demand.

---

## Screenshots

<table>
<tr>
<td width="62%"><img src="docs/images/v0.13.10/en/map.png" alt="Dependency map: 16 relation types, each with its own line and arrowhead"></td>
<td width="38%"><img src="docs/images/v0.13.10/en/window.png" alt="lgmon compact window: a one-line verdict and collapsible sub-services"></td>
</tr>
<tr>
<td align="center"><sub><b>Dependency map</b>: 16 typed relations showing blast radius at a glance</sub></td>
<td align="center"><sub><b>Compact startup window</b>: instant verdict, shared boards overview & one-click Agent handoff</sub></td>
</tr>
</table>

<table>
<tr>
<td width="50%"><img src="docs/images/v0.13.10/en/board-dark.png" alt="Service cards showing each service’s own status in dark mode"></td>
<td width="50%"><img src="docs/images/v0.13.10/en/service.png" alt="Orders API detail with 24-hour and 30-day status history"></td>
</tr>
<tr>
<td align="center"><sub><b>Dark mode cards</b>: 6 customizable card styles per service</sub></td>
<td align="center"><sub><b>Service detail</b>: 24h / 30d history, latency, sub-services, incidents & living doc</sub></td>
</tr>
</table>

---

## Quick start

### 1. Download & Install (30 seconds)
Download **`lgmon-setup-<version>.exe`** from [GitHub Releases](https://github.com/LFenX/lg-monitor/releases/latest) and run it:
- Installs to `Program Files\lg-monitor` and runs as a Windows service starting with your PC (`%ProgramData%\lg-monitor` keeps all configs & data across upgrades/uninstalls).
- Open **lgmon** from the Start menu for the compact window, or visit <http://127.0.0.1:7444/> for the full dashboard.
- Built-in self-updater verifies `SHA256SUMS` before applying new releases.

> Releases include `SHA256SUMS` and GitHub **SLSA Build Provenance Attestations**. Because the binary isn't EV code-signed yet, Windows SmartScreen may prompt on first run — choose **More info → Run anyway**.

### 2. Connect your AI Agent (or add services manually)
Click **Copy prompt** in the startup window and paste it into Claude Code, Codex, Cursor, or any MCP-capable agent — or click **Add service** in the dashboard to pick running ports, processes, Windows services, scheduled tasks, or URLs directly.

---

## Architecture

```mermaid
flowchart LR
  subgraph PC[Monitored Windows PCs]
    C[lgmon collector<br/>processes · ports · services · tasks<br/>ETW instant events · HTTP/TCP/script probes]
  end
  subgraph M[lgmon Monitor — single exe, can be same PC]
    S[(SQLite<br/>raw 24h · 1m 14d · 1h 365d)]
    R[Rules · Replay · Diagnostics]
    D[Living Docs + RAG Index]
    W[Web UI + HTTP API / SSE]
    P[MCP Server · 98 tools]
    B[Bohemia Assistant]
  end
  C -- gRPC + mTLS<br/>offline disk buffer --> S
  S --> R --> N[Feishu/Lark Cards · Windows Toast · Webhooks]
  S --> W
  D --> B
  W --> B
  W --> P --> A[AI Agents<br/>Claude Code · Codex · Cursor]
  W --> G[LAN Browser Guests<br/>zero-install read-only · scoped boards]
  W --> L[Team Peers & Departments<br/>mTLS pairing · model sharing]
```

- **Zero external dependencies**: Single Go binary (`CGO_ENABLED=0`) with embedded React + ECharts web UI and pure-Go SQLite.
- **Privacy & security first**: Process command lines are matched locally and never uploaded; secrets are encrypted with Windows DPAPI and never printed in plain text.

---

## Features at a glance

| Area | Capabilities |
|---|---|
| **Monitoring** | Websites (HTTP + TLS expiry), TCP ports, Windows services, processes (command-line + ETW instant events), scheduled tasks, [batch jobs](docs/jobs.md), script probes, [Windows Event Log](docs/collection.md), [startup & task inventory](docs/inventory.md) |
| **Dashboard & UX** | List / Card / Dependency Map views; 6 card styles; <kbd>Ctrl</kbd>+<kbd>K</kbd> command palette; Light & Dark themes; **English & 简体中文** with real-time cross-window sync; [declarative](docs/dashboards.md) & [custom HTML dashboards](docs/html-dashboards.md) |
| **Docs & AI** | Living Markdown service docs with embedded dependency diagrams; RAG search + **Bohemia** (<kbd>Ctrl</kbd>+<kbd>J</kbd>); **98 MCP tools**; one-click [diagnostic bundles](docs/diagnostics.md) |
| **Alerting** | Process/port/service/probe/metric/SQL rules; min-duration, flap merging, repeat reminders, ack, silence, [historical replay](docs/rules.md); Feishu/Lark interactive cards ([ack & mute in chat](docs/card-actions.md)), custom bots, [webhooks](docs/webhooks.md), Windows Toast |
| **Team & LAN** | Zero-install **LAN read-only browser access** with per-board visibility scope & live visitor list; peer-to-peer 6-digit pairing; nested departments; secure Bohemia model key sharing |
| **Integrations** | [Prometheus / OTLP export](docs/exports.md), OpenAPI 3.1 HTTP API, SSE live stream, [multi-monitor reporting](docs/multi-monitor.md), [restricted service restarts](docs/service-operations.md) |

---

## CLI cheat sheet

```powershell
lgmon                         # Open compact window (tray icon in background)
lgmon run --no-tray           # Run in terminal only
lgmon status                  # Services & alerts (--json for scripts)
lgmon alerts                  # Firing alerts
lgmon diagnose <alert-id>     # Generate an AI-ready diagnostic bundle
lgmon config validate         # Validate YAML configs with line numbers
lgmon feishu setup            # Connect Feishu/Lark interactively
lgmon secret set <name>       # Store an encrypted DPAPI secret
lgmon agent connect           # Print MCP setup for AI agents
```

Full reference: [CLI documentation](docs/cli.md). Schemas live in [schemas/](schemas/) and examples in [examples/](examples/).

---

## Resource usage

Measured over a 30-minute end-to-end run with **20 simulated collectors × 50 targets = 1,000 targets** (Windows 11, monitor process):

| Metric | Measured / Projected |
|---|---|
| Monitor CPU (avg) / Memory (peak) | **1.74% / 102.8 MiB** |
| Database growth (1,000 targets) | ~480 MB / hour |
| Default retention ceiling (24h raw / 14d 1m / 365d 1h) | ~32.5 GB |
| Offline disk buffer per collector | 500 MB default (~250 MB for 24h offline × 50 targets) |

A typical PC monitoring 10–50 services uses only a tiny fraction of this. See [M1 acceptance evidence](docs/acceptance-m1.md#m1-22-工程证据).

---

## Building from source

Requires Go 1.27, Git, Node 22.22.2+, and pnpm 11.21.0:

```powershell
.\scripts\build.ps1                                 # Builds bin\lgmon.exe and bin\lgmon-window.exe
.\bin\lgmon.exe --home .\dev-home                   # Run against a dev data directory (no admin needed)
.\scripts\build-installer.ps1 -Version 0.0.0-dev   # Optional: build Windows installer (Inno Setup 6.7.4)
```

Verification gates before committing:

```powershell
gofmt -l . && go vet ./... && go test ./... && pnpm --dir web test
```

---

## Documentation

| Using lgmon | Agents & Integrations | Internals |
|---|---|---|
| [Web dashboard](docs/web-panel.md) | [MCP tools (98)](docs/mcp.md) | [Product requirements](docs/PRD.md) |
| [Install & update](docs/installer.md) | [Agent guide](docs/agent-guide.md) | [Plan & execution log](docs/PLAN.md) |
| [LAN sharing & teams](docs/teams.md) | [HTTP API](docs/api.md) | [Collector transport](docs/transport.md) |
| [Rules & replay](docs/rules.md) | [Webhooks](docs/webhooks.md) | [Rollups & retention](docs/storage.md) |
| [Batch jobs](docs/jobs.md) | [Metric export](docs/exports.md) | [Collection sources](docs/collection.md) |
| [Diagnostic bundles](docs/diagnostics.md) | [CLI reference](docs/cli.md) | [Windows service](docs/windows-service.md) |

---

## License & Provenance

lgmon is released under [PolyForm Noncommercial 1.0.0](LICENSE.md) (see also [NOTICE](NOTICE)). Free for personal, academic, and nonprofit use; **commercial / for-profit use requires permission from the author**.

Built-in avatars include lgmon's original illustrations, [DiceBear Lorelei](https://www.dicebear.com/styles/lorelei/) (CC0 1.0), and [fiora](https://github.com/yinxin630/fiora) (MIT) — see `web/src/avatars/LICENSE`.

Made with care by **LFen** · [GitHub](https://github.com/LFenX/lg-monitor) · [lfen.ink](https://lfen.ink/) · [留白 margin.lfen.ink](https://margin.lfen.ink/)
