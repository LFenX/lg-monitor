package secrets

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type machineProtector struct{}
type filePolicy struct{}

func nativeAdapters() (Protector, AccessPolicy, error) { return machineProtector{}, filePolicy{}, nil }

func (machineProtector) Encrypt(value []byte) ([]byte, error) { return transform(value, true) }
func (machineProtector) Decrypt(value []byte) ([]byte, error) { return transform(value, false) }

func transform(value []byte, encrypt bool) ([]byte, error) {
	if len(value) == 0 || uint64(len(value)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("DPAPI 输入不能为空")
	}
	input := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	var output windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_LOCAL_MACHINE|windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	} else {
		err = windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	runtime.KeepAlive(value)
	if err != nil {
		return nil, err
	}
	buffer := unsafe.Slice(output.Data, int(output.Size))
	defer func() { clear(buffer); windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data)))) }()
	return append([]byte(nil), buffer...), nil
}

func (filePolicy) CurrentUser() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func (filePolicy) Secure(path, owner string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("读取密钥文件写入账户失败：%w", err)
	}
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		// A service must not restore the interactive creator's grants inside its private home.
		owner = "S-1-5-32-544"
	}
	sid, err := windows.StringToSid(owner)
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func replaceFile(temporary, destination string) error {
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
