package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Settings struct {
	RemoteOperations RemoteOperations      `yaml:"remoteOperations,omitempty" json:"remoteOperations,omitempty"`
	Inventory        InventorySettings     `yaml:"inventory,omitempty" json:"inventory,omitempty"`
	ServiceSwitch    ServiceSwitchSettings `yaml:"serviceSwitch,omitempty" json:"serviceSwitch,omitempty"`
	Cache            CacheSettings         `yaml:"cache,omitempty" json:"cache,omitempty"`
	Retention        Retention             `yaml:"retention,omitempty" json:"retention,omitempty"`
	HTTPListen       string                `yaml:"httpListen,omitempty" json:"httpListen,omitempty" jsonschema_description:"HTTP API 监听地址，默认 127.0.0.1:7444。"`
	PublicBaseURL    string                `yaml:"publicBaseUrl,omitempty" json:"publicBaseUrl,omitempty" jsonschema:"format=uri" jsonschema_description:"卡片打开面板按钮使用的 HTTP/HTTPS 基础地址。"`
	TeamListen       string                `yaml:"teamListen,omitempty" json:"teamListen,omitempty" jsonschema_description:"仅有自建团队时监听成员 HTTPS，默认 127.0.0.1:7445。"`
	TeamPublicURL    string                `yaml:"teamPublicUrl,omitempty" json:"teamPublicUrl,omitempty" jsonschema:"format=uri" jsonschema_description:"邀请中的成员 HTTPS 基础地址；省略时用实际团队监听地址。"`
}

type InventorySettings struct {
	Interval Duration `yaml:"interval,omitempty" json:"interval,omitempty" jsonschema_description:"本机启动项和计划任务采集间隔，默认 1m，10s～1h，重启采集器生效。"`
}

func (s InventorySettings) EffectiveInterval() (time.Duration, error) {
	if s.Interval == "" {
		return time.Minute, nil
	}
	value, err := s.Interval.Parse()
	if err != nil || value < 10*time.Second || value > time.Hour {
		return 0, fmt.Errorf("inventory.interval 必须是 10s～1h 的时长")
	}
	return value, nil
}

func (s Settings) HTTPAddress() string {
	if s.HTTPListen == "" {
		return "127.0.0.1:7444"
	}
	return s.HTTPListen
}

func (s Settings) TeamAddress() string {
	if s.TeamListen == "" {
		return "127.0.0.1:7445"
	}
	return s.TeamListen
}

const HTTPWarningText = "正在通过不加密的 HTTP 在局域网提供面板，同一网络里的人可能截获令牌。建议改为只监听 Tailscale 地址"

var tailscaleHTTPNetworks = [...]netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

func (s Settings) HTTPWarning() string {
	host, _, err := net.SplitHostPort(s.HTTPAddress())
	if err != nil {
		return HTTPWarningText
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return ""
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		// Do not infer an encrypted route from a DNS name.
		return HTTPWarningText
	}
	address = address.Unmap().WithZone("")
	if address.IsLoopback() {
		return ""
	}
	for _, network := range tailscaleHTTPNetworks {
		if network.Contains(address) {
			return ""
		}
	}
	return HTTPWarningText
}

type settingIssue struct{ field, message string }

func (s Settings) validateHTTP() []settingIssue {
	var issues []settingIssue
	teamHost, teamPort, teamErr := net.SplitHostPort(s.TeamAddress())
	teamNumber, teamParse := strconv.Atoi(teamPort)
	if teamErr != nil || teamParse != nil || teamNumber < 0 || teamNumber > 65535 || strings.Trim(teamPort, "0123456789") != "" || strings.ContainsAny(teamHost, " \t\r\n/\\?#") {
		issues = append(issues, settingIssue{"teamListen", "teamListen 必须是 主机:端口 格式，IPv6 地址须加方括号"})
	}
	if s.TeamPublicURL != "" {
		u, err := url.Parse(s.TeamPublicURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
			issues = append(issues, settingIssue{"teamPublicUrl", "teamPublicUrl 必须为 HTTPS 基础地址，不含凭据、路径、查询参数或片段"})
		} else if u.Port() != "" {
			if _, err := net.LookupPort("tcp", u.Port()); err != nil {
				issues = append(issues, settingIssue{"teamPublicUrl", "teamPublicUrl 端口必须在 0 到 65535 之间"})
			}
		}
	}
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
