package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"syscall"
)

func openBrowser(ctx context.Context, address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || (u.Host != "open.feishu.cn" && u.Host != "open.larksuite.com") {
		return fmt.Errorf("浏览器跳转只支持飞书或 Lark 开发者后台 HTTPS 链接")
	}
	command := exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", address)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Run(); err != nil {
		return fmt.Errorf("打开开发者后台失败：%w", err)
	}
	return nil
}
