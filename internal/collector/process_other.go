//go:build !windows

package collector

import (
	"context"

	gprocess "github.com/shirou/gopsutil/v4/process"
)

func (nativeProcesses) Processes(ctx context.Context) ([]processEntry, error) {
	processes, err := gprocess.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]processEntry, 0, len(processes))
	for _, process := range processes {
		entries = append(entries, processEntry{pid: process.Pid, reader: process})
	}
	return entries, nil
}
