//go:build !windows

package main

import (
	"context"
	"fmt"
)

func openBrowser(context.Context, string) error {
	return fmt.Errorf("当前系统请手动打开飞书权限链接")
}
