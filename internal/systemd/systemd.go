// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package systemd runs lgmon as a systemd service on Linux, the counterpart of the Windows service in winsvc.
package systemd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LFenX/lg-monitor/internal/winsvc"
)

const (
	Unit     = "lgmon.service"
	UnitPath = "/etc/systemd/system/" + Unit
)

// Runner runs systemctl; tests replace it.
type Runner func(ctx context.Context, args ...string) (string, error)

// Files writes and removes the unit file; tests replace it.
type Files interface {
	Write(path string, data []byte) error
	Remove(path string) error
}

type osFiles struct{}

func (osFiles) Write(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (osFiles) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

type Controller struct {
	Run   Runner
	Files Files
	Root  func() bool
}

// Native is the controller for the machine it runs on.
func Native() (*Controller, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, fmt.Errorf("没有找到 systemctl：这台机器没有使用 systemd，请用 lgmon run 在前台运行，或交给你自己的进程管理器")
	}
	return &Controller{Run: systemctl, Files: osFiles{}, Root: func() bool { return os.Geteuid() == 0 }}, nil
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "systemctl", args...)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	text := strings.TrimSpace(output.String())
	if err != nil {
		if text == "" {
			return text, fmt.Errorf("systemctl %s 失败：%w", strings.Join(args, " "), err)
		}
		return text, fmt.Errorf("systemctl %s 失败：%s：%w", strings.Join(args, " "), text, err)
	}
	return text, nil
}

func (c *Controller) needRoot() error {
	if c.Root != nil && !c.Root() {
		return fmt.Errorf("管理 lgmon 服务需要 root 权限，请在命令前加 sudo")
	}
	return nil
}

// quote writes one ExecStart argument the way systemd reads it: double quotes, with backslashes, quotes and
// percent signs (systemd specifiers) escaped.
func quote(value string) string {
	value = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(value)
	return `"` + value + `"`
}

// UnitFile is the service definition: root, restarted ten seconds after a failure, started at boot once enabled.
func UnitFile(def winsvc.Definition) string {
	args := []string{quote(def.Executable)}
	for _, arg := range def.Arguments() {
		args = append(args, quote(arg))
	}
	return strings.Join([]string{
		"# Written by lgmon install; lgmon uninstall removes it.",
		"[Unit]",
		"Description=lg-monitor (lgmon) service monitor",
		"Documentation=https://github.com/LFenX/lg-monitor",
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + strings.Join(args, " "),
		"Environment=LGMON_SERVICE=1",
		"Restart=on-failure",
		"RestartSec=10",
		"KillMode=mixed",
		"TimeoutStopSec=30",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func (c *Controller) Install(ctx context.Context, def winsvc.Definition) error {
	if err := c.needRoot(); err != nil {
		return err
	}
	absolute := func(value string) bool { return filepath.IsAbs(value) || path.IsAbs(value) }
	if !absolute(def.Executable) || !absolute(def.Home) {
		return fmt.Errorf("程序路径和数据目录都必须是绝对路径")
	}
	if err := os.MkdirAll(def.Home, 0o700); err != nil {
		return fmt.Errorf("创建数据目录失败：%w", err)
	}
	if err := c.Files.Write(UnitPath, []byte(UnitFile(def))); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", UnitPath, err)
	}
	if _, err := c.Run(ctx, "daemon-reload"); err != nil {
		return err
	}
	_, err := c.Run(ctx, "enable", Unit)
	return err
}

func (c *Controller) Uninstall(ctx context.Context) error {
	if err := c.needRoot(); err != nil {
		return err
	}
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if status.Installed {
		if _, err := c.Run(ctx, "disable", "--now", Unit); err != nil {
			return err
		}
	}
	if err := c.Files.Remove(UnitPath); err != nil {
		return fmt.Errorf("删除 %s 失败：%w", UnitPath, err)
	}
	_, err = c.Run(ctx, "daemon-reload")
	return err
}

func (c *Controller) Start(ctx context.Context) error {
	if err := c.needRoot(); err != nil {
		return err
	}
	_, err := c.Run(ctx, "start", Unit)
	return err
}

func (c *Controller) Stop(ctx context.Context) error {
	if err := c.needRoot(); err != nil {
		return err
	}
	_, err := c.Run(ctx, "stop", Unit)
	return err
}

func (c *Controller) Restart(ctx context.Context) error {
	if err := c.needRoot(); err != nil {
		return err
	}
	_, err := c.Run(ctx, "restart", Unit)
	return err
}

// Status reads the unit without root, so anyone on the machine can see whether lgmon runs.
func (c *Controller) Status(ctx context.Context) (winsvc.Status, error) {
	text, err := c.Run(ctx, "show", Unit, "--property=LoadState,ActiveState,MainPID")
	if err != nil {
		return winsvc.Status{}, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			values[key] = value
		}
	}
	status := winsvc.Status{Installed: values["LoadState"] != "" && values["LoadState"] != "not-found", State: winsvc.Stopped}
	switch values["ActiveState"] {
	case "active", "reloading":
		status.State = winsvc.Running
	case "activating":
		status.State = winsvc.StartPending
	case "deactivating":
		status.State = winsvc.StopPending
	}
	if pid, err := strconv.ParseUint(values["MainPID"], 10, 32); err == nil {
		status.PID = uint32(pid)
	}
	return status, nil
}
