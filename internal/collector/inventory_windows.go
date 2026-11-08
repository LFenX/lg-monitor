//go:build windows

package collector

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type nativeInventoryLocation struct {
	hive      registry.Key
	key       string
	view      uint32
	directory string
}
type nativeInventory struct {
	key       []byte
	locations map[inventoryScope]nativeInventoryLocation
}

func newInventoryBackend(key []byte) inventoryBackend { return &nativeInventory{key: key} }

func (n *nativeInventory) Scopes(ctx context.Context) ([]inventoryScope, error) {
	n.locations = map[inventoryScope]nativeInventoryLocation{{"tasks", "local"}: {}}
	addRegistry := func(hive registry.Key, prefix, base string) {
		type registryView struct {
			name string
			flag uint32
		}
		views := []registryView{{"64", registry.WOW64_64KEY}, {"32", registry.WOW64_32KEY}}
		if hive == registry.USERS {
			views = []registryView{{"shared", 0}}
		}
		for _, view := range views {
			for _, key := range []string{"Run", "RunOnce"} {
				n.locations[inventoryScope{"startup", prefix + "/" + view.name + "/" + key}] = nativeInventoryLocation{hive: hive, key: base + `Software\Microsoft\Windows\CurrentVersion\` + key, view: view.flag}
			}
		}
	}
	addRegistry(registry.LOCAL_MACHINE, "machine", "")
	common, _ := windows.KnownFolderPath(windows.FOLDERID_CommonStartup, 0)
	n.locations[inventoryScope{"startup", "machine/Startup"}] = nativeInventoryLocation{directory: common}
	users, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, fmt.Errorf("读取已加载用户清单失败：%w", err)
	}
	names, err := users.ReadSubKeyNames(-1)
	users.Close()
	if err != nil {
		return nil, fmt.Errorf("读取用户清单失败：%w", err)
	}
	for _, sid := range names {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !strings.HasPrefix(sid, "S-1-5-21-") && !strings.HasPrefix(sid, "S-1-12-1-") {
			continue
		}
		if _, err := windows.StringToSid(sid); err != nil {
			continue
		}
		addRegistry(registry.USERS, "user/"+sid, sid+`\`)
		profile := ""
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+sid, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err == nil {
			profile, _, err = key.GetStringValue("ProfileImagePath")
			key.Close()
			if err != nil {
				profile = ""
			}
		}
		if strings.Contains(profile, "%") {
			profile, _ = registry.ExpandString(profile)
		}
		directory := ""
		if profile != "" {
			directory = filepath.Join(profile, `AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup`)
		}
		n.locations[inventoryScope{"startup", "user/" + sid + "/Startup"}] = nativeInventoryLocation{directory: directory}
	}
	if len(n.locations) > 256 {
		return nil, fmt.Errorf("启动项来源超过 256 个")
	}
	scopes := make([]inventoryScope, 0, len(n.locations))
	for scope := range n.locations {
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func (n *nativeInventory) Read(ctx context.Context, scope inventoryScope) ([]InventoryEntry, error) {
	if scope.kind == "tasks" {
		return readScheduledTasks(ctx)
	}
	location := n.locations[scope]
	if location.key != "" {
		return n.readStartupRegistry(ctx, location)
	}
	return n.readStartupDirectory(ctx, location.directory)
}

func (n *nativeInventory) readStartupRegistry(ctx context.Context, location nativeInventoryLocation) ([]InventoryEntry, error) {
	var userRoot registry.Key
	if location.hive == registry.USERS {
		parts := strings.SplitN(location.key, `\`, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("用户启动项来源无效")
		}
		root, err := registry.OpenKey(registry.USERS, parts[0], registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		userRoot = root
		location.hive, location.key = root, parts[1]
	}
	key, err := registry.OpenKey(location.hive, location.key, registry.QUERY_VALUE|location.view)
	if errors.Is(err, fs.ErrNotExist) {
		if userRoot != 0 {
			if _, err := userRoot.Stat(); err != nil {
				return nil, err
			}
		}
		return []InventoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer key.Close()
	before, err := key.Stat()
	if err != nil {
		return nil, err
	}
	if before.ValueCount > MaxInventoryEntries*2 || before.MaxValueNameLen > 1024 {
		return nil, errInventoryLimit
	}
	names, err := key.ReadValueNames(MaxInventoryEntries*2 + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > MaxInventoryEntries*2 {
		return nil, errInventoryLimit
	}
	entries := make([]InventoryEntry, 0, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		size, typ, err := key.GetValue(name, nil)
		if err != nil || size > 65536 {
			return nil, fmt.Errorf("读取启动项失败或注册表值过大")
		}
		if typ != registry.SZ && typ != registry.EXPAND_SZ {
			continue
		}
		value := make([]byte, size+4)
		binary.LittleEndian.PutUint32(value, typ)
		read, _, err := key.GetValue(name, value[4:])
		if err != nil || read != size {
			clear(value)
			return nil, fmt.Errorf("启动项在读取时发生变化")
		}
		fingerprint := inventoryFingerprint(n.key, value)
		clear(value)
		entries = append(entries, InventoryEntry{ID: strings.ToLower(name), Name: name, Fingerprint: fingerprint, State: "registered"})
		if len(entries) > MaxInventoryEntries {
			return nil, errInventoryLimit
		}
	}
	after, err := key.Stat()
	if err != nil {
		return nil, err
	}
	if before.ModTime() != after.ModTime() {
		return nil, fmt.Errorf("启动项来源在枚举期间发生变化")
	}
	if userRoot != 0 {
		if _, err := userRoot.Stat(); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func lockInventoryDirectory(path string) (func(), error) {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) {
		return nil, fmt.Errorf("启动目录必须位于本机磁盘")
	}
	volume := filepath.VolumeName(path)
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, fmt.Errorf("启动目录不能位于网络或可移动磁盘")
	}
	var handles []windows.Handle
	release := func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}
	current := volume + `\`
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), `\`) {
		current = filepath.Join(current, part)
		pointer, err := windows.UTF16PtrFromString(current)
		if err != nil {
			release()
			return nil, err
		}
		// Denying delete sharing keeps checked ancestors from being replaced by junctions.
		handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			release()
			return nil, err
		}
		handles = append(handles, handle)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			release()
			return nil, err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			release()
			return nil, fmt.Errorf("启动目录不能包含重解析点")
		}
	}
	return release, nil
}

func (n *nativeInventory) readStartupDirectory(ctx context.Context, path string) ([]InventoryEntry, error) {
	release, err := lockInventoryDirectory(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []InventoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer release()
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	files, err := directory.ReadDir(MaxInventoryEntries*2 + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(files) > MaxInventoryEntries*2 {
		return nil, errInventoryLimit
	}
	entries := make([]InventoryEntry, 0, len(files))
	for _, file := range files {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if strings.EqualFold(file.Name(), "desktop.ini") {
			continue
		}
		info, err := file.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		metadata := strings.ToLower(file.Name()) + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
		entries = append(entries, InventoryEntry{ID: strings.ToLower(file.Name()), Name: file.Name(), Fingerprint: inventoryFingerprint(n.key, []byte(metadata)), State: "registered"})
		if len(entries) > MaxInventoryEntries {
			return nil, errInventoryLimit
		}
	}
	return entries, nil
}
