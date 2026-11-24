//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Without DPAPI the vault is sealed with a random key kept next to it, readable only by its owner. File
// permissions do the protecting, as they do for an SSH key; the key file keeps secrets.json from ever
// holding plain text, so a copied or backed-up vault alone reveals nothing.
type keyFileProtector struct {
	path string
	mu   sync.Mutex
	key  []byte
}

type filePolicy struct{}

func nativeAdapters(home string) (Protector, AccessPolicy, error) {
	if home == "" {
		return nil, nil, fmt.Errorf("密钥库需要数据目录")
	}
	return &keyFileProtector{path: filepath.Join(home, "data", "secrets.key")}, filePolicy{}, nil
}

func (p *keyFileProtector) cipher() (cipher.AEAD, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key == nil {
		key, err := os.ReadFile(p.path)
		if errors.Is(err, fs.ErrNotExist) {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, fmt.Errorf("生成密钥库主密钥失败：%w", err)
			}
			if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
				return nil, fmt.Errorf("创建密钥目录失败：%w", err)
			}
			// O_EXCL: two processes creating the key at once must not each keep a different one.
			file, createErr := os.OpenFile(p.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(createErr, fs.ErrExist) {
				key, err = os.ReadFile(p.path)
			} else if createErr != nil {
				return nil, fmt.Errorf("保存密钥库主密钥失败：%w", createErr)
			} else {
				_, err = file.Write(key)
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("读取密钥库主密钥失败：%w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("密钥库主密钥 %s 已损坏，请恢复备份", p.path)
		}
		p.key = key
	}
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (p *keyFileProtector) Encrypt(value []byte) ([]byte, error) {
	aead, err := p.cipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, value, nil), nil
}

func (p *keyFileProtector) Decrypt(value []byte) ([]byte, error) {
	aead, err := p.cipher()
	if err != nil {
		return nil, err
	}
	if len(value) < aead.NonceSize() {
		return nil, fmt.Errorf("密文太短")
	}
	return aead.Open(nil, value[:aead.NonceSize()], value[aead.NonceSize():], nil)
}

func (filePolicy) CurrentUser() (string, error) { return "uid:" + strconv.Itoa(os.Geteuid()), nil }

func (filePolicy) Secure(path, _ string) error { return os.Chmod(path, 0o600) }

func replaceFile(temporary, destination string) error { return os.Rename(temporary, destination) }
