package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

var ErrServiceSwitchNotConfigured = errors.New("请先在采集器本机 settings.yaml 设置 serviceSwitch.path，然后重启采集器")

type ServiceSwitchError struct {
	Message string
	Cause   error
}

func (e *ServiceSwitchError) Error() string { return e.Message }
func (e *ServiceSwitchError) Unwrap() error { return e.Cause }

type ServiceSwitchSource interface {
	ReadServiceSwitch(context.Context) (config.ServiceSwitchSnapshot, error)
}

type ServiceSwitchFiles interface {
	OpenServiceSwitch(context.Context, string) (io.ReadCloser, error)
}

type NativeServiceSwitchFiles struct{}

func (NativeServiceSwitchFiles) OpenServiceSwitch(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("不是可读取的普通文件")
	}
	return file, nil
}

type ServiceSwitchReader struct {
	Settings config.ServiceSwitchSettings
	Files    ServiceSwitchFiles
	Now      func() time.Time
}

func (s ServiceSwitchReader) ReadServiceSwitch(ctx context.Context) (config.ServiceSwitchSnapshot, error) {
	var result config.ServiceSwitchSnapshot
	if s.Settings.Path == "" {
		return result, ErrServiceSwitchNotConfigured
	}
	if err := s.Settings.Validate(); err != nil {
		return result, &ServiceSwitchError{Message: err.Error()}
	}
	if s.Files == nil || s.Now == nil {
		return result, &ServiceSwitchError{Message: "服务开关读取依赖未配置"}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	file, err := s.Files.OpenServiceSwitch(ctx, s.Settings.Path)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, &ServiceSwitchError{Message: "services.json 不存在或无法读取，请在采集器本机检查固定路径和权限", Cause: fmt.Errorf("打开固定来源失败：%w", err)}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, config.MaxServiceSwitchFile+1))
	if err != nil {
		return result, &ServiceSwitchError{Message: "读取 services.json 失败，请在采集器本机检查文件", Cause: fmt.Errorf("读取固定来源失败：%w", err)}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Summary, err = config.ParseServiceSwitch(data)
	if err != nil {
		return result, &ServiceSwitchError{Message: err.Error(), Cause: err}
	}
	result.SHA256, _, err = config.ServiceSwitchFingerprint(result.Summary)
	result.ReadAt = s.Now().UnixMilli()
	return result, err
}
