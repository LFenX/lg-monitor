//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const connectPipePrefix = `\\.\pipe\lgmon-agent-connect-`
const connectMessageLimit = 128 * 1024

type nativeConnectElevation struct {
	launch func(string, []string) error
}

func (native nativeConnectElevation) Connect(parent context.Context, request connectRequest) (connectOutput, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return connectOutput{}, fmt.Errorf("接入已取消：%w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return connectOutput{}, fmt.Errorf("读取接入账户失败：%w", err)
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return connectOutput{}, fmt.Errorf("生成接入返回管道失败：%w", err)
	}
	pipe := connectPipePrefix + hex.EncodeToString(random[:])
	// The consent helper must not leave its token in a file or expose it to another account.
	listener, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + user.User.Sid.String() + ")"})
	if err != nil {
		return connectOutput{}, fmt.Errorf("创建受保护接入管道失败：%w", err)
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	launch := native.launch
	if launch == nil {
		launch = launchConnectConsent
	}
	if err := ctx.Err(); err != nil {
		return connectOutput{}, fmt.Errorf("接入已取消：%w", err)
	}
	if err := launch(request.Executable, []string{"agent", "connect", "--result-pipe", pipe}); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return connectOutput{}, fmt.Errorf("已取消 Windows 管理员授权，Agent 未接入；可重试或选择手动接入")
		}
		return connectOutput{}, fmt.Errorf("请求 Windows 接入授权失败：%w", err)
	}
	connection, err := listener.Accept()
	if err != nil {
		return connectOutput{}, fmt.Errorf("等待接入授权结果失败：%w", err)
	}
	defer connection.Close()
	stopConnection := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopConnection()
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		return connectOutput{}, fmt.Errorf("设置接入等待时间失败：%w", err)
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return connectOutput{}, fmt.Errorf("传递接入请求失败：%w", err)
	}
	var result connectOutput
	if err := json.NewDecoder(io.LimitReader(connection, connectMessageLimit)).Decode(&result); err != nil {
		return connectOutput{}, fmt.Errorf("读取接入授权结果失败：%w", err)
	}
	if result.Status != "ok" {
		return connectOutput{}, fmt.Errorf("管理员接入未完成：%s", result.Message)
	}
	if result.Scope != api.Scope(request.Scope) || result.Token == "" || result.TokenID == "" || !strings.EqualFold(filepath.Clean(result.Home), filepath.Clean(request.Home)) || !strings.EqualFold(filepath.Clean(result.Executable), filepath.Clean(request.Executable)) || (request.URL != "" && result.URL != request.URL) {
		return connectOutput{}, fmt.Errorf("管理员接入结果与本次请求不匹配")
	}
	if request.Tools != "" && result.Tools != request.Tools {
		return connectOutput{}, fmt.Errorf("管理员接入返回的工具集与本次请求不匹配")
	}
	return result, nil
}

func launchConnectConsent(executable string, args []string) error {
	quoted := make([]string, len(args))
	for index, argument := range args {
		quoted[index] = syscall.EscapeArg(argument)
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	parameters, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, parameters, nil, windows.SW_HIDE)
}

func serveConnectElevation(parent context.Context, pipe string, work func(connectRequest) connectOutput) error {
	if !strings.HasPrefix(pipe, connectPipePrefix) || len(pipe) != len(connectPipePrefix)+48 {
		return fmt.Errorf("接入返回管道无效")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(pipe, connectPipePrefix)); err != nil {
		return fmt.Errorf("接入返回管道无效：%w", err)
	}
	admin, err := winsvc.IsAdministrator()
	if err != nil || !admin {
		return fmt.Errorf("接入辅助进程需要本人确认 Windows 管理员授权")
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	connection, err := winio.DialPipeContext(ctx, pipe)
	if err != nil {
		return fmt.Errorf("连接接入返回管道失败：%w", err)
	}
	defer connection.Close()
	stopConnection := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopConnection()
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("设置接入返回时间失败：%w", err)
	}
	var request connectRequest
	decoder := json.NewDecoder(io.LimitReader(connection, connectMessageLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("读取接入请求失败：%w", err)
	}
	if request.Scope != "config" && request.Scope != "read" {
		return fmt.Errorf("接入授权仅支持 config 或 read")
	}
	if request.Tools != "essential" && request.Tools != "all" {
		return fmt.Errorf("接入工具集仅支持 essential 或 all")
	}
	if err := json.NewEncoder(connection).Encode(work(request)); err != nil {
		return fmt.Errorf("返回接入结果失败：%w", err)
	}
	return nil
}
