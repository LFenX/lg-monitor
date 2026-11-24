// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/update"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

// lgmon update brings a Linux install to the newest release from an SSH session: download, check against the
// published SHA-256, swap the program in place and restart the service. Windows keeps its installer, run from
// the lgmon window, which also handles the start menu, the window program and the service registration.

type updateDependencies struct {
	Latest     func(ctx context.Context, feed, name string) (update.Release, error)
	Download   func(ctx context.Context, release update.Release, dir string) (string, error)
	Executable func() (string, error)
	// Verify runs the new program once before it replaces the old one.
	Verify  func(ctx context.Context, path string) error
	Service func() (serviceController, error)
	GOOS    string
	GOARCH  string
}

func defaultUpdateDependencies() updateDependencies {
	client := &http.Client{Timeout: 15 * time.Minute, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	return updateDependencies{
		Latest: func(ctx context.Context, feed, name string) (update.Release, error) {
			if name == "" {
				return update.Latest(ctx, client, feed)
			}
			return update.LatestAsset(ctx, client, feed, name)
		},
		Download: func(ctx context.Context, release update.Release, dir string) (string, error) {
			return update.Download(ctx, client, release, dir, nil)
		},
		Executable: func() (string, error) {
			path, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(path)
		},
		Verify: func(ctx context.Context, path string) error {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
			if err != nil {
				return fmt.Errorf("新程序无法运行：%s：%w", strings.TrimSpace(string(output)), err)
			}
			return nil
		},
		Service: nativeServiceController,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
}

type updateResult struct {
	Status    string `json:"status"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Updated   bool   `json:"updated"`
	Restarted bool   `json:"restarted"`
	Page      string `json:"page,omitempty"`
	Message   string `json:"message"`
}

func newUpdateCommand(deps updateDependencies) *cobra.Command {
	var check, yes, force, asJSON bool
	var feed string
	command := &cobra.Command{Use: "update", Short: "检查并更新到最新版本（Linux：下载、校验、替换程序并重启服务）", Args: noArgs, SilenceUsage: true, SilenceErrors: true, RunE: func(cmd *cobra.Command, _ []string) error {
		result := updateResult{Status: "ok", Current: version}
		finish := func(err error) error {
			if err != nil {
				result.Status, result.Message = "error", err.Error()
			}
			if asJSON {
				if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encodeErr != nil {
					return encodeErr
				}
			} else if _, printErr := fmt.Fprintln(cmd.OutOrStdout(), result.Message); printErr != nil {
				return printErr
			}
			if err != nil {
				return &reportedError{err}
			}
			return nil
		}
		asset := ""
		if deps.GOOS == "linux" {
			asset = update.LinuxAsset(deps.GOARCH)
		}
		release, err := deps.Latest(cmd.Context(), feed, asset)
		if err != nil {
			return finish(fmt.Errorf("读取最新版本失败：%w", err))
		}
		if release.Version == "" {
			result.Message = "还没有发布过版本"
			return finish(nil)
		}
		result.Latest, result.Page = release.Version, release.Page
		result.Available = update.Newer(release.Version, version) || version == "dev"
		if !result.Available && !force {
			result.Message = fmt.Sprintf("已是最新版本 %s", version)
			return finish(nil)
		}
		if check {
			result.Message = fmt.Sprintf("有新版本 %s（当前 %s）：%s；运行 lgmon update 更新", release.Version, version, release.Page)
			return finish(nil)
		}
		if deps.GOOS != "linux" {
			return finish(fmt.Errorf("Windows 上请从 lgmon 小窗口检查并安装更新：它会下载、校验并运行安装包，同时更新小窗口和服务注册。最新版本是 %s", release.Version))
		}
		if !yes {
			ok, err := confirmUpdate(cmd, release.Version)
			if err != nil {
				return finish(err)
			}
			if !ok {
				result.Message = "已取消，没有改动"
				return finish(nil)
			}
		}
		executable, err := deps.Executable()
		if err != nil {
			return finish(fmt.Errorf("找不到当前程序：%w", err))
		}
		dir := filepath.Join(os.TempDir(), "lgmon-update")
		archive, err := deps.Download(cmd.Context(), release, dir)
		if err != nil {
			return finish(err)
		}
		defer os.Remove(archive)
		if err := replaceProgram(cmd.Context(), archive, executable, deps.Verify); err != nil {
			return finish(err)
		}
		result.Updated = true
		result.Message = fmt.Sprintf("已更新到 %s（%s），旧程序保留为 %s", release.Version, executable, executable+".previous")
		if deps.Service != nil {
			if controller, err := deps.Service(); err == nil {
				if status, err := controller.Status(cmd.Context()); err == nil && status.Installed && status.State == winsvc.Running {
					restart := func() error {
						if restarter, ok := controller.(interface{ Restart(context.Context) error }); ok {
							return restarter.Restart(cmd.Context())
						}
						if err := controller.Stop(cmd.Context()); err != nil {
							return err
						}
						return controller.Start(cmd.Context())
					}
					if err := restart(); err != nil {
						return finish(fmt.Errorf("程序已更新，但重启服务失败，请运行 sudo lgmon service restart：%w", err))
					}
					result.Restarted = true
					result.Message += "；服务已重启"
				}
			}
		}
		return finish(nil)
	}}
	command.Flags().BoolVar(&check, "check", false, "只检查有没有新版本，不下载")
	command.Flags().BoolVarP(&yes, "yes", "y", false, "不再询问，直接更新（脚本和 Agent 使用）")
	command.Flags().BoolVar(&force, "force", false, "已是最新版本时也重新安装")
	command.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	command.Flags().StringVar(&feed, "feed", update.DefaultFeed, "发布信息地址")
	return command
}

func confirmUpdate(cmd *cobra.Command, latest string) (bool, error) {
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "更新到 %s 并重启 lgmon 服务？[y/N] ", latest); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
		return false, fmt.Errorf("没有终端可以确认；非交互运行时加 --yes")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// replaceProgram takes the lgmon program out of a release archive and puts it where the running one is: written
// next to it, run once, then renamed over it, so the swap is atomic and a broken download never replaces a
// working program. The old one stays as <program>.previous for rolling back by hand.
func replaceProgram(ctx context.Context, archive, executable string, verify func(context.Context, string) error) error {
	file, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("打开更新包失败：%w", err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("更新包不是有效的 tar.gz：%w", err)
	}
	reader := tar.NewReader(unzipped)
	staged := filepath.Join(filepath.Dir(executable), ".lgmon-update-"+fmt.Sprint(os.Getpid()))
	found := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取更新包失败：%w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "lgmon" {
			continue
		}
		if header.Size <= 0 || header.Size > 512<<20 {
			return fmt.Errorf("更新包里的程序大小不对")
		}
		out, err := os.OpenFile(staged, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return fmt.Errorf("没有权限写入 %s，请在命令前加 sudo：%w", filepath.Dir(executable), err)
			}
			return fmt.Errorf("写入新程序失败：%w", err)
		}
		_, copyErr := io.CopyN(out, reader, header.Size)
		syncErr := out.Sync()
		closeErr := out.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			os.Remove(staged)
			return fmt.Errorf("写入新程序失败：%w", err)
		}
		found = true
		break
	}
	if !found {
		return fmt.Errorf("更新包里没有 lgmon 程序")
	}
	if verify != nil {
		if err := verify(ctx, staged); err != nil {
			os.Remove(staged)
			return err
		}
	}
	previous := executable + ".previous"
	_ = os.Remove(previous)
	if err := os.Link(executable, previous); err != nil {
		// A filesystem without hard links still gets a copy to roll back to.
		if copyErr := copyFile(executable, previous); copyErr != nil {
			os.Remove(staged)
			return fmt.Errorf("保留旧程序失败：%w", copyErr)
		}
	}
	if err := os.Rename(staged, executable); err != nil {
		os.Remove(staged)
		return fmt.Errorf("替换程序失败：%w", err)
	}
	return nil
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(target, source)
	return errors.Join(err, target.Close())
}
