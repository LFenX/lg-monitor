//go:build !windows

package app

import (
	"context"
	"fmt"
	"strings"
)

// OpenAppWindow is the Windows start window; other systems open the panel in a browser themselves.
func OpenAppWindow(context.Context, string) error {
	return fmt.Errorf("lgmon 小窗口仅支持 Windows 10/11")
}

// StartPageURL is the small page shown when lgmon is opened.
func StartPageURL(address string) string {
	return strings.TrimRight(address, "/") + "/#/start"
}

func (NativeTrayOpener) Window(context.Context, string) error {
	return fmt.Errorf("lgmon 小窗口仅支持 Windows 10/11")
}
