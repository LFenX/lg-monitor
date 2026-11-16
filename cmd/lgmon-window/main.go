// Command lgmon-window is lgmon's own launcher: the small start page in a window with lgmon's icon and title,
// rather than a browser tab. lgmon.exe starts it; it is not meant to be run by hand.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Logical size of the window's page; the window scales it to the screen's DPI.
const (
	windowWidth  = 460
	windowHeight = 760
	minWidth     = 400
	minHeight    = 560
)

type options struct {
	start *url.URL
	data  string
}

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func parseOptions(args []string, stderr io.Writer) (options, error) {
	flags := flag.NewFlagSet("lgmon-window", flag.ContinueOnError)
	flags.SetOutput(stderr)
	address := flags.String("url", "", "lgmon 小窗口的地址，例如 http://127.0.0.1:7444/#/start")
	data := flags.String("data", "", "窗口的浏览器数据目录；省略时放在本人的 %LOCALAPPDATA%\\lg-monitor\\window")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	start, err := startURL(*address)
	if err != nil {
		return options{}, err
	}
	return options{start: start, data: dataDir(*data)}, nil
}

// startURL accepts only lgmon's own page: an HTTP(S) address without credentials or a query.
func startURL(raw string) (*url.URL, error) {
	address, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" || address.User != nil || address.RawQuery != "" {
		return nil, fmt.Errorf("--url 必须是 lgmon 的 HTTP/HTTPS 地址，不能带凭据或查询参数")
	}
	return address, nil
}

func dataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "lg-monitor", "window")
	}
	return filepath.Join(os.TempDir(), "lgmon-window")
}

// external is a link the page asked to open outside the window. Only lgmon's own pages go to the browser,
// so nothing the page shows can send the person somewhere else.
func external(target string, start *url.URL) (string, bool) {
	address, err := url.Parse(strings.TrimSpace(target))
	if err != nil || address.User != nil || !strings.EqualFold(address.Scheme, start.Scheme) || !strings.EqualFold(address.Host, start.Host) {
		return "", false
	}
	return address.String(), true
}

// bridge runs before the page's own scripts. Links meant for a new tab (the board, alerts, settings) open in
// the default browser instead of a second, chromeless window, and the page knows it is inside lgmon's window.
const bridge = `(() => {
  const post = (href) => window.chrome.webview.postMessage(JSON.stringify({ open: new URL(href, location.href).href }));
  document.documentElement.dataset.launcher = '';
  window.lgmonLauncher = true;
  document.addEventListener('click', (event) => {
    const link = event.target instanceof Element ? event.target.closest('a[href]') : null;
    if (!link) return;
    const href = new URL(link.href, location.href);
    if (link.target === '_blank' || href.origin !== location.origin) {
      event.preventDefault();
      post(href.href);
    }
  }, true);
  window.open = (href) => { if (href) post(String(href)); return null; };
})();`
