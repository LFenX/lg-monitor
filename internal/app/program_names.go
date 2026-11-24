// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

// nameProgram gives the device page's program rows the names people know them by: one the owner set, else the
// monitored service whose process match fits the row. Matching and alerts keep using the process itself.
func nameProgram(cfg *config.Config, rows []collector.ProcessUsage) []collector.ProcessUsage {
	for index := range rows {
		row := &rows[index]
		row.DisplayName = ""
		if at := slices.IndexFunc(cfg.Settings.ProgramNames, func(item config.ProgramName) bool { return item.Detail != "" && item.Matches(row.Name, row.Detail) }); at >= 0 {
			row.DisplayName = cfg.Settings.ProgramNames[at].Name
			continue
		}
		if at := slices.IndexFunc(cfg.Settings.ProgramNames, func(item config.ProgramName) bool { return item.Detail == "" && item.Matches(row.Name, row.Detail) }); at >= 0 {
			row.DisplayName = cfg.Settings.ProgramNames[at].Name
			continue
		}
		row.DisplayName = serviceProgramName(cfg.Targets, row.Name, row.Detail)
	}
	return rows
}

// serviceProgramName is the one monitored service matched by process name whose command line pattern, if it has
// one, fits the row's script or module. Two services that both fit give no name rather than a wrong one.
func serviceProgramName(targets []config.Target, process, detail string) string {
	name := ""
	for _, target := range targets {
		match := target.Match
		if match.Process == "" || match.Port != nil || match.Service != "" || !(config.ProgramName{Process: match.Process}).Matches(process, "") {
			continue
		}
		if match.Cmdline != "" {
			pattern, err := regexp.Compile("(?i)" + match.Cmdline)
			if err != nil || detail == "" || !pattern.MatchString(detail) {
				continue
			}
		} else if detail != "" {
			// "node" alone says nothing about which of several node programs this row is.
			continue
		}
		if name != "" {
			return ""
		}
		name = firstOf(target.Name, target.ID)
	}
	return name
}

func (b *APIBackend) putProgramName(ctx context.Context, input api.ProgramNameInput) ([]config.ProgramName, error) {
	input.Process, input.Detail, input.Name = strings.TrimSpace(input.Process), strings.TrimSpace(input.Detail), strings.TrimSpace(input.Name)
	if input.Process == "" || utf8.RuneCountInString(input.Process) > 80 || utf8.RuneCountInString(input.Detail) > 80 || utf8.RuneCountInString(input.Name) > 40 {
		return nil, invalidAPI("需要进程名（最多 80 字），显示名最多 40 字，脚本或模块最多 80 字")
	}
	var saved []config.ProgramName
	_, err := b.options.Editor.Update(ctx, "settings.yaml", func(old []byte) ([]byte, error) {
		latest, err := config.ParseSettings(old)
		if err != nil {
			return nil, err
		}
		names := slices.DeleteFunc(slices.Clone(latest.ProgramNames), func(item config.ProgramName) bool {
			return strings.EqualFold(item.Process, input.Process) && strings.EqualFold(item.Detail, input.Detail)
		})
		if input.Name != "" {
			names = append(names, config.ProgramName{Name: input.Name, Process: input.Process, Detail: input.Detail})
		}
		saved = names
		return config.SetProgramNames(old, names)
	})
	if err != nil {
		return nil, err
	}
	return append([]config.ProgramName{}, saved...), nil
}
