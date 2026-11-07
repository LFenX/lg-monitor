//go:build webui

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var bundled embed.FS

func assets() (fs.FS, error) { return fs.Sub(bundled, "dist") }
