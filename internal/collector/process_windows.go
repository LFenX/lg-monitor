package collector

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/shirou/gopsutil/v4/cpu"
	gprocess "github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/windows"
)

type nativeProcess struct {
	row   Process
	times cpu.TimesStat
}

func (nativeProcesses) LogicalProcessors() uint32 {
	return windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
}

func (s *nativeProcesses) Processes(ctx context.Context) ([]processEntry, error) {
	const maxSize = 64 << 20
	buffer := s.buffer
	var length uint32
	for size := uint32(max(1<<20, len(buffer)*8)); ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The NT structures contain 64-bit values and must remain aligned on every retry.
		if len(buffer)*8 < int(size) {
			buffer = make([]uint64, (size+7)/8)
		}
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)*8), &length)
		if err == nil {
			s.buffer = buffer
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			return nil, fmt.Errorf("读取系统进程信息失败：%w", err)
		}
		if size >= maxSize || length > maxSize-65536 {
			return nil, fmt.Errorf("系统进程信息超过 %d 字节限制", maxSize)
		}
		size = min(max(size*2, length+65536), maxSize)
	}
	if length == 0 || uint64(length) > uint64(len(buffer))*8 {
		return nil, fmt.Errorf("系统进程信息长度无效：%d", length)
	}
	entries := make([]processEntry, 0, 256)
	base := unsafe.Pointer(&buffer[0])
	for offset := uintptr(0); ; {
		if offset+unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}) > uintptr(length) {
			return nil, fmt.Errorf("系统进程信息被截断")
		}
		info := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Add(base, offset))
		p := &nativeProcess{
			row:   Process{PID: int32(info.UniqueProcessID), ParentPID: int32(info.InheritedFromUniqueProcessID), Name: info.ImageName.String(), CreatedAt: filetimeToUnixMillis(info.CreateTime), RSS: uint64(info.WorkingSetSize), Threads: int32(info.NumberOfThreads)},
			times: cpu.TimesStat{User: float64(info.UserTime) / 1e7, System: float64(info.KernelTime) / 1e7},
		}
		entries = append(entries, processEntry{pid: p.row.PID, reader: p})
		if info.NextEntryOffset == 0 {
			break
		}
		if uintptr(info.NextEntryOffset) < unsafe.Sizeof(*info) || info.NextEntryOffset%8 != 0 {
			return nil, fmt.Errorf("系统进程信息偏移无效")
		}
		offset += uintptr(info.NextEntryOffset)
	}
	runtime.KeepAlive(buffer)
	return entries, nil
}

func (p *nativeProcess) PpidWithContext(context.Context) (int32, error)  { return p.row.ParentPID, nil }
func (p *nativeProcess) NameWithContext(context.Context) (string, error) { return p.row.Name, nil }
func (p *nativeProcess) CreateTimeWithContext(context.Context) (int64, error) {
	return p.row.CreatedAt, nil
}
func (p *nativeProcess) TimesWithContext(context.Context) (*cpu.TimesStat, error) {
	return &p.times, nil
}
func (p *nativeProcess) MemoryInfoWithContext(context.Context) (*gprocess.MemoryInfoStat, error) {
	return &gprocess.MemoryInfoStat{RSS: p.row.RSS}, nil
}
func (p *nativeProcess) NumThreadsWithContext(context.Context) (int32, error) {
	return p.row.Threads, nil
}

func (p *nativeProcess) open(ctx context.Context) (windows.Handle, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.row.PID))
	if err != nil {
		return 0, err
	}
	var created, exited, kernel, user windows.Filetime
	err = windows.GetProcessTimes(handle, &created, &exited, &kernel, &user)
	if err == nil && filetimeToUnixMillis(int64(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime))) != p.row.CreatedAt {
		err = fmt.Errorf("读取详情前进程身份已变化")
	}
	if err != nil {
		windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func (p *nativeProcess) ExeWithContext(ctx context.Context) (string, error) {
	handle, err := p.open(ctx)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func (p *nativeProcess) CmdlineWithContext(ctx context.Context) (string, error) {
	handle, err := p.open(ctx)
	if err != nil {
		return "", err
	}
	// Retaining the old process handle prevents a reused PID from populating this identity's cache.
	defer windows.CloseHandle(handle)
	process := &gprocess.Process{Pid: p.row.PID}
	return process.CmdlineWithContext(ctx)
}
