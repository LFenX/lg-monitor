//go:build windows

package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

type toastSystem interface {
	CurrentSession() (uint32, error)
	ActiveSessions() ([]uint32, error)
	Run(context.Context, uint32, string) (uint32, error)
}

type windowsToast struct{ system toastSystem }
type windowsToastSystem struct{}

// Win32 SDK: ProcThreadAttributeValue(13, FALSE, TRUE, FALSE).
const procThreadAttributeJobList = 0x0002000d

func nativeToastPlatform() ToastPlatform { return windowsToast{system: windowsToastSystem{}} }

func (p windowsToast) Deliver(ctx context.Context, content []byte) (ToastResult, error) {
	if err := ctx.Err(); err != nil {
		return ToastResult{}, fmt.Errorf("Windows 通知已取消：%w", err)
	}
	session, err := p.system.CurrentSession()
	if err != nil {
		return ToastResult{Skipped: true, Reason: "无法确定 Windows 登录会话"}, nil
	}
	if session == 0 {
		sessions, err := p.system.ActiveSessions()
		if err != nil || len(sessions) == 0 {
			return ToastResult{Skipped: true, Reason: "没有可用的 Windows 交互登录会话"}, nil
		}
		session = sessions[0]
		if session == 0 {
			return ToastResult{Skipped: true, Reason: "不能在 Windows 服务的会话零中显示通知"}, nil
		}
	}
	exit, err := p.system.Run(ctx, session, toastScript(content))
	if err != nil {
		if ctx.Err() != nil {
			return ToastResult{}, fmt.Errorf("Windows 通知已取消或超时：%w", errors.Join(ctx.Err(), err))
		}
		return ToastResult{Skipped: true, Reason: "无法以登录用户身份提交 Windows 通知，请检查会话权限和 Windows PowerShell"}, nil
	}
	if exit == 2 {
		return ToastResult{Skipped: true, Reason: "Windows 通知被系统设置禁用"}, nil
	}
	if exit != 0 {
		return ToastResult{Skipped: true, Reason: "Windows 通知提交失败，请检查 Windows 通知权限"}, nil
	}
	return ToastResult{}, nil
}

func toastScript(content []byte) string {
	// Only encoded XML enters the fixed script; alert text never becomes PowerShell code.
	return `$ErrorActionPreference = 'Stop'
try {
    $appId = 'lg-monitor'
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Classes\AppUserModelId\' + $appId)
    try {
        $key.SetValue('DisplayName', 'lg-monitor', [Microsoft.Win32.RegistryValueKind]::String)
        $key.SetValue('CustomActivator', '{A33F1C35-7294-49A8-A692-39E7FE5AD482}', [Microsoft.Win32.RegistryValueKind]::String)
    } finally { $key.Dispose() }
    [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
    [Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
    [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
    $document = New-Object Windows.Data.Xml.Dom.XmlDocument
    $document.LoadXml([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString(content) + `')))
    $notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId)
    if ([int]$notifier.Setting -ne 0) { exit 2 }
    $notifier.Show([Windows.UI.Notifications.ToastNotification]::new($document))
    exit 0
} catch { exit 3 }
`
}

func (windowsToastSystem) CurrentSession() (uint32, error) {
	var session uint32
	err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session)
	return session, err
}

func (windowsToastSystem) ActiveSessions() ([]uint32, error) {
	var rows *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &rows, &count); err != nil {
		return nil, fmt.Errorf("枚举 Windows 登录会话失败：%w", err)
	}
	if rows != nil {
		defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(rows)))
	}
	var result []uint32
	console := windows.WTSGetActiveConsoleSessionId()
	for _, row := range unsafe.Slice(rows, count) {
		if row.State == windows.WTSActive && row.SessionID != 0 {
			result = append(result, row.SessionID)
		}
	}
	slices.Sort(result)
	if index := slices.Index(result, console); index > 0 {
		result[0], result[index] = result[index], result[0]
	}
	return result, nil
}

func (s windowsToastSystem) Run(ctx context.Context, session uint32, script string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if session == 0 {
		return 0, fmt.Errorf("Windows 通知不能在服务会话零运行")
	}
	current, err := s.CurrentSession()
	if err != nil {
		return 0, err
	}
	var token windows.Token
	var environment *uint16
	if session != current {
		if current != 0 || session == 0 {
			return 0, fmt.Errorf("Windows 通知不能切换到其他用户会话")
		}
		if err := windows.WTSQueryUserToken(session, &token); err != nil {
			return 0, fmt.Errorf("取得登录用户令牌失败：%w", err)
		}
		defer token.Close()
		if err := windows.CreateEnvironmentBlock(&environment, token, false); err != nil {
			return 0, fmt.Errorf("创建登录用户环境失败：%w", err)
		}
		defer windows.DestroyEnvironmentBlock(environment)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return 0, err
	}
	shell := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	executable, err := windows.UTF16PtrFromString(shell)
	if err != nil {
		return 0, err
	}
	// Win32 hides the helper; -WindowStyle waits for a console in a headless process.
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{shell, "-NoProfile", "-NonInteractive", "-Command", script}))
	if err != nil {
		return 0, err
	}
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	directory, err := windows.UTF16PtrFromString(system)
	if err != nil {
		return 0, err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(job)
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return 0, err
	}
	count := uint32(1)
	if token == 0 {
		count++
	}
	attributes, err := windows.NewProcThreadAttributeList(count)
	if err != nil {
		return 0, err
	}
	jobs := []windows.Handle{job}
	var handles []windows.Handle
	defer func() {
		attributes.Delete()
		runtime.KeepAlive(jobs)
		runtime.KeepAlive(handles)
	}()
	if err := attributes.Update(procThreadAttributeJobList, unsafe.Pointer(&jobs[0]), unsafe.Sizeof(jobs[0])); err != nil {
		return 0, err
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Desktop: desktop, Flags: windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE}, ProcThreadAttributeList: attributes.List()}
	flags := uint32(windows.CREATE_NEW_CONSOLE | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if token == 0 {
		startup.Desktop = nil
		input, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			return 0, err
		}
		defer input.Close()
		handles = []windows.Handle{windows.Handle(input.Fd())}
		if err := windows.SetHandleInformation(handles[0], windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return 0, err
		}
		if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
			return 0, err
		}
		startup.Flags |= windows.STARTF_USESTDHANDLES
		startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[0], handles[0]
		flags = uint32(windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	}
	var process windows.ProcessInformation
	if token == 0 {
		err = windows.CreateProcess(executable, line, nil, nil, true, flags, nil, directory, &startup.StartupInfo, &process)
	} else {
		err = windows.CreateProcessAsUser(token, executable, line, nil, nil, false, flags, environment, directory, &startup.StartupInfo, &process)
	}
	if err != nil {
		return 0, fmt.Errorf("创建 Windows 通知辅助进程失败：%w", err)
	}
	defer func() {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.TerminateProcess(process.Process, 1)
		_, _ = windows.WaitForSingleObject(process.Process, windows.INFINITE)
		_ = windows.CloseHandle(process.Process)
		_ = windows.CloseHandle(process.Thread)
	}()
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		if err := windows.TerminateJobObject(job, 1); err != nil {
			_ = windows.TerminateProcess(process.Process, 1)
		}
	})
	defer func() {
		if !stop() {
			<-done
		}
	}()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if _, err := windows.WaitForSingleObject(process.Process, windows.INFINITE); err != nil {
		return 0, err
	}
	var exit uint32
	if err := windows.GetExitCodeProcess(process.Process, &exit); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return exit, nil
}
