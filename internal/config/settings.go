package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Settings struct {
	Retention     Retention `yaml:"retention,omitempty" json:"retention,omitempty"`
	HTTPListen    string    `yaml:"httpListen,omitempty" json:"httpListen,omitempty" jsonschema_description:"HTTP API 监听地址，默认 127.0.0.1:7444。"`
	PublicBaseURL string    `yaml:"publicBaseUrl,omitempty" json:"publicBaseUrl,omitempty" jsonschema:"format=uri" jsonschema_description:"卡片打开面板按钮使用的 HTTP/HTTPS 基础地址。"`
}

func (s Settings) HTTPAddress() string {
	if s.HTTPListen == "" {
		return "127.0.0.1:7444"
	}
	return s.HTTPListen
}

type settingIssue struct{ field, message string }

func (s Settings) validateHTTP() []settingIssue {
	var issues []settingIssue
	host, port, err := net.SplitHostPort(s.HTTPAddress())
	number, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || number < 0 || number > 65535 || strings.Trim(port, "0123456789") != "" || strings.ContainsAny(host, " \t\r\n/\\?#") {
		issues = append(issues, settingIssue{"httpListen", "httpListen 必须是 主机:端口 格式，IPv6 地址须加方括号"})
	}
	if s.PublicBaseURL != "" {
		address, err := url.Parse(s.PublicBaseURL)
		if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Hostname() == "" || address.User != nil || address.RawQuery != "" || address.Fragment != "" {
			issues = append(issues, settingIssue{"publicBaseUrl", "publicBaseUrl 必须是完整 HTTP/HTTPS 基础地址，不能包含凭据、查询参数或片段"})
		}
	}
	return issues
}

type Retention struct {
	Raw    Duration `yaml:"raw,omitempty" json:"raw,omitempty" jsonschema_description:"原始数据保留时长，默认 24h。"`
	Minute Duration `yaml:"minute,omitempty" json:"minute,omitempty" jsonschema_description:"分钟汇总保留时长，默认 336h（14 天）。"`
	Hour   Duration `yaml:"hour,omitempty" json:"hour,omitempty" jsonschema_description:"小时汇总保留时长，默认 8760h（365 天）。"`
}

type RetentionDurations struct{ Raw, Minute, Hour time.Duration }

func (r Retention) Effective() (RetentionDurations, error) {
	result := RetentionDurations{Raw: 24 * time.Hour, Minute: 14 * 24 * time.Hour, Hour: 365 * 24 * time.Hour}
	for _, field := range []struct {
		name  string
		value Duration
		out   *time.Duration
	}{{"raw", r.Raw, &result.Raw}, {"minute", r.Minute, &result.Minute}, {"hour", r.Hour, &result.Hour}} {
		if field.value == "" {
			continue
		}
		value, err := field.value.Parse()
		if err != nil || value < time.Millisecond {
			return result, fmt.Errorf("retention.%s 必须为至少 1ms 的正数时长，例如 24h 或 336h", field.name)
		}
		*field.out = value
	}
	if result.Raw < 24*time.Hour {
		return result, fmt.Errorf("原始数据的保留时长不能短于 24h，以覆盖离线缓存和规则回放")
	}
	if result.Minute < result.Raw {
		return result, fmt.Errorf("分钟级数据的保留时长（%s）不能短于原始数据（%s）", retentionText(r.Minute, "14d"), retentionText(r.Raw, "24h"))
	}
	if result.Hour < result.Minute {
		return result, fmt.Errorf("小时级数据的保留时长（%s）不能短于分钟级数据（%s）", retentionText(r.Hour, "365d"), retentionText(r.Minute, "14d"))
	}
	return result, nil
}

func retentionText(value Duration, fallback string) string {
	if value == "" {
		return fallback
	}
	return string(value)
}
