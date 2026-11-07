//go:build !webui

package web

import (
	"fmt"
	"io/fs"
)

// Backend-only checks must work in a clean checkout without committing generated assets.
func assets() (fs.FS, error) {
	return nil, fmt.Errorf("网页尚未构建，请运行 scripts\\build.ps1，或先 pnpm --dir web build 再使用 -tags webui 编译")
}
