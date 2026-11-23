// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	latestReleaseTTL    = 2 * time.Minute
	githubRepoAPI       = "https://api.github.com/repos/LFenX/lg-monitor"
	githubReleasesAPI   = "https://api.github.com/repos/LFenX/lg-monitor/releases?per_page=100"
	githubRepoPage      = "https://github.com/LFenX/lg-monitor"
	githubStatsTTL      = 2 * time.Minute
)

// LANVisitor describes one LAN peer currently viewing this monitor's web dashboard.
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

func (c *latestDownloadCache) seed(target string, expiresAt time.Time) {
	if target == "" {
		return
	}
	c.mu.Lock()
	c.url = target
	c.expiresAt = expiresAt
	c.mu.Unlock()
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
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
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

// AssetCounts counts file requests across the release assets GitHub exposes.
type AssetCounts struct {
	Installer       int `json:"installer"`
	LegacyInstaller int `json:"legacyInstaller"`
	Checksums       int `json:"checksums"`
	Metadata        int `json:"metadata"`
	Other           int `json:"other"`
}

func (a *AssetCounts) add(name string, count int) {
	name = strings.ToLower(name)
	switch {
	case strings.HasPrefix(name, "lgmon-setup-") && strings.HasSuffix(name, ".exe"):
		a.Installer += count
	case strings.HasPrefix(name, "lgmon-") && strings.HasSuffix(name, "-windows-amd64-setup.exe"):
		a.LegacyInstaller += count
	case name == "sha256sums":
		a.Checksums += count
	case strings.HasSuffix(name, "-setup.json"):
		a.Metadata += count
	default:
		a.Other += count
	}
}

// ReleaseDownload summarizes download count for a single GitHub release tag.
type ReleaseDownload struct {
	Assets      AssetCounts `json:"assets"`
	Tag         string      `json:"tag"`
	Downloads   int         `json:"downloads"`
	PublishedAt string      `json:"publishedAt,omitempty"`
}

// GitHubStats exposes live GitHub repository and release download counts for the guest download bar
// and the author's admin growth dashboard.
type GitHubStats struct {
	Assets    *AssetCounts      `json:"assets,omitempty"`
	Downloads int               `json:"downloads"`
	Stars     int               `json:"stars"`
	Forks     int               `json:"forks"`
	Version   string            `json:"version,omitempty"`
	RepoURL   string            `json:"repoUrl"`
	UpdatedAt int64             `json:"updatedAt"`
	Releases  []ReleaseDownload `json:"releases,omitempty"`
	// Available is false while no count has ever been read, so the page hides zeros it cannot vouch for.
	Available bool `json:"available"`
}

type githubStatsCache struct {
	mu          sync.Mutex
	stats       GitHubStats
	hasData     bool
	loaded      bool
	fetching    bool
	expiresAt   time.Time
	retryAt     time.Time
	now         func() time.Time
	client      *http.Client
	repoURL     string
	releasesURL string
	pageURL     string
	// file keeps the last good numbers across restarts: an office network that shares one address with many
	// people often exhausts GitHub's anonymous quota, and the bar would otherwise read 0 until it resets.
	file     string
	current  string
	onLatest func(url string, expiresAt time.Time)
}

func (c *githubStatsCache) get(ctx context.Context) GitHubStats {
	nowFn := c.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	c.mu.Lock()
	c.loadLocked()
	if c.hasData && now.Before(c.expiresAt) || c.fetching || now.Before(c.retryAt) {
		out := c.withFallbacksLocked()
		c.mu.Unlock()
		return out
	}
	c.fetching = true
	c.mu.Unlock()

	fresh, retry, ok := c.fetch(ctx, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetching = false
	if !ok {
		c.retryAt = now.Add(retry)
		if !c.hasData && c.stats.Version == "" {
			// The public release page redirect needs no API quota, so the bar can still name the version.
			c.mu.Unlock()
			version := latestTagFromPage(ctx, c.httpClient(), c.pageURL)
			c.mu.Lock()
			if version != "" {
				c.stats.Version = version
			}
		}
		return c.withFallbacksLocked()
	}
	c.stats = fresh
	c.hasData = true
	c.expiresAt = now.Add(githubStatsTTL)
	c.retryAt = time.Time{}
	c.saveLocked()
	return c.withFallbacksLocked()
}

func (c *githubStatsCache) withFallbacksLocked() GitHubStats {
	out := c.stats
	out.RepoURL = githubRepoPage
	out.Available = c.hasData
	if out.Version == "" && releaseVersion.MatchString(c.current) {
		out.Version = "v" + strings.TrimPrefix(c.current, "v")
	}
	return out
}

var releaseVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func (c *githubStatsCache) loadLocked() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.file == "" {
		return
	}
	data, err := os.ReadFile(c.file)
	if err != nil {
		return
	}
	var saved GitHubStats
	if json.Unmarshal(data, &saved) == nil && saved.UpdatedAt > 0 {
		c.stats, c.hasData = saved, true
	}
}

func (c *githubStatsCache) saveLocked() {
	if c.file == "" {
		return
	}
	data, err := json.Marshal(c.stats)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.file), 0o755); err != nil {
		return
	}
	tmp := c.file + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, c.file)
	}
}

