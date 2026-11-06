//go:build !windows

package winsvc

import "fmt"

func NativeController() (*Controller, error) {
	return nil, fmt.Errorf("Windows 服务管理仅支持 Windows")
}

func IsAdministrator() (bool, error) {
	return false, fmt.Errorf("当前平台不支持 Windows 管理员身份检查")
}
