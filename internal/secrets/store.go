package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type Store interface {
	Set(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	List(context.Context) ([]string, error)
}
type Protector interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}
type AccessPolicy interface {
	CurrentUser() (string, error)
	Secure(string, string) error
}

type secretFile struct {
	Version int               `json:"version"`
	Owner   string            `json:"owner"`
	Values  map[string][]byte `json:"values"`
}
type FileStore struct {
	home      string
	protector Protector
	policy    AccessPolicy
	logger    *slog.Logger
	mu        sync.Mutex
}

type privateError struct {
	message string
	cause   error
}

func (e *privateError) Error() string { return e.message }
func (e *privateError) Unwrap() error { return e.cause }
func safeError(message string, cause error) error {
	return &privateError{message: message, cause: cause}
}

func New(home string, logger *slog.Logger) (Store, error) {
	protector, policy, err := nativeAdapters()
	if err != nil {
		return nil, err
	}
	return NewFileStore(home, protector, policy, logger)
}

func NewFileStore(home string, protector Protector, policy AccessPolicy, logger *slog.Logger) (*FileStore, error) {
	if home == "" || protector == nil || policy == nil {
		return nil, fmt.Errorf("密钥库需要数据目录、加密器和权限策略")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &FileStore{home: home, protector: protector, policy: policy, logger: logger}, nil
}

func (s *FileStore) load() (secretFile, error) {
	data, err := os.ReadFile(filepath.Join(s.home, "data", "secrets.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return secretFile{Version: 1, Values: make(map[string][]byte)}, nil
	}
	if err != nil {
		return secretFile{}, fmt.Errorf("读取密钥库失败：%w", err)
	}
	var file secretFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return file, safeError("密钥库文件格式无效，请恢复备份，不要写入明文", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return file, safeError("密钥库包含多余内容，请恢复备份", err)
	}
	if file.Version != 1 || file.Owner == "" {
		return file, fmt.Errorf("密钥库版本或创建者信息无效，请恢复备份")
	}
	if file.Values == nil {
		file.Values = make(map[string][]byte)
	}
	return file, nil
}

func (s *FileStore) Set(ctx context.Context, name string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存密钥已取消：%w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" || len(value) == 0 {
		return fmt.Errorf("密钥名称和内容不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return err
	}
	if file.Owner == "" {
		file.Owner, err = s.policy.CurrentUser()
		if err != nil || file.Owner == "" {
			return safeError("无法识别密钥库创建者，请检查当前账户", err)
		}
	}
	encrypted, err := s.protector.Encrypt(value)
	if err != nil {
		failure := safeError("加密密钥失败，请检查当前账户和计算机的 DPAPI 状态", err)
		s.logger.Error("Secret encryption failed", "error", failure)
		return fmt.Errorf("保存密钥失败：%w", failure)
	}
	if len(encrypted) == 0 {
		return fmt.Errorf("加密器返回空结果，密钥未保存")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存密钥已取消：%w", err)
	}
	file.Values[name] = encrypted
	if err := s.save(file); err != nil {
		s.logger.Error("Secret persistence failed", "error", err)
		return err
	}
	s.logger.Info("Secret saved")
	return nil
}

func (s *FileStore) Get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取密钥已取消：%w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return nil, err
	}
	encrypted, exists := file.Values[strings.TrimSpace(name)]
	if !exists {
		return nil, fmt.Errorf("密钥不存在，请先运行 lgmon secret set 保存凭证：%w", fs.ErrNotExist)
	}
	plain, err := s.protector.Decrypt(encrypted)
	if err != nil {
		return nil, safeError("解密密钥失败，请在原计算机上运行并检查文件权限", err)
	}
	if err := ctx.Err(); err != nil {
		clear(plain)
		return nil, fmt.Errorf("读取密钥已取消：%w", err)
	}
	return plain, nil
}

func (s *FileStore) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("列出密钥已取消：%w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(file.Values))
	for name := range file.Values {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (s *FileStore) save(contents secretFile) error {
	directory := filepath.Join(s.home, "data")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建密钥目录失败：%w", err)
	}
	file, err := os.CreateTemp(directory, "secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("创建密钥临时文件失败：%w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	// Machine-level ciphertext is decryptable by other accounts, so restrict the temporary file first.
	if err := s.policy.Secure(temporary, contents.Owner); err != nil {
		return safeError("设置密钥文件权限失败，密钥未保存", err)
	}
	if err := json.NewEncoder(file).Encode(contents); err != nil {
		return safeError("写入加密密钥失败", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("刷新密钥文件失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭密钥临时文件失败：%w", err)
	}
	if err := replaceFile(temporary, filepath.Join(directory, "secrets.json")); err != nil {
		return fmt.Errorf("发布密钥文件失败：%w", err)
	}
	return nil
}
