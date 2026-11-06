//go:build !windows

package secrets

import "fmt"

type pathPolicy struct{}

func NativePathPolicy() PathPolicy { return pathPolicy{} }
func (pathPolicy) Check(string) error {
	return fmt.Errorf("Windows 路径权限检查仅支持 Windows")
}
func (pathPolicy) ProtectTree(string, bool) error {
	return fmt.Errorf("Windows 路径权限设置仅支持 Windows")
}
