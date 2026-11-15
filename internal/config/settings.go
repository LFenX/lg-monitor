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
	Assistant         AssistantSettings     `yaml:"assistant,omitempty" json:"assistant,omitempty"`
	Collection        CollectionSettings    `yaml:"collection,omitempty" json:"collection,omitempty"`
	CollectorMonitors []CollectorMonitor    `yaml:"collectorMonitors,omitempty" json:"collectorMonitors,omitempty" jsonschema:"maxItems=7" jsonschema_description:"本机附加监控器，HTTP/MCP 只读，最多 7 项；每个独立使用 cache.maxSize，修改后重启采集器生效。证书先通过 pair join --name 配对。"`
	Exports           ExportSettings        `yaml:"exports,omitempty" json:"exports,omitempty"`
	ProcessEvents     ProcessEventsSettings `yaml:"processEvents,omitempty" json:"processEvents,omitempty"`
	RemoteOperations  RemoteOperations      `yaml:"remoteOperations,omitempty" json:"remoteOperations,omitempty"`
	Inventory         InventorySettings     `yaml:"inventory,omitempty" json:"inventory,omitempty"`
	LocalAccess       string                `yaml:"localAccess,omitempty" json:"localAccess,omitempty" jsonschema:"enum=read,enum=config" jsonschema_description:"在这台电脑上直接打开网页时的权限。read 只能查看；config 可以在网页里添加服务、建面板、确认告警。省略时：以本人身份运行（个人目录）为 config，以系统服务运行为 read。修改设置和密钥始终需要管理员令牌。"`
	ServiceSwitch     ServiceSwitchSettings `yaml:"serviceSwitch,omitempty" json:"serviceSwitch,omitempty"`
	Cache             CacheSettings         `yaml:"cache,omitempty" json:"cache,omitempty"`
	Retention         Retention             `yaml:"retention,omitempty" json:"retention,omitempty"`
	HTTPListen        string                `yaml:"httpListen,omitempty" json:"httpListen,omitempty" jsonschema_description:"HTTP API 监听地址，默认 127.0.0.1:7444。"`
	PublicBaseURL     string                `yaml:"publicBaseUrl,omitempty" json:"publicBaseUrl,omitempty" jsonschema:"format=uri" jsonschema_description:"卡片打开面板按钮使用的 HTTP/HTTPS 基础地址。"`
	LAN               LANSettings           `yaml:"lan,omitempty" json:"lan,omitempty" jsonschema_description:"内网可见：打开后同一局域网的 lgmon 能看到面板名称和简介，看具体状态需要申请并经本人同意。"`
	TeamListen        string                `yaml:"teamListen,omitempty" json:"teamListen,omitempty" jsonschema_description:"仅有自建团队时监听成员 HTTPS，默认 127.0.0.1:7445。"`
	TeamPublicURL     string                `yaml:"teamPublicUrl,omitempty" json:"teamPublicUrl,omitempty" jsonschema:"format=uri" jsonschema_description:"邀请中的成员 HTTPS 基础地址；省略时用实际团队监听地址。"`
}

type ProcessEventsSettings struct {
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty" jsonschema:"enum=,enum=auto,enum=disabled,default=auto" jsonschema_description:"本机即时进程事件：auto 使用自有 ETW 会话，disabled 使用原间隔采样；省略或空值为 auto，重启采集器生效。"`
}

func (s ProcessEventsSettings) Enabled() (bool, error) {
	switch s.Mode {
	case "", "auto":
		return true, nil
	case "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("processEvents.mode 必须为 auto 或 disabled")
	}
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

// LANSettings say whether this monitor answers on the local network and under which name.
type LANSettings struct {
	Visible bool   `yaml:"visible,omitempty" json:"visible,omitempty" jsonschema_description:"打开后回应局域网发现，并在成员端口公开面板名称和简介；默认关闭。"`
	Name    string `yaml:"name,omitempty" json:"name,omitempty" jsonschema:"maxLength=40" jsonschema_description:"别人在内网里看到的名字，省略时用电脑名。"`
}

func (s Settings) TeamAddress() string {
	if s.TeamListen == "" {
		if s.LAN.Visible {
			// Being visible on the network means listening on it; the explicit setting still wins.
			return "0.0.0.0:7445"
		}
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
