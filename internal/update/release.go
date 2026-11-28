// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const githubReleases = "https://github.com/LFenX/lg-monitor/releases"

// DefaultFeed resolves the latest stable release through GitHub's public page without using the REST API.
const DefaultFeed = githubReleases + "/latest"

var releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Release is a published version and the installer that brings a computer to it.
type Release struct {
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	Page      string `json:"page"`
	Published string `json:"published"`
	File      string `json:"file"`
	URL       string `json:"-"`
	Size      int64  `json:"size"`
	SHA256    string `json:"-"`
}

type feedAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type feed struct {
	Tag       string      `json:"tag_name"`
	Body      string      `json:"body"`
	Page      string      `json:"html_url"`
	Published string      `json:"published_at"`
	Draft     bool        `json:"draft"`
	Pre       bool        `json:"prerelease"`
	Assets    []feedAsset `json:"assets"`
}

func get(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "lgmon-updater")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("连不上发布页，请检查网络或代理：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, errNoRelease
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("发布页返回 HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, limit))
}

var errNoRelease = errors.New("还没有发布过版本")

// Latest reads the newest release and its installer checksum. A custom JSON feed without a published
// release returns a zero Release and no error.
func Latest(ctx context.Context, client *http.Client, address string) (Release, error) {
	return latestFile(ctx, client, address, func(version string) string { return "lgmon-setup-" + version + ".exe" }, func(name, _ string) bool {
		return strings.HasPrefix(name, "lgmon-setup-") && strings.HasSuffix(strings.ToLower(name), ".exe")
	})
}

// LinuxAsset names the Linux build for an architecture (amd64, arm64). The name carries no version, so
// .../releases/latest/download/<name> always fetches the newest one.
func LinuxAsset(arch string) string { return "lgmon-linux-" + arch + ".tar.gz" }

// LatestAsset reads the newest release like Latest, but for the release file called name.
func LatestAsset(ctx context.Context, client *http.Client, address, name string) (Release, error) {
	return latestFile(ctx, client, address, func(string) string { return name }, func(asset, _ string) bool { return asset == name })
}

// LatestForPlatform automatically routes to the matching release asset for the given OS and architecture:
// Windows uses the Inno installer (lgmon-setup-<version>.exe), Linux uses lgmon-linux-<arch>.tar.gz.
func LatestForPlatform(ctx context.Context, client *http.Client, address, goos, goarch string) (Release, error) {
	if goos == "linux" {
		return LatestAsset(ctx, client, address, LinuxAsset(goarch))
	}
	return Latest(ctx, client, address)
}

func latestFile(ctx context.Context, client *http.Client, address string, fileFor func(version string) string, match func(name, version string) bool) (Release, error) {
	if address == DefaultFeed {
		return latestFromPublicPage(ctx, client, fileFor)
	}
	data, err := get(ctx, client, address, 4<<20)
	if errors.Is(err, errNoRelease) {
		return Release{}, nil
	}
	if err != nil {
		return Release{}, err
	}
	var latest feed
	if err := json.Unmarshal(data, &latest); err != nil {
		return Release{}, fmt.Errorf("发布信息无法识别：%w", err)
	}
	if latest.Draft || latest.Pre || latest.Tag == "" {
		return Release{}, nil
	}
	release := Release{Version: strings.TrimPrefix(latest.Tag, "v"), Notes: strings.TrimSpace(latest.Body), Page: latest.Page, Published: latest.Published}
	sums := ""
	for _, asset := range latest.Assets {
		switch {
		case match(asset.Name, release.Version):
			release.File, release.URL, release.Size = asset.Name, asset.URL, asset.Size
		case asset.Name == "SHA256SUMS":
			sums = asset.URL
		}
	}
	if release.URL == "" || sums == "" {
		return Release{}, fmt.Errorf("版本 %s 缺少 %s 或校验文件", release.Version, fileFor(release.Version))
	}
	return withChecksum(ctx, client, release, sums)
}

func latestFromPublicPage(ctx context.Context, client *http.Client, fileFor func(string) string) (Release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, DefaultFeed, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("User-Agent", "lgmon-updater")
	pageClient := *client
	pageClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := pageClient.Do(request)
	if err != nil {
		return Release{}, fmt.Errorf("连不上 GitHub 公开发布页：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		return Release{}, fmt.Errorf("GitHub 公开发布页返回 HTTP %d", response.StatusCode)
	}
	location, err := response.Location()
	if err != nil {
		return Release{}, fmt.Errorf("无法读取 GitHub 公开发布页的版本链接：%w", err)
	}
	page := location.String()
	tag, ok := strings.CutPrefix(page, githubReleases+"/tag/")
	if !ok || !releaseTag.MatchString(tag) {
		return Release{}, fmt.Errorf("GitHub 公开发布页未指向有效的正式版本")
	}
	version := strings.TrimPrefix(tag, "v")
	file := fileFor(version)
	assets := githubReleases + "/download/" + tag + "/"
	release := Release{Version: version, Page: page, File: file, URL: assets + file}
	return withChecksum(ctx, client, release, assets+"SHA256SUMS")
}

func withChecksum(ctx context.Context, client *http.Client, release Release, sums string) (Release, error) {
	data, err := get(ctx, client, sums, 64<<10)
	if errors.Is(err, errNoRelease) {
		return Release{}, fmt.Errorf("版本 %s 缺少校验文件", release.Version)
	}
	if err != nil {
		return Release{}, err
	}
	release.SHA256 = checksum(string(data), release.File)
	if release.SHA256 == "" {
		return Release{}, fmt.Errorf("校验文件里没有 %s", release.File)
	}
	return release, nil
}

// checksum finds name's SHA-256 in a sha256sum-style list ("<hex>  <name>" per line).
func checksum(list, name string) string {
	scanner := bufio.NewScanner(strings.NewReader(list))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				return strings.ToLower(fields[0])
			}
		}
	}
	return ""
}

