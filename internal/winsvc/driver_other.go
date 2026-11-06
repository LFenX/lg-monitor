//go:build !windows

package winsvc

import "fmt"

func NativeController() (*Controller, error) {
	return nil, fmt.Errorf("Windows 服务管理仅支持 Windows")
}
