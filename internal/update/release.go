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
	"strings"
	"time"
)

// DefaultFeed is the latest published lgmon release. Releases are made by pushing a v* tag (release.yml).
const DefaultFeed = "https://api.github.com/repos/LFenX/lg-monitor/releases/latest"

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

// Latest reads the newest release and the checksum of its installer. A feed without a published release
// returns a zero Release and no error.
func Latest(ctx context.Context, client *http.Client, address string) (Release, error) {
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
		case strings.HasPrefix(asset.Name, "lgmon-setup-") && strings.HasSuffix(strings.ToLower(asset.Name), ".exe"):
			release.File, release.URL, release.Size = asset.Name, asset.URL, asset.Size
		case asset.Name == "SHA256SUMS":
			sums = asset.URL
		}
	}
	if release.URL == "" || sums == "" {
		return Release{}, fmt.Errorf("版本 %s 缺少安装器或校验文件", release.Version)
	}
	data, err = get(ctx, client, sums, 64<<10)
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "lgmon-updater")
	response, err := client.Do(request)
	if err != nil {
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
	return target, nil
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
