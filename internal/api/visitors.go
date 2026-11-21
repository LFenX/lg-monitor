package api

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	visitorActiveWindow = 90 * time.Second
	visitorEvictWindow  = 5 * time.Minute
	visitorMaxTracked   = 200
	latestReleaseAPI    = "https://api.github.com/repos/LFenX/lg-monitor/releases/latest"
	latestReleasePage   = "https://github.com/LFenX/lg-monitor/releases/latest"
	latestReleaseTTL    = 15 * time.Minute
)

// LANVisitor describes one LAN peer currently viewing this monitor's web dashboard in read-only mode.
type LANVisitor struct {
	IP          string `json:"ip"`
	Avatar      string `json:"avatar"`
	FirstSeen   int64  `json:"firstSeen"`
	LastSeen    int64  `json:"lastSeen"`
	Connections int    `json:"connections"`
}

// Built-in avatar keys shared with web/src/avatar.tsx so visitors get consistent, charming random avatars.
var visitorAvatars = []string{
	"lg-cat-1", "lg-cat-2", "lg-cat-3", "lg-cat-4", "lg-cat-5",
	"lg-bear-1", "lg-bear-2", "lg-bear-3", "lg-bear-4", "lg-bear-5",
	"lg-rabbit-1", "lg-rabbit-2", "lg-rabbit-3", "lg-rabbit-4", "lg-rabbit-5",
	"lg-fox-1", "lg-fox-2", "lg-fox-3", "lg-fox-4", "lg-fox-5",
	"lg-panda-1", "lg-panda-2", "lg-panda-3", "lg-panda-4", "lg-panda-5",
	"lg-frog-1", "lg-frog-2", "lg-frog-3", "lg-frog-4", "lg-frog-5",
	"lg-chick-1", "lg-chick-2", "lg-chick-3", "lg-chick-4", "lg-chick-5",
	"lg-pig-1", "lg-pig-2", "lg-pig-3", "lg-pig-4", "lg-pig-5",
	"fiora-0", "fiora-1", "fiora-2", "fiora-3", "fiora-4",
	"fiora-5", "fiora-6", "fiora-7", "fiora-8", "fiora-9",
	"lorelei-1", "lorelei-5", "lorelei-9", "lorelei-14", "lorelei-19",
	"lorelei-24", "lorelei-29", "lorelei-35", "lorelei-39",
}

func visitorAvatarForIP(ip string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte("lgmon-visitor:" + ip))
	return visitorAvatars[int(h.Sum32())%len(visitorAvatars)]
}

type visitorEntry struct {
	ip          string
	avatar      string
	firstSeen   int64
	lastSeen    int64
	connections int
}

// VisitorTracker remembers LAN peers currently viewing the dashboard over HTTP/SSE.
type VisitorTracker struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*visitorEntry
}

func newVisitorTracker(now func() time.Time) *VisitorTracker {
	if now == nil {
		now = time.Now
	}
	return &VisitorTracker{now: now, entries: map[string]*visitorEntry{}}
}

func (t *VisitorTracker) Touch(ip string) {
	if ip == "" {
		return
	}
	nowMS := t.now().UnixMilli()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.touchLocked(ip, nowMS)
}

func (t *VisitorTracker) EnterStream(ip string) func() {
	if ip == "" {
		return func() {}
	}
	nowMS := t.now().UnixMilli()
	t.mu.Lock()
	entry := t.touchLocked(ip, nowMS)
	entry.connections++
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			if current, ok := t.entries[ip]; ok {
				if current.connections > 0 {
					current.connections--
				}
				current.lastSeen = t.now().UnixMilli()
			}
			t.mu.Unlock()
		})
	}
}

func (t *VisitorTracker) touchLocked(ip string, nowMS int64) *visitorEntry {
	if entry, ok := t.entries[ip]; ok {
		entry.lastSeen = nowMS
		return entry
	}
	if len(t.entries) >= visitorMaxTracked {
		cutoff := nowMS - visitorEvictWindow.Milliseconds()
		for k, v := range t.entries {
			if v.connections == 0 && v.lastSeen < cutoff {
				delete(t.entries, k)
			}
		}
	}
	entry := &visitorEntry{
		ip:        ip,
		avatar:    visitorAvatarForIP(ip),
		firstSeen: nowMS,
		lastSeen:  nowMS,
	}
	t.entries[ip] = entry
	return entry
}

func (t *VisitorTracker) List() []LANVisitor {
	nowMS := t.now().UnixMilli()
	activeCutoff := nowMS - visitorActiveWindow.Milliseconds()
	evictCutoff := nowMS - visitorEvictWindow.Milliseconds()
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]LANVisitor, 0, len(t.entries))
	for k, v := range t.entries {
		if v.connections == 0 && v.lastSeen < evictCutoff {
			delete(t.entries, k)
			continue
		}
		if v.connections > 0 || v.lastSeen >= activeCutoff {
			result = append(result, LANVisitor{
				IP:          v.ip,
				Avatar:      v.avatar,
				FirstSeen:   v.firstSeen,
				LastSeen:    v.lastSeen,
				Connections: v.connections,
			})
		}
	}
	slices.SortFunc(result, func(a, b LANVisitor) int {
		if a.LastSeen != b.LastSeen {
			if a.LastSeen > b.LastSeen {
				return -1
			}
			return 1
		}
		return strings.Compare(a.IP, b.IP)
	})
	return result
}

type latestDownloadCache struct {
	mu        sync.Mutex
	url       string
	expiresAt time.Time
	now       func() time.Time
}

func (c *latestDownloadCache) resolve(ctx context.Context) string {
	nowFn := c.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	c.mu.Lock()
	if c.url != "" && now.Before(c.expiresAt) {
		cached := c.url
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, latestReleaseAPI, nil)
	if err != nil {
		return latestReleasePage
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lgmon")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return latestReleasePage
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return latestReleasePage
	}
	var release struct {
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return latestReleasePage
	}
	target := release.HTMLURL
	for _, asset := range release.Assets {
		lower := strings.ToLower(asset.Name)
		if strings.HasPrefix(lower, "lgmon-setup-") && strings.HasSuffix(lower, ".exe") && strings.HasPrefix(asset.BrowserDownloadURL, "https://") {
			target = asset.BrowserDownloadURL
			break
		}
	}
	if target == "" {
		target = latestReleasePage
	}
	c.mu.Lock()
	c.url = target
	c.expiresAt = now.Add(latestReleaseTTL)
	c.mu.Unlock()
	return target
}
