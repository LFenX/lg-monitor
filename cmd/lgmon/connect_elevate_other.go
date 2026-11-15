//go:build !windows

package main

import (
	"context"
	"fmt"
)

type nativeConnectElevation struct{}

func (nativeConnectElevation) Connect(context.Context, connectRequest) (connectOutput, error) {
	return connectOutput{}, fmt.Errorf("自动管理员授权仅支持 Windows；请由有权账户运行接入命令")
}

func serveConnectElevation(context.Context, string, func(connectRequest) connectOutput) error {
	return fmt.Errorf("本机接入授权管道仅支持 Windows")
}
