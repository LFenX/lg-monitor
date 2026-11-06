//go:build windows

package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/LFenX/lg-monitor/internal/config"

	"golang.org/x/sys/windows"
)

type captureWriter struct {
	capture *scriptCapture
	stdout  bool
}

func (w captureWriter) Write(data []byte) (int, error) {
	w.capture.write(data, w.stdout)
	return len(data), nil
}

func scriptCommandLine(systemDirectory, filename string, args []string) (string, string) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".ps1":
		shell := filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")
		return shell, windows.ComposeCommandLine(append([]string{shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filename}, args...))
	case ".cmd", ".bat":
		shell := filepath.Join(systemDirectory, "cmd.exe")
		// cmd treats metacharacters in unquoted paths as commands even without whitespace.
		inner := `"` + filename + `"`
		if len(args) != 0 {
			inner += " " + windows.ComposeCommandLine(args)
		}
		return shell, windows.ComposeCommandLine([]string{shell}) + ` /d /c "` + inner + `"`
	default:
		return filename, windows.ComposeCommandLine(append([]string{filename}, args...))
	}
}

func (r nativeScriptRunner) Run(ctx context.Context, command ScriptCommand) (output ScriptOutput, runErr error) {
	if err := ctx.Err(); err != nil {
		return output, fmt.Errorf("脚本执行已取消：%w", err)
	}
	if err := config.ValidateScript(command.Name, command.Args); err != nil {
		return output, &scriptError{err.Error(), err}
	}
	if r.home == "" || r.policy == nil {
		return output, &scriptError{message: "脚本探测需要采集器的 home 路径与权限检查器"}
	}
	home, err := filepath.Abs(r.home)
	if err != nil {
		return output, &scriptError{"解析采集器脚本目录失败", err}
	}
	scripts := filepath.Join(home, "scripts")
	filename := filepath.Join(scripts, command.Name)
	extension := strings.ToLower(filepath.Ext(filename))
	if (extension == ".cmd" || extension == ".bat") && strings.ContainsAny(filename, `%!`) {
		return output, &scriptError{message: "批处理脚本的完整目录不能包含 % 或 !，请改用不含环境变量展开字符的 home 路径"}
	}
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return output, &scriptError{fmt.Sprintf("采集器 %s 的 scripts 目录里没有 %s", r.hostname, command.Name), err}
	}
	if err != nil {
		return output, &scriptError{fmt.Sprintf("采集器 %s 无法读取脚本 %s", r.hostname, command.Name), err}
	}
	if !info.Mode().IsRegular() {
		return output, &scriptError{message: "脚本必须是普通文件，不能是链接或目录"}
	}
	for _, path := range []string{scripts, filename} {
		if err := r.policy.Check(path); err != nil {
			return output, &scriptError{fmt.Sprintf("采集器 %s 的脚本 %s 权限不安全：%s", r.hostname, command.Name, err), err}
		}
	}
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return output, fmt.Errorf("读取系统命令解释器目录失败：%w", err)
	}
	shell, line := scriptCommandLine(directory, filename, command.Args)
	executable, err := windows.UTF16PtrFromString(shell)
	if err != nil {
		return output, fmt.Errorf("解析脚本解释器失败：%w", err)
	}
	commandLine, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return output, fmt.Errorf("解析脚本命令失败：%w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return output, fmt.Errorf("创建脚本输出管道失败：%w", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return output, fmt.Errorf("创建脚本错误管道失败：%w", err)
	}
	defer stderrR.Close()
	defer stderrW.Close()
	input, err := os.Open(os.DevNull)
	if err != nil {
		return output, fmt.Errorf("打开脚本空输入失败：%w", err)
	}
	defer input.Close()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return output, fmt.Errorf("创建脚本进程组失败：%w", err)
	}
	var process windows.ProcessInformation
	var capture scriptCapture
	var readers sync.WaitGroup
	readErrors := make(chan error, 2)
	var stopCancellation func() bool
	var cancellationDone chan struct{}
	exitCode := uint32(0)
	defer func() {
		if stopCancellation != nil && !stopCancellation() {
			<-cancellationDone
		}
		// Handles identify only the process tree created by this invocation, even after PID reuse.
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
		if process.Process != 0 {
			_ = windows.TerminateProcess(process.Process, 1)
			_, _ = windows.WaitForSingleObject(process.Process, windows.INFINITE)
			_ = windows.CloseHandle(process.Process)
		}
		if process.Thread != 0 {
			_ = windows.CloseHandle(process.Thread)
		}
		_ = stdoutW.Close()
		_ = stderrW.Close()
		readers.Wait()
		close(readErrors)
		for err := range readErrors {
			if err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("读取脚本输出失败：%w", err))
			}
		}
		output = capture.output(exitCode)
	}()
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return output, fmt.Errorf("设置脚本进程组清理策略失败：%w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return output, fmt.Errorf("创建脚本句柄列表失败：%w", err)
	}
	defer attributes.Delete()
	handles := []windows.Handle{windows.Handle(input.Fd()), windows.Handle(stdoutW.Fd()), windows.Handle(stderrW.Fd())}
	for _, handle := range handles {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return output, fmt.Errorf("设置脚本标准句柄失败：%w", err)
		}
	}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return output, fmt.Errorf("限制脚本继承句柄失败：%w", err)
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE, StdInput: handles[0], StdOutput: handles[1], StdErr: handles[2]}, ProcThreadAttributeList: attributes.List()}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	workingDirectory, err := windows.UTF16PtrFromString(scripts)
	if err != nil {
		return output, fmt.Errorf("解析脚本工作目录失败：%w", err)
	}
	if err := windows.CreateProcess(executable, commandLine, nil, nil, true, flags, nil, workingDirectory, &startup.StartupInfo, &process); err != nil {
		return output, fmt.Errorf("创建脚本进程失败：%w", err)
	}
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		return output, fmt.Errorf("绑定脚本进程组失败：%w", err)
	}
	cancellationDone = make(chan struct{})
	stopCancellation = context.AfterFunc(ctx, func() {
		defer close(cancellationDone)
		if err := windows.TerminateJobObject(job, 1); err != nil {
			_ = windows.TerminateProcess(process.Process, 1)
		}
	})
	if err := ctx.Err(); err != nil {
		return output, fmt.Errorf("脚本执行已取消：%w", err)
	}
	_ = stdoutW.Close()
	_ = stderrW.Close()
	for _, pipe := range []struct {
		reader *os.File
		stdout bool
	}{{stdoutR, true}, {stderrR, false}} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			_, err := io.Copy(captureWriter{&capture, pipe.stdout}, pipe.reader)
			readErrors <- err
		}()
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		return output, fmt.Errorf("启动脚本主线程失败：%w", err)
	}
	if _, err := windows.WaitForSingleObject(process.Process, windows.INFINITE); err != nil {
		return output, fmt.Errorf("等待脚本进程失败：%w", err)
	}
	if err := windows.GetExitCodeProcess(process.Process, &exitCode); err != nil {
		return output, fmt.Errorf("读取脚本退出码失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return output, fmt.Errorf("脚本执行已取消或超时：%w", err)
	}
	return output, nil
}
