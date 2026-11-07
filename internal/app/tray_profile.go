package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type TrayProtector interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

type TrayProfile struct {
	Directory string
	Protector TrayProtector
}

type TrayRegistration struct {
	URL     string `json:"url"`
	TokenID string `json:"tokenId"`
	Name    string `json:"name"`
	Home    string `json:"home"`
}

func (p TrayProfile) Registration() (TrayRegistration, error) {
	file, err := os.Open(filepath.Join(p.Directory, "tray.json"))
	if errors.Is(err, os.ErrNotExist) {
		return TrayRegistration{URL: "http://127.0.0.1:7444"}, nil
	}
	if err != nil {
		return TrayRegistration{}, fmt.Errorf("读取托盘配置失败：%w", err)
	}
	defer file.Close()
	var r TrayRegistration
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &r) != nil || !trayPanelURL(r.URL) {
		return TrayRegistration{}, fmt.Errorf("托盘配置无效，请重新运行 lgmon tray setup")
	}
	return r, nil
}

func (p TrayProfile) Load() (string, []byte, error) {
	r, err := p.Registration()
	if err != nil {
		return "http://127.0.0.1:7444", nil, err
	}
	file, err := os.Open(filepath.Join(p.Directory, "tray-token"))
	if err != nil {
		return r.URL, nil, fmt.Errorf("读取托盘令牌失败：%w", err)
	}
	defer file.Close()
	cipher, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(cipher) > 65536 || p.Protector == nil {
		return r.URL, nil, fmt.Errorf("托盘令牌无效")
	}
	token, err := p.Protector.Decrypt(cipher)
	if err != nil {
		return r.URL, nil, fmt.Errorf("当前账户不能解密托盘令牌")
	}
	if len(token) < 1 || len(token) > 512 {
		clear(token)
		return r.URL, nil, fmt.Errorf("托盘令牌格式无效")
	}
	return r.URL, token, nil
}

func (p TrayProfile) Save(r TrayRegistration, token []byte) error {
	if p.Protector == nil {
		return fmt.Errorf("托盘缺少用户加密接口")
	}
	cipher, err := p.Protector.Encrypt(token)
	if err != nil {
		return fmt.Errorf("加密托盘令牌失败：%w", err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("编码托盘配置失败：%w", err)
	}
	if err := os.MkdirAll(p.Directory, 0700); err != nil {
		return fmt.Errorf("创建托盘目录失败：%w", err)
	}
	for _, item := range []struct {
		name string
		data []byte
	}{{"tray-token", cipher}, {"tray.json", data}} {
		f, err := os.CreateTemp(p.Directory, ".tray-")
		if err != nil {
			return fmt.Errorf("创建托盘临时文件失败：%w", err)
		}
		temp := f.Name()
		writeErr := f.Chmod(0600)
		if writeErr == nil {
			_, writeErr = f.Write(item.data)
		}
		if writeErr == nil {
			writeErr = f.Sync()
		}
		err = errors.Join(writeErr, f.Close())
		if err == nil {
			err = replaceTrayFile(temp, filepath.Join(p.Directory, item.name))
		}
		_ = os.Remove(temp)
		if err != nil {
			return fmt.Errorf("保存托盘文件失败：%w", err)
		}
	}
	return nil
}

func (p TrayProfile) Remove() error {
	for _, name := range []string{"tray-token", "tray.json"} {
		if err := os.Remove(filepath.Join(p.Directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除托盘文件失败：%w", err)
		}
	}
	return nil
}

func TrayMonitorURL(listen string) (string, error) {
	address := listenerURL(listen)
	if !trayPanelURL(address) {
		return "", fmt.Errorf("HTTP 监听地址无效")
	}
	return address, nil
}
