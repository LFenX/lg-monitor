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
	"time"

	"github.com/LFenX/lg-monitor/internal/update"
)

// Logical size of the window's page; the window scales it to the screen's DPI.
const (
	windowWidth  = 460
	windowHeight = 760
	minWidth     = 400
	minHeight    = 560
)

// version is set at build time; a development build does not look for updates.
var version = "dev"

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

// updateDir holds downloaded installers; LGMON_UPDATE_DIR moves it for development and tests.
func updateDir() string {
	if dir := os.Getenv("LGMON_UPDATE_DIR"); dir != "" {
		return dir
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "lg-monitor", "updates")
	}
	return filepath.Join(os.TempDir(), "lgmon-updates")
}

// revealWait is how long an open page has to mark its title before a new page opens instead.
const revealWait = 1500 * time.Millisecond

// updateFeed is where releases are read; LGMON_UPDATE_FEED points it at a local fake release for testing.
func updateFeed() string {
	if feed := os.Getenv("LGMON_UPDATE_FEED"); feed != "" {
		return feed
	}
	return update.DefaultFeed
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
// An lgmon page already open in the browser is reused: lgmon tells it to go to the link and mark its title, and
// the window brings that browser window forward (see reveal); only when none shows up does a new page open.
//
// It runs as the document is created, before <html> exists, so nothing here may assume document.documentElement.
const bridge = `(() => {
  window.lgmonLauncher = true;
  const post = (href) => {
    const target = new URL(href, location.href);
    if (target.origin !== location.origin) {
      window.chrome.webview.postMessage(JSON.stringify({ open: target.href }));
      return;
    }
    const nonce = Math.floor(Math.random() * 65536).toString(16).padStart(4, '0');
    let token = '';
    try { token = JSON.parse(document.getElementById('lgmon-session').textContent).token || ''; } catch {}
    const done = () => window.chrome.webview.postMessage(JSON.stringify({ open: target.href, reveal: nonce }));
    fetch('/api/v1/pages/reveal', {
      method: 'POST',
      headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' },
      body: JSON.stringify({ hash: target.hash || '#/', nonce }),
    }).then(done, done);
  };
  const mark = () => { if (document.documentElement) document.documentElement.dataset.launcher = ''; };
  mark();
  document.addEventListener('DOMContentLoaded', mark);
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
