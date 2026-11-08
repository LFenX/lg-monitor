package config

import (
	"fmt"
	"strings"
)

// The monitor's host OS must not change how a collector's Windows paths are interpreted.
func CleanWindowsDirectory(value string) (string, error) {
	invalid := strings.ContainsAny(value, `*?"<>|`)
	for _, character := range value {
		invalid = invalid || character < 32
	}
	if invalid {
		return "", fmt.Errorf("目录不能包含控制字符或 *?\"<>|")
	}
	value = strings.ReplaceAll(value, "/", `\`)
	var root string
	var parts []string
	switch {
	case len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1:3] == `:\`:
		root, parts = strings.ToUpper(value[:1])+`:\`, strings.Split(value[3:], `\`)
	case strings.HasPrefix(value, `\\`):
		segments := strings.Split(value[2:], `\`)
		if len(segments) < 2 || segments[0] == "" || segments[1] == "" || segments[0] == "." || segments[0] == ".." || segments[1] == "." || segments[1] == ".." ||
			strings.Contains(segments[0], ":") || strings.Contains(segments[1], ":") {
			return "", fmt.Errorf("UNC 目录必须包含服务器名和共享名")
		}
		root, parts = `\\`+segments[0]+`\`+segments[1], segments[2:]
	default:
		return "", fmt.Errorf("目录必须是带盘符的绝对路径或包含共享名的 UNC 路径")
	}
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
		case "..":
			if len(clean) > 0 {
				clean = clean[:len(clean)-1]
			}
		default:
			if strings.Contains(part, ":") {
				return "", fmt.Errorf("目录分段不能包含冒号")
			}
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return root, nil
	}
	return strings.TrimSuffix(root, `\`) + `\` + strings.Join(clean, `\`), nil
}

func ObservedWindowsDirectory(value string) (string, error) {
	value = strings.ReplaceAll(value, "/", `\`)
	if len(value) >= 8 && strings.EqualFold(value[:8], `\\?\UNC\`) {
		value = `\\` + value[8:]
	} else if strings.HasPrefix(value, `\\?\`) {
		value = value[4:]
	}
	return CleanWindowsDirectory(value)
}

func ExecutableDirectory(value string) (string, error) {
	value = strings.ReplaceAll(value, "/", `\`)
	last := strings.LastIndex(value, `\`)
	if last < 0 || last == len(value)-1 {
		return "", fmt.Errorf("程序路径缺少绝对目录或文件名")
	}
	// Keep the root separator for an executable directly under a drive.
	return ObservedWindowsDirectory(value[:last+1])
}

func WindowsDirectoryContains(directory, observed string) bool {
	root := strings.Split(strings.TrimSuffix(directory, `\`), `\`)
	candidate := strings.Split(strings.TrimSuffix(observed, `\`), `\`)
	if len(root) > len(candidate) {
		return false
	}
	for i, part := range root {
		if !strings.EqualFold(part, candidate[i]) {
			return false
		}
	}
	return true
}
