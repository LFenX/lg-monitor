package collector

import (
	"context"
	"fmt"

	gprocess "github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/windows"
)

func (nativeWorkingDirectoryReader) ReadDirectory(ctx context.Context, identity ProcessIdentity) (string, error) {
	process := &nativeProcess{row: Process{PID: identity.PID, CreatedAt: identity.CreatedAt}}
	handle, err := process.open(ctx)
	if err != nil {
		return "", fmt.Errorf("打开进程身份失败：%w", err)
	}
	// Retaining this process object prevents PID reuse while gopsutil reads either PEB layout.
	defer windows.CloseHandle(handle)
	reader := &gprocess.Process{Pid: identity.PID}
	return reader.CwdWithContext(ctx)
}
