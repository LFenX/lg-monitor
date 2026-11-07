//go:build !windows

package app

import (
	"context"
	"fmt"
	"os"
)

func NewNativeTrayDriver() (TrayDriver, error) {
	return nil, fmt.Errorf("当前版本的托盘仅支持 Windows 10/11")
}

func NewTrayProtector() TrayProtector       { return nil }
func replaceTrayFile(from, to string) error { return os.Rename(from, to) }

type NativeTrayAutostart struct{}

func (NativeTrayAutostart) Set(string, bool) error {
	return fmt.Errorf("登录启动仅支持 Windows")
}

type NativeTrayOpener struct{}

func (NativeTrayOpener) Open(context.Context, string) error {
	return fmt.Errorf("当前版本的面板打开操作仅支持 Windows 10/11")
}
