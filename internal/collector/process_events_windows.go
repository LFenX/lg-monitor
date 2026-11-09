//go:build windows

package collector

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeProcessEvents struct{ now func() time.Time }

func startOwnedProcessTrace(start func() uint32) (err error) {
	etwPrivilegeMu.Lock()
	defer etwPrivilegeMu.Unlock()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_ADJUST_PRIVILEGES, &token); err != nil {
		return fmt.Errorf("读取本进程跟踪权限失败：%w", err)
	}
	defer token.Close()
	name, _ := windows.UTF16PtrFromString("SeSystemProfilePrivilege")
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return fmt.Errorf("读取进程跟踪权限标识失败：%w", err)
	}
	requested := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}}}
	var previous windows.Tokenprivileges
	var returned uint32
	if err := windows.AdjustTokenPrivileges(token, false, &requested, uint32(unsafe.Sizeof(previous)), &previous, &returned); err != nil {
		return fmt.Errorf("启用本进程已有跟踪权限失败：%w", err)
	}
	defer func() {
		if previous.PrivilegeCount != 0 {
			if failure := windows.AdjustTokenPrivileges(token, false, &previous, 0, nil, nil); failure != nil {
				err = errors.Join(err, fmt.Errorf("恢复本进程跟踪权限失败：%w", failure))
			}
		}
	}()
	if status := start(); status != 0 {
		return fmt.Errorf("创建自有 ETW 进程会话失败：%w", windows.Errno(status))
	}
	return nil
}

func (s nativeProcessEvents) Run(ctx context.Context, filter ProcessEventFilter, notify func(ProcessEvent)) (runErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOARCH != "amd64" || unsafe.Sizeof(etwTraceProperties{}) != 120 || unsafe.Sizeof(etwLogFile{}) != 448 || unsafe.Sizeof(etwRecord{}) != 112 {
		return fmt.Errorf("即时进程事件需要 Windows x64 的受支持 ETW 布局")
	}
	if notify == nil || len(filter.Names) > 512 || len(filter.PIDs) > 1024 {
		return fmt.Errorf("即时进程事件接收或筛选参数无效")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("生成自有进程会话名称失败：%w", err)
	}
	name := fmt.Sprintf("lgmon-process-%d-%s", os.Getpid(), hex.EncodeToString(random[:]))
	wide, _ := windows.UTF16FromString(name)
	buffer := make([]byte, int(unsafe.Sizeof(etwTraceProperties{}))+2*len(wide))
	properties := (*etwTraceProperties)(unsafe.Pointer(&buffer[0]))
	properties.Wnode = etwWnode{BufferSize: uint32(len(buffer)), GUID: windows.GUID{Data1: binary.LittleEndian.Uint32(random[:4]), Data2: binary.LittleEndian.Uint16(random[4:6]), Data3: binary.LittleEndian.Uint16(random[6:8]), Data4: [8]byte(random[8:])}, ClientContext: 1, Flags: 0x20000}
	properties.BufferSize, properties.MinimumBuffers, properties.MaximumBuffers = 64, 4, 16
	properties.LogFileMode = 0x100 | 0x02000000 | 0x10000000
	properties.FlushTimer, properties.EnableFlags = 1, 1
	properties.LoggerNameOffset = uint32(unsafe.Sizeof(etwTraceProperties{}))
	for i, c := range wide {
		binary.LittleEndian.PutUint16(buffer[int(properties.LoggerNameOffset)+i*2:], c)
	}
	var session uint64
	created := false
	consumer := uint64(math.MaxUint64)
	var processed <-chan uint32
	var logfile *etwLogFile
	readDone := false
	id := uintptr(etwNextID.Add(1))
	control := func(action uintptr) uint32 {
		status, _, _ := etwControl.NewProc("ControlTraceW").Call(uintptr(session), uintptr(unsafe.Pointer(&wide[0])), uintptr(unsafe.Pointer(properties)), action)
		return uint32(status)
	}
	defer func() {
		if created {
			if status := control(1); status != 0 && status != 4201 {
				runErr = errors.Join(runErr, &ProcessEventCleanupError{Session: name, Err: fmt.Errorf("停止自有 ETW 会话失败：%w", windows.Errno(status))})
			}
		}
		if consumer != math.MaxUint64 {
			status, _, _ := etwControl.NewProc("CloseTrace").Call(uintptr(consumer))
			if status != 0 && status != 7007 {
				runErr = errors.Join(runErr, &ProcessEventCleanupError{Session: name, Err: fmt.Errorf("关闭自有 ETW 消费者失败：%w", windows.Errno(status))})
			}
		}
		if processed != nil && !readDone {
			<-processed
		}
		etwActive.Delete(id)
		runtime.KeepAlive(buffer)
		runtime.KeepAlive(wide)
		runtime.KeepAlive(logfile)
	}()
	if err := startOwnedProcessTrace(func() uint32 {
		status, _, _ := etwControl.NewProc("StartTraceW").Call(uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&wide[0])), uintptr(unsafe.Pointer(properties)))
		created = status == 0
		return uint32(status)
	}); err != nil {
		return err
	}
	state := &etwProcessConsumer{ctx: ctx, now: s.now, notify: notify, filter: filter, names: make(map[string]bool), pids: make(map[int32]bool), failures: make(chan error, 1)}
	for _, name := range filter.Names {
		state.names[eventProcessName(name)] = true
	}
	for _, pid := range filter.PIDs {
		if pid > 0 {
			state.pids[pid] = true
		}
	}
	etwActive.Store(id, state)
	logfile = &etwLogFile{LoggerName: &wide[0], Mode: 0x100 | 0x10000000, EventCallback: etwCallback, Context: id}
	value, _, failure := etwControl.NewProc("OpenTraceW").Call(uintptr(unsafe.Pointer(logfile)))
	consumer = uint64(value)
	if consumer == math.MaxUint64 {
		return fmt.Errorf("打开自有 ETW 消费者失败：%w", failure)
	}
	done := make(chan uint32, 1)
	processed = done
	go func() {
		status, _, _ := etwControl.NewProc("ProcessTrace").Call(uintptr(unsafe.Pointer(&consumer)), 1, 0, 0)
		done <- uint32(status)
	}()
	notify(ProcessEvent{Kind: ProcessEventsReady, TS: s.now().UnixMilli(), Session: name})
	var lost uint64
	flush := time.NewTicker(100 * time.Millisecond)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case status := <-processed:
			readDone = true
			if status != 0 && status != 1223 {
				return fmt.Errorf("处理自有 ETW 进程流失败：%w", windows.Errno(status))
			}
			return fmt.Errorf("自有 ETW 进程流意外结束")
		case failure := <-state.failures:
			return failure
		case <-flush.C:
			if status := control(3); status != 0 {
				return fmt.Errorf("刷新自有 ETW 进程会话失败：%w", windows.Errno(status))
			}
			currentLost := uint64(properties.EventsLost) + uint64(properties.RealTimeBuffersLost) + uint64(properties.LogBuffersLost)
			if currentLost > lost {
				lost = currentLost
				notify(ProcessEvent{Kind: ProcessEventsResync, TS: s.now().UnixMilli(), Session: name})
			}
		}
	}
}
