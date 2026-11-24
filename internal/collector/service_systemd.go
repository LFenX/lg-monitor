// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type systemctlRunner func(ctx context.Context, args ...string) (string, error)

func execSystemctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		if text == "" {
			return text, fmt.Errorf("systemctl %s 失败：%w", strings.Join(args, " "), err)
		}
		return text, fmt.Errorf("systemctl %s 失败：%s：%w", strings.Join(args, " "), text, err)
	}
	return text, nil
}

// systemdUnitName normalizes a configured service name into a systemd .service unit name.
func systemdUnitName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, ".") {
		return trimmed
	}
	return trimmed + ".service"
}

type systemdSession struct {
	run systemctlRunner
}

func (s *systemdSession) Close() error { return nil }

func (s *systemdSession) Names(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var names []string
	addLine := func(line string) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return
		}
		unit := strings.TrimSpace(fields[0])
		if !strings.HasSuffix(unit, ".service") {
			return
		}
		// Skip template units like getty@.service that cannot be queried without an instance.
		if strings.Contains(unit, "@.service") {
			return
		}
		name := strings.TrimSuffix(unit, ".service")
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		names = append(names, name)
	}
	if out, err := s.run(ctx, "list-unit-files", "--type=service", "--no-legend", "--no-pager"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			addLine(line)
		}
	} else {
		return nil, err
	}
	if out, err := s.run(ctx, "list-units", "--type=service", "--all", "--no-legend", "--no-pager"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			// Active/failed lines may begin with a bullet symbol in some locales; strip non-unit prefix.
			line = strings.TrimLeft(line, " \t●*")
			addLine(line)
		}
	}
	return names, nil
}

func (s *systemdSession) Read(ctx context.Context, name string) (Service, error) {
	row := Service{Name: name, DisplayName: name, State: ServiceUnknown, StartType: "unknown"}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	unit := systemdUnitName(name)
	if unit == "" {
		return row, errServiceMissing
	}
	out, err := s.run(ctx, "show", unit, "--property=LoadState,ActiveState,UnitFileState,MainPID,Description", "--no-pager")
	if err != nil {
		return row, err
	}
	return parseSystemdService(name, out)
}

func parseSystemdService(name, showOutput string) (Service, error) {
	row := Service{Name: name, DisplayName: name, State: ServiceUnknown, StartType: "unknown"}
	props := make(map[string]string, 5)
	for _, line := range strings.Split(showOutput, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	loadState := props["LoadState"]
	if loadState == "" || loadState == "not-found" {
		return row, errServiceMissing
	}
	if desc := strings.TrimSpace(props["Description"]); desc != "" {
		row.DisplayName = desc
	}
	if pid, err := strconv.ParseInt(props["MainPID"], 10, 32); err == nil && pid >= 0 {
		row.PID = int32(pid)
	}
	switch props["ActiveState"] {
	case "active", "reloading":
		row.State = ServiceRunning
	case "activating":
		row.State = "start_pending"
	case "deactivating":
		row.State = "stop_pending"
	case "inactive", "failed", "maintenance":
		row.State = ServiceStopped
	default:
		row.State = ServiceUnknown
	}
	switch props["UnitFileState"] {
	case "enabled", "enabled-runtime":
		row.StartType = "automatic"
	case "disabled", "masked", "masked-runtime":
		row.StartType = "disabled"
	case "static", "indirect", "generated", "transient":
		row.StartType = "manual"
	default:
		row.StartType = "unknown"
	}
	return row, nil
}

type systemdRestartHandle struct {
	ctx  context.Context
	unit string
	run  systemctlRunner
}

func (h *systemdRestartHandle) State() (string, error) {
	out, err := h.run(h.ctx, "show", h.unit, "--property=ActiveState", "--value", "--no-pager")
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(out) {
	case "active", "reloading":
		return "running", nil
	case "inactive", "failed", "maintenance":
		return "stopped", nil
	default:
		return "pending", nil
	}
}

func (h *systemdRestartHandle) Stop() error {
	_, err := h.run(h.ctx, "stop", h.unit)
	return err
}

func (h *systemdRestartHandle) Start() error {
	_, err := h.run(h.ctx, "start", h.unit)
	return err
}

func (h *systemdRestartHandle) Close() error { return nil }
