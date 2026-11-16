//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/LFenX/lg-monitor/internal/desktop"
	"github.com/LFenX/lg-monitor/internal/webwindow"
)

// className identifies lgmon's window, so a second launch brings the first one forward instead of opening another.
const className = "lgmonWindow"

func run(args []string, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if webwindow.FocusExisting(className) {
		return 0
	}
	// Without the WebView2 runtime (rare on Windows 10/11) the same page opens as an Edge app window.
	if !webwindow.Available() {
		return fallback(opts.start.String())
	}
	start := opts.start
	err = webwindow.Run(webwindow.Options{
		ClassName: className, Title: "lgmon", Width: windowWidth, Height: windowHeight,
		MinWidth: minWidth, MinHeight: minHeight, Resizable: true,
		DataDir: opts.data, URL: start.String(), Script: bridge,
		OnMessage: func(window *webwindow.Window, text string) {
			var request struct {
				Open string `json:"open"`
			}
			if json.Unmarshal([]byte(text), &request) != nil || request.Open == "" {
				return
			}
			if target, ok := external(request.Open, start); ok {
				_ = desktop.ShellOpen(target)
			}
		},
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return fallback(start.String())
	}
	return 0
}

func fallback(target string) int {
	if desktop.OpenEdgeApp(target, windowWidth, windowHeight) == nil || desktop.ShellOpen(target) == nil {
		return 0
	}
	return 1
}
