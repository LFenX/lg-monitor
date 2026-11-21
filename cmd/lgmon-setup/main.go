// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Command lgmon-setup is the installer people see: one window with lgmon's mark and an "安装" button, then a
// progress bar, then "打开 lgmon". Underneath it runs the Inno Setup installer silently, which keeps every
// install, pairing, upgrade and uninstall rule it already has.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/winsvc"
)

// version is set at build time to the release being installed.
var version = "dev"

//go:embed page.html
var page string

// choice is what the person picked, or what an update keeps from the installed lgmon.
type choice struct {
	Role    string `json:"role"`
	Home    string `json:"home"`
	Listen  string `json:"listen"`
	Monitor string `json:"monitor"`
	Upgrade bool   `json:"upgrade"`
}

type options struct {
	silent, update bool
	engine         string
	choice         choice
}

// installed is the lgmon already on this computer, as its service and Inno Setup record it.
type installed struct {
	Version string `json:"version"`
	Home    string `json:"home"`
	Role    string `json:"role"`
	Listen  string `json:"listen"`
	Monitor string `json:"monitor"`
	// Incomplete means the program is there but its service is not: an install or update stopped part way, or
	// the service was removed by hand. Finishing it is an update; a fresh install would refuse the program.
	Incomplete bool `json:"incomplete,omitempty"`
}

// leftInstall is the installation in directory whose service is gone, with what its setup.ini still records.
// Without setup.ini the engine falls back to its defaults, and its own check decides whether to take it over.
func leftInstall(directory string) *installed {
	if info, err := os.Stat(filepath.Join(directory, "lgmon.exe")); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	metadata, _ := winsvc.ReadSetupMetadata(directory)
	return &installed{Home: metadata.Home, Role: metadata.Role, Listen: metadata.Listen, Monitor: metadata.Monitor, Incomplete: true}
}

// attemptLog is a new log file for one run of the engine in directory, keeping the keep-1 newest earlier ones.
// Every attempt gets its own file: a retry used to overwrite the log of the attempt that actually failed.
func attemptLog(directory string, now time.Time, keep int) string {
	if earlier, err := filepath.Glob(filepath.Join(directory, "install-*.log")); err == nil && len(earlier) >= keep {
		sort.Strings(earlier)
		for _, old := range earlier[:len(earlier)-keep+1] {
			_ = os.Remove(old)
		}
	}
	return filepath.Join(directory, "install-"+now.Format("20060102-150405.000")+".log")
}

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("lgmon-setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&opts.silent, "silent", false, "不显示窗口，直接安装（自动化和测试用）")
	flags.BoolVar(&opts.update, "update", false, "沿用已安装的角色和数据目录更新到这个版本，完成后重新打开 lgmon")
	flags.StringVar(&opts.engine, "engine", "", "使用这个 Inno 安装程序，而不是内嵌的（测试用）")
	flags.StringVar(&opts.choice.Role, "role", "", "both、monitor 或 collector")
	flags.StringVar(&opts.choice.Home, "home", "", "数据目录")
	flags.StringVar(&opts.choice.Listen, "listen", "", "接收采集器的地址 host:port")
	flags.StringVar(&opts.choice.Monitor, "monitor", "", "只采集时，监控器的 host:port")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if role := opts.choice.Role; role != "" && role != "both" && role != "monitor" && role != "collector" {
		return opts, fmt.Errorf("--role 必须是 both、monitor 或 collector")
	}
	opts.choice.Upgrade = opts.update
	return opts, nil
}

// engineArgs are the Inno Setup command-line arguments for a silent install of c that logs to log.
func engineArgs(c choice, log string) []string {
	role := c.Role
	if role == "" {
		role = "both"
	}
	args := []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/LGMONROLE=" + role, "/LGMONCONFIRM", "/LOG=" + log}
	if c.Home != "" {
		args = append(args, "/LGMONHOME="+c.Home)
	}
	if role == "collector" {
		if c.Monitor != "" {
			args = append(args, "/LGMONMONITOR="+c.Monitor)
		}
	} else if c.Listen != "" {
		args = append(args, "/LGMONLISTEN="+c.Listen)
	}
	if c.Upgrade {
		args = append(args, "/LGMONUPGRADE")
	}
	return args
}

// stage is how far the engine has got, read from its log; the page eases between stages.
type stage struct {
	Percent int    `json:"percent"`
	Step    string `json:"step"`
}

// progress reads the Inno Setup log written so far. The lgmon-stage lines come from lgmon.iss.
func progress(log string) stage {
	current := stage{Percent: 3, Step: "正在启动安装程序"}
	files := 0
	for _, line := range strings.Split(log, "\n") {
		switch {
		case strings.Contains(line, "Setup version:"):
			current = stage{6, "正在准备"}
		case strings.Contains(line, "lgmon-stage: check"):
			current = stage{16, "正在检查这台电脑"}
		case strings.Contains(line, "lgmon-stage: upgrade"):
			current = stage{22, "正在停下旧版本"}
		case strings.Contains(line, "Starting the installation process."):
			current = stage{30, "正在复制文件"}
		case strings.Contains(line, "Dest filename:"):
			files++
			current = stage{min(30+files*6, 54), "正在复制文件"}
		case strings.Contains(line, "lgmon-stage: service"):
			current = stage{60, "正在注册并启动 lgmon 服务"}
		case strings.Contains(line, "Installation process succeeded."):
			current = stage{96, "马上就好"}
		}
	}
	return current
}

// failure is the reason worth showing from a failed engine's log, or a plain one for its exit code.
func failure(log string, code uint32) string {
	reason := ""
	for _, line := range strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n") {
		for _, marker := range []string{"Exception message:", "PrepareToInstall failed:", "Message box (OK):"} {
			if index := strings.Index(line, marker); index >= 0 {
				if text := strings.TrimSpace(line[index+len(marker):]); text != "" {
					reason = text
				}
			}
		}
	}
	if reason != "" {
		return reason
	}
	switch code {
	case 2, 5:
		return "安装被取消了，什么都没有改动。"
	case 7:
		return "安装前的检查没有通过，什么都没有改动。"
	}
	return "安装没有完成（代码 " + strconv.FormatUint(uint64(code), 10) + "），配置和数据都还在。"
}
