//go:build windows

package secrets

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type pathPolicy struct{}

func NativePathPolicy() PathPolicy { return pathPolicy{} }

const trustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"

func trustedWriter(sid *windows.SID) bool {
	return sid != nil && (sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.String() == trustedInstallerSID)
}

func ordinaryPath(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("读取受保护路径失败：%w", err)
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("受保护路径不能是链接、重解析点或特殊文件：%s", path)
	}
	return info, nil
}

func (pathPolicy) Check(path string) error {
	if _, err := ordinaryPath(path); err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("读取路径权限失败：%w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("读取路径所有者失败：%w", err)
	}
	// An owner can change the DACL even without an explicit write grant.
	if !trustedWriter(owner) {
		return fmt.Errorf("路径所有者不是 SYSTEM、Administrators 或 TrustedInstaller：%s", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("读取路径 DACL 失败：%w", err)
	}
	if dacl == nil {
		return fmt.Errorf("路径缺少可核对的 DACL：%s", path)
	}
	const fileDeleteChild = 0x0040
	const mutable = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL | windows.MAXIMUM_ALLOWED
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("读取路径权限条目失败：%w", err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return fmt.Errorf("路径包含无法安全核对的权限条目：%s", path)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask&mutable != 0 && !trustedWriter(sid) {
			return fmt.Errorf("账户或组 %s 可以修改受保护路径：%s", sid.String(), path)
		}
	}
	return nil
}

func protectedRoot(path string) (string, error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	if !filepath.IsAbs(path) || strings.EqualFold(path, volume) || strings.EqualFold(path, volume+string(os.PathSeparator)) {
		return "", fmt.Errorf("受保护目录必须是绝对路径，且不能是磁盘或共享根目录")
	}
	return path, nil
}

func (pathPolicy) ProtectTree(path string, readable bool) error {
	path, err := protectedRoot(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("创建受保护目录失败：%w", err)
	}
	grants := "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	if readable {
		grants += "(A;OICI;GRGX;;;BU)"
	}
	root, err := windows.SecurityDescriptorFromString(grants)
	if err != nil {
		return fmt.Errorf("构造目录权限失败：%w", err)
	}
	rootACL, _, err := root.DACL()
	if err != nil {
		return fmt.Errorf("读取目录权限模板失败：%w", err)
	}
	empty, err := windows.SecurityDescriptorFromString("D:")
	if err != nil {
		return fmt.Errorf("构造继承权限失败：%w", err)
	}
	emptyACL, _, err := empty.DACL()
	if err != nil {
		return fmt.Errorf("读取继承权限模板失败：%w", err)
	}
	if emptyACL == nil {
		return fmt.Errorf("继承权限模板无效")
	}
	owner, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("读取管理员组身份失败：%w", err)
	}
	var paths []string
	err = filepath.WalkDir(path, func(current string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("遍历受保护目录失败：%w", walkErr)
		}
		info, err := ordinaryPath(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			file, err := os.Open(current)
			if err != nil {
				return fmt.Errorf("检查受保护文件失败：%w", err)
			}
			var data windows.ByHandleFileInformation
			err = windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &data)
			file.Close()
			if err != nil {
				return fmt.Errorf("检查文件链接失败：%w", err)
			}
			if data.NumberOfLinks != 1 {
				return fmt.Errorf("受保护目录中不能包含硬链接文件：%s", current)
			}
		}
		paths = append(paths, current)
		return nil
	})
	if err != nil {
		return err
	}
	// Root ACL updates propagate to existing children, so reject aliases before any update.
	for _, current := range paths {
		acl := emptyACL
		flags := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
		if current == path {
			acl = rootACL
			flags = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
		}
		if err := windows.SetNamedSecurityInfo(current, windows.SE_FILE_OBJECT, flags, owner, nil, acl, nil); err != nil {
			return fmt.Errorf("设置受保护路径权限失败：%w", err)
		}
	}
	return nil
}