func (c *githubStatsCache) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	return http.DefaultClient
}

// latestTagFromPage reads the newest stable tag from the redirect of GitHub's public "latest release" page.
func latestTagFromPage(ctx context.Context, client *http.Client, page string) string {
	if page == "" {
		page = latestReleasePage
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodHead, page, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "lgmon")
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	location := resp.Header.Get("Location")
	_, tag, ok := strings.Cut(location, "/releases/tag/")
	if !ok || !releaseVersion.MatchString(tag) || !strings.HasPrefix(tag, "v") {
		return ""
	}
	return tag
}

// statsRetry says how long to leave GitHub alone after a failed read; a spent quota names its own reset time.
func statsRetry(resp *http.Response, now time.Time) time.Duration {
	retry := githubStatsTTL
	if resp == nil || resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return retry
	}
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
		retry = time.Duration(seconds) * time.Second
	} else if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		retry = time.Unix(reset, 0).Sub(now)
	}
	return min(max(retry, githubStatsTTL), time.Hour)
}

func (c *githubStatsCache) fetch(ctx context.Context, now time.Time) (GitHubStats, time.Duration, bool) {
	client := c.httpClient()
	retry := githubStatsTTL
	repoAPI := c.repoURL
	if repoAPI == "" {
		repoAPI = githubRepoAPI
	}
	releasesAPI := c.releasesURL
	if releasesAPI == "" {
		releasesAPI = githubReleasesAPI
	}
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	doGet := func(url string, dst any) bool {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(callCtx, http.MethodGet, url, nil)
		if err != nil {
			return false
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "lgmon")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			retry = statsRetry(resp, now)
			return false
		}
		return json.NewDecoder(resp.Body).Decode(dst) == nil
	}

	var repo struct {
		Stars int `json:"stargazers_count"`
		Forks int `json:"forks_count"`
	}
	type releaseInfo struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			DownloadCount      int    `json:"download_count"`
		} `json:"assets"`
	}
	doGet(repoAPI, &repo)
	var releases []releaseInfo
	endpoint, err := url.Parse(releasesAPI)
	if err != nil {
		return GitHubStats{}, retry, false
	}
	for page := 1; ; page++ {
		var chunk []releaseInfo
		if !doGet(endpoint.String(), &chunk) {
			return GitHubStats{}, retry, false
		}
		releases = append(releases, chunk...)
		if len(chunk) < 100 {
			break
		}
		query := endpoint.Query()
		query.Set("page", strconv.Itoa(page+1))
		endpoint.RawQuery = query.Encode()
	}
	if ctx.Err() != nil {
		return GitHubStats{}, retry, false
	}
	downloads := 0
	version := ""
	latestTarget := ""
	totals := AssetCounts{}
	breakdown := make([]ReleaseDownload, 0, len(releases))
	for _, rel := range releases {
		if version == "" && !rel.Draft && !rel.Prerelease && rel.TagName != "" {
			version = rel.TagName
			latestTarget = rel.HTMLURL
			for _, asset := range rel.Assets {
				lower := strings.ToLower(asset.Name)
				if strings.HasPrefix(lower, "lgmon-setup-") && strings.HasSuffix(lower, ".exe") && strings.HasPrefix(asset.BrowserDownloadURL, "https://") {
					latestTarget = asset.BrowserDownloadURL
					break
				}
			}
		}
		counts := AssetCounts{}
		relDownloads := 0
		for _, asset := range rel.Assets {
			relDownloads += asset.DownloadCount
			counts.add(asset.Name, asset.DownloadCount)
			totals.add(asset.Name, asset.DownloadCount)
		}
		downloads += relDownloads
		if rel.TagName != "" {
			breakdown = append(breakdown, ReleaseDownload{
				Tag:         rel.TagName,
				Downloads:   relDownloads,
				Assets:      counts,
				PublishedAt: rel.PublishedAt,
			})
		}
	}
	if latestTarget != "" && c.onLatest != nil {
		c.onLatest(latestTarget, now.Add(latestReleaseTTL))
	}
	return GitHubStats{
		Downloads: downloads,
		Assets:    &totals,
		Stars:     repo.Stars,
		Forks:     repo.Forks,
		Version:   version,
		RepoURL:   githubRepoPage,
		UpdatedAt: now.UnixMilli(),
		Releases:  breakdown,
	}, 0, true
}