var (
	// stallTimeout is a variable so tests need not wait for it.
	stallTimeout = time.Minute
	errStalled   = errors.New("一直没有收到数据")
)

// Download saves the release's installer into dir and checks it against the published SHA-256. A file already
// there with the right checksum is used as it is; anything that does not match is deleted.
func Download(ctx context.Context, client *http.Client, release Release, dir string, progress func(done, total int64)) (string, error) {
	if release.File == "" || filepath.Base(release.File) != release.File || release.SHA256 == "" {
		return "", fmt.Errorf("这个版本没有可下载的安装器")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建下载目录失败：%w", err)
	}
	target := filepath.Join(dir, release.File)
	if sum, err := fileSum(target); err == nil && sum == release.SHA256 {
		return target, nil
	}
	// A connection that stays open without delivering anything would otherwise hold the update for good.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(stallTimeout, func() { cancel(errStalled) })
	defer stall.Stop()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "lgmon-updater")
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(context.Cause(ctx), errStalled) {
			err = errStalled
		}
		return "", fmt.Errorf("下载更新失败，请检查网络或代理：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载更新失败：HTTP %d", response.StatusCode)
	}
	total := response.ContentLength
	if total <= 0 {
		total = release.Size
	}
	partial := target + ".part"
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("保存更新失败：%w", err)
	}
	hash := sha256.New()
	var done int64
	last := time.Time{}
	buffer := make([]byte, 64<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			stall.Reset(stallTimeout)
			if _, err := file.Write(buffer[:n]); err != nil {
				file.Close()
				os.Remove(partial)
				return "", fmt.Errorf("保存更新失败：%w", err)
			}
			hash.Write(buffer[:n])
			done += int64(n)
			if progress != nil && time.Since(last) > 150*time.Millisecond {
				last = time.Now()
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			os.Remove(partial)
			if errors.Is(context.Cause(ctx), errStalled) {
				readErr = errStalled
			}
			return "", fmt.Errorf("下载中断了，请重试：%w", readErr)
		}
	}
	if err := file.Close(); err != nil {
		os.Remove(partial)
		return "", fmt.Errorf("保存更新失败：%w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != release.SHA256 {
		os.Remove(partial)
		return "", fmt.Errorf("下载的安装器和发布的校验值不一致，已经删掉，请重试")
	}
	if progress != nil {
		progress(done, total)
	}
	_ = os.Remove(target)
	if err := os.Rename(partial, target); err != nil {
		return "", fmt.Errorf("保存更新失败：%w", err)
	}
	_, _ = PruneInstallers(dir, release.File, "")
	return target, nil
}

// PruneInstallers removes superseded lgmon-setup-*.exe files and stale .part downloads from dir.
// When keepFile is non-empty, every other lgmon-setup-*.exe is removed; otherwise installers whose
// version is not newer than currentVersion (when currentVersion is a dotted version) are removed.
func PruneInstallers(dir, keepFile, currentVersion string) (int, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	now := time.Now()
	var removed int
	var freed int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		full := filepath.Join(dir, name)
		info, infoErr := entry.Info()
		if strings.HasSuffix(lower, ".part") {
			if infoErr == nil && now.Sub(info.ModTime()) > time.Hour {
				if os.Remove(full) == nil {
					removed++
					freed += info.Size()
				}
			}
			continue
		}
		if !strings.HasPrefix(lower, "lgmon-setup-") || !strings.HasSuffix(lower, ".exe") {
			continue
		}
		if keepFile != "" {
			if strings.EqualFold(name, keepFile) {
				continue
			}
		} else if currentVersion != "" && currentVersion != "dev" {
			ver := strings.TrimSuffix(strings.TrimPrefix(lower, "lgmon-setup-"), ".exe")
			if Newer(ver, currentVersion) {
				continue
			}
		} else {
			continue
		}
		if os.Remove(full) == nil {
			removed++
			if infoErr == nil {
				freed += info.Size()
			}
		}
	}
	return removed, freed
}

func fileSum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
