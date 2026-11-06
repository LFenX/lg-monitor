//go:build !windows

package collector

import (
	"context"
	"fmt"
)

func (nativeScriptRunner) Run(context.Context, string) (ScriptOutput, error) {
	return ScriptOutput{}, fmt.Errorf("脚本探测原生执行器仅支持 Windows")
}
