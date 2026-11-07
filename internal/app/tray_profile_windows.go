//go:build windows

package app

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type userTrayProtector struct{}

func NewTrayProtector() TrayProtector                          { return userTrayProtector{} }
func (userTrayProtector) Encrypt(value []byte) ([]byte, error) { return trayDPAPI(value, true) }
func (userTrayProtector) Decrypt(value []byte) ([]byte, error) { return trayDPAPI(value, false) }

func trayDPAPI(value []byte, encrypt bool) ([]byte, error) {
	if len(value) == 0 || len(value) > 65536 {
		return nil, fmt.Errorf("托盘 DPAPI 输入长度无效")
	}
	input := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	var output windows.DataBlob
	var err error
	if encrypt {
		// This credential belongs to the interactive account, never the machine.
		err = windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	} else {
		err = windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	runtime.KeepAlive(value)
	if err != nil {
		return nil, fmt.Errorf("用户 DPAPI 操作失败：%w", err)
	}
	buffer := unsafe.Slice(output.Data, int(output.Size))
	defer func() { clear(buffer); windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data)))) }()
	return append([]byte(nil), buffer...), nil
}

func replaceTrayFile(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

type NativeTrayAutostart struct{}

func (NativeTrayAutostart) Set(executable string, enabled bool) error {
	if executable == "" {
		return fmt.Errorf("登录启动缺少程序路径")
	}
	command := `"` + executable + `" tray`
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("打开登录启动项失败：%w", err)
	}
	defer key.Close()
	if enabled {
		if err := key.SetStringValue("lg-monitor", command); err != nil {
			return fmt.Errorf("保存登录启动项失败：%w", err)
		}
		return nil
	}
	existing, _, err := key.GetStringValue("lg-monitor")
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取登录启动项失败：%w", err)
	}
	if existing != command {
		return fmt.Errorf("登录启动项指向另一程序，请从原程序取消启动")
	}
	if err := key.DeleteValue("lg-monitor"); err != nil {
		return fmt.Errorf("删除登录启动项失败：%w", err)
	}
	return nil
}
