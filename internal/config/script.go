package config

import (
	"fmt"
	"regexp"
	"strings"
)

const scriptNamePattern = `^[^/\\:<>"|?*%&^!()\x00-\x1F\x7F]+\.(?:[pP][sS]1|[cC][mM][dD]|[bB][aA][tT]|[eE][xX][eE])$`
const scriptArgumentPattern = `^[A-Za-z0-9 _.,:=/@+-]*$`

var scriptName = regexp.MustCompile(scriptNamePattern)
var scriptArgument = regexp.MustCompile(scriptArgumentPattern)
var scriptDevice = regexp.MustCompile(`^(?i:con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

func scriptNameError(name string) error {
	if !scriptName.MatchString(name) || strings.Contains(name, "..") || scriptDevice.MatchString(strings.SplitN(name, ".", 2)[0]) {
		return fmt.Errorf("script 只能是 scripts 目录里的 .ps1、.cmd、.bat 或 .exe 文件名，不能包含路径、..、设备名或命令控制字符")
	}
	return nil
}

func ValidateScript(name string, args []string) error {
	if err := scriptNameError(name); err != nil {
		return err
	}
	for i, arg := range args {
		if !scriptArgument.MatchString(arg) {
			return fmt.Errorf("args[%d] 只允许字母、数字、空格和 _ . , : = / @ + -", i)
		}
	}
	return nil
}
