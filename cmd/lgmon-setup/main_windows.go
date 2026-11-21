//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/LFenX/lg-monitor/internal/desktop"
	"github.com/LFenX/lg-monitor/internal/update"
	"github.com/LFenX/lg-monitor/internal/webwindow"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	className = "lgmonSetup"
	// uninstallKey is where Inno Setup records the installed version, under lgmon.iss's AppId.
	uninstallKey   = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{39B64C4F-BFE7-46A2-9201-217A293487D1}_is1`
	createNoWindow = 0x08000000
)

var (
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
	procMultiByteToWide = kernel32.NewProc("MultiByteToWideChar")
)

// shellExecuteInfo is SHELLEXECUTEINFOW; Go's alignment matches the 64-bit layout.
type shellExecuteInfo struct {
	Size          uint32
	Mask          uint32
	Window        uintptr
	Verb          *uint16
	File          *uint16
	Parameters    *uint16
	Directory     *uint16
	Show          int32
	InstApp       uintptr
	IDList        uintptr
	Class         *uint16
	ClassKey      uintptr
	HotKey        uint32
	IconOrMonitor uintptr
	Process       windows.Handle
}

func findInstalled() *installed {
	definition, ok, err := winsvc.InstalledDefinition(context.Background())
	if err != nil {
		return nil
	}
	found := &installed{Home: definition.Home, Role: definition.Role, Listen: definition.Listen, Monitor: definition.Monitor}
	if !ok {
		if found = leftInstall(programDir()); found == nil {
			return nil
		}
	}
	if key, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		found.Version, _, _ = key.GetStringValue("DisplayVersion")
		key.Close()
	}
	return found
}

func programDir() string {
	root := os.Getenv("ProgramW6432")
	if root == "" {
		root = os.Getenv("ProgramFiles")
	}
	return filepath.Join(root, "lg-monitor")
}

func defaultHome() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "lg-monitor")
}

// keep fills an update's choice from the installed lgmon, unless the command line already named it.
func keep(c choice, found *installed) choice {
	if found == nil {
		return c
	}
	if c.Role == "" {
		c.Role = found.Role
	}
	if c.Home == "" {
		c.Home = found.Home
	}
	if c.Listen == "" {
		c.Listen = found.Listen
	}
	if c.Monitor == "" {
		c.Monitor = found.Monitor
	}
	return c
}

// prepareEngine is the Inno Setup installer to run: the one named on the command line, or the embedded one
// written to a fresh temporary folder.
func prepareEngine(work, named string) (string, error) {
	if named != "" {
		return named, nil
	}
	if len(engine) == 0 {
		return "", fmt.Errorf("这个 lgmon-setup 没有内嵌安装程序，请用 --engine 指定")
	}
	path := filepath.Join(work, "lgmon-engine.exe")
	if err := os.WriteFile(path, engine, 0o700); err != nil {
		return "", fmt.Errorf("准备安装程序失败：%w", err)
	}
	return path, nil
}

// launch starts program through the shell, so its own manifest decides whether Windows asks for administrator
// rights, and returns the process to wait for.
func launch(program string, args []string, show int32) (windows.Handle, error) {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = syscall.EscapeArg(arg)
	}
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(program)
	parameters, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: file, Parameters: parameters, Show: show} // NOCLOSEPROCESS, NOASYNC
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return 0, err
	}
	return info.Process, nil
}

// readLog returns the engine's log as text; Inno writes UTF-8, and older builds the system code page.
func readLog(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	if utf8.Valid(data) {
		return string(data)
	}
	size, _, _ := procMultiByteToWide.Call(0, 0, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0, 0)
	if size == 0 {
		return string(data)
	}
	wide := make([]uint16, size)
	procMultiByteToWide.Call(0, 0, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&wide[0])), size)
	return windows.UTF16ToString(wide)
}

// install runs the engine silently and reports each new stage; it returns the engine's exit code.
func install(enginePath string, c choice, log string, report func(stage)) (uint32, error) {
	process, err := launch(enginePath, engineArgs(c, log), 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(process)
	last := stage{}
	for {
		event, _ := windows.WaitForSingleObject(process, 250)
		if current := progress(readLog(log)); current != last && report != nil {
			last = current
			report(current)
		}
		if event != uint32(windows.WAIT_TIMEOUT) {
			break
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		return 0, err
	}
	return code, nil
}

// logs hands out one log file per engine run. They live under %LOCALAPPDATA%\lg-monitor\setup rather than in the
// window's temporary folder, which is deleted when the window closes and took the reason for a failure with it.
type logs struct {
	mu        sync.Mutex
	directory string
	current   string
}

func newLogs(work string) *logs {
	l := &logs{directory: work}
	if root := os.Getenv("LOCALAPPDATA"); root != "" {
		if directory := filepath.Join(root, "lg-monitor", "setup"); os.MkdirAll(directory, 0o700) == nil {
			l.directory = directory
		}
	}
	return l
}

// next is the log for a new attempt.
func (l *logs) next() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current = attemptLog(l.directory, time.Now(), 10)
	return l.current
}

// latest is the last attempt's log, or the folder when nothing has run yet.
func (l *logs) latest() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current == "" {
		return l.directory
	}
	return l.current
}

// openLgmon opens the installed lgmon for this person: its window, started by lgmon.exe without a command.
func openLgmon(home string) error {
	program := filepath.Join(programDir(), "lgmon.exe")
	args := []string{}
	if home != "" {
		args = append(args, "--home", home)
	}
	command := exec.Command(program, args...)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := command.Start(); err != nil {
		return fmt.Errorf("没有打开 lgmon：%w", err)
	}
	return command.Process.Release()
}

func run(args []string, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	work, err := os.MkdirTemp("", "lgmon-setup-")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(work)
	enginePath, err := prepareEngine(work, opts.engine)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	found := findInstalled()
	if opts.update {
		opts.choice = keep(opts.choice, found)
	}
	logs := newLogs(work)
	if opts.silent {
		log := logs.next()
		code, err := install(enginePath, opts.choice, log, nil)
		if err != nil {
			fmt.Fprintln(stderr, "安装程序没有运行：", err)
			return 1
		}
		if code != 0 {
			fmt.Fprintln(stderr, failure(readLog(log), code))
			fmt.Fprintln(stderr, "安装日志："+log)
		}
		return int(code)
	}
	if webwindow.FocusExisting(className) {
		return 0
	}
	// Without WebView2 the engine's own wizard does the whole job.
	if !webwindow.Available() {
		return wizard(enginePath, stderr)
	}
	state := map[string]any{
		"version": version, "update": opts.update, "program": programDir(),
		"defaults": map[string]string{"home": defaultHome(), "listen": "127.0.0.1:7443"},
	}
	if found != nil {
		state["installed"] = found
		state["older"] = update.Newer(version, found.Version)
	}
	encoded, _ := json.Marshal(state)
	title := "安装 lgmon"
	if opts.update {
		title = "更新 lgmon"
	}
	err = webwindow.Run(webwindow.Options{
		ClassName: className, Title: title, Width: 560, Height: 500, Dialog: true,
		DataDir: filepath.Join(work, "webview"), HTML: page, Script: "window.lgmonSetup = " + string(encoded) + ";",
		OnMessage: func(window *webwindow.Window, text string) {
			var message struct {
				Type   string `json:"type"`
				Choice choice `json:"choice"`
			}
			if json.Unmarshal([]byte(text), &message) != nil {
				return
			}
			switch message.Type {
			case "install":
				c := message.Choice
				if c.Upgrade {
					c = keep(choice{Upgrade: true}, found)
				}
				if opts.update {
					c = opts.choice
				}
				go func() {
					log := logs.next()
					code, err := install(enginePath, c, log, func(current stage) {
						window.Send(map[string]any{"type": "progress", "percent": current.Percent, "step": current.Step})
					})
					switch {
					case errors.Is(err, windows.ERROR_CANCELLED):
						window.Send(map[string]any{"type": "failed", "message": "需要管理员授权才能注册 lgmon 服务。点“重试”，在弹出的窗口里选“是”。"})
					case err != nil:
						window.Send(map[string]any{"type": "failed", "message": "安装程序没有运行：" + err.Error()})
					case code != 0:
						window.Send(map[string]any{"type": "failed", "message": failure(readLog(log), code)})
					default:
						window.Send(map[string]any{"type": "done", "update": opts.update})
						if opts.update {
							time.Sleep(1500 * time.Millisecond)
							_ = openLgmon(c.Home)
							window.Close()
						}
					}
				}()
			case "wizard":
				// Pairing a collector needs the one-time code and fingerprint; the engine's wizard asks for them.
				wizard := []string{"/LGMONROLE=collector"}
				if message.Choice.Home != "" {
					wizard = append(wizard, "/LGMONHOME="+message.Choice.Home)
				}
				if process, err := launch(enginePath, wizard, 1); err == nil {
					go func() {
						_, _ = windows.WaitForSingleObject(process, windows.INFINITE)
						windows.CloseHandle(process)
						window.Close()
					}()
				}
			case "open":
				home := ""
				if found != nil {
					home = found.Home
				}
				if c := keep(choice{}, findInstalled()); c.Home != "" {
					home = c.Home
				}
				if err := openLgmon(home); err != nil {
					window.Send(map[string]any{"type": "failed", "message": err.Error()})
					return
				}
				window.Close()
			case "log":
				_ = desktop.ShellOpen(logs.latest())
			case "close":
				window.Close()
			}
		},
	})
	if errors.Is(err, webwindow.ErrNoWebView) {
		// The runtime is there but broken for this user; the wizard still installs everything.
		return wizard(enginePath, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// wizard runs the engine's own lgmon-styled wizard and waits for it.
func wizard(enginePath string, stderr io.Writer) int {
	process, err := launch(enginePath, nil, 1)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = windows.WaitForSingleObject(process, windows.INFINITE)
	windows.CloseHandle(process)
	return 0
}
