package collector

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type IDStore interface{ LoadOrCreate() (string, error) }

type IdentityReader interface {
	ReadFile(string) ([]byte, error)
}

type IdentityWaiter interface{ Wait(time.Duration) }

type nativeIdentityReader struct{}
type nativeIdentityWaiter struct{}

func (nativeIdentityReader) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (nativeIdentityWaiter) Wait(delay time.Duration)             { time.Sleep(delay) }

type FileIDStore struct {
	Home   string
	Reader IdentityReader
	Waiter IdentityWaiter
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s FileIDStore) LoadOrCreate() (string, error) {
	directory := filepath.Join(s.Home, "data")
	path := filepath.Join(directory, "collector-id")
	read := func() (string, error) {
		data, err := s.readIdentity(path)
		if err != nil {
			return "", fmt.Errorf("读取采集器身份失败：%w", err)
		}
		id := strings.TrimSpace(string(data))
		if !uuidPattern.MatchString(id) {
			return "", fmt.Errorf("%s 中的采集器 ID 不是合法 UUID v4，请恢复原身份文件", path)
		}
		return id, nil
	}
	if id, err := read(); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return id, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("创建采集器数据目录失败：%w", err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("生成采集器 UUID 失败：%w", err)
	}
	raw[6], raw[8] = raw[6]&0x0f|0x40, raw[8]&0x3f|0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
	file, err := os.CreateTemp(directory, "collector-id-*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建采集器身份临时文件失败：%w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	_, err = file.WriteString(id + "\n")
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("保存采集器身份失败：%w", err)
	}
	if err := publishIdentity(temporary, path); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("发布采集器身份失败：%w", err)
	}
	return read()
}

func (s FileIDStore) readIdentity(path string) ([]byte, error) {
	reader, waiter := s.Reader, s.Waiter
	if reader == nil {
		reader = nativeIdentityReader{}
	}
	if waiter == nil {
		waiter = nativeIdentityWaiter{}
	}
	// Concurrent publication can temporarily deny reads of an otherwise complete identity file.
	for attempt := 0; ; attempt++ {
		data, err := reader.ReadFile(path)
		if err == nil || !identitySharingViolation(err) || attempt == 9 {
			return data, err
		}
		waiter.Wait(20 * time.Millisecond)
	}
}
