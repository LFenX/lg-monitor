package blueprint

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
)

// MonitorInput is everything needed to start watching one thing.
// At least one of port, process, service or url says what to watch.
type MonitorInput struct {
	Name      string   `json:"name" jsonschema:"minLength=1,maxLength=80" jsonschema_description:"给人看的名字，例如“博客网站”。"`
	ID        string   `json:"id,omitempty" jsonschema:"pattern=^[a-z0-9-]+$,maxLength=48" jsonschema_description:"稳定 ID；省略时自动生成。已存在同 ID 时更新它。"`
	Collector string   `json:"collector,omitempty" jsonschema_description:"采集器 ID 或主机名；只有一台电脑时省略。"`
	Port      *int     `json:"port,omitempty" jsonschema:"minimum=1,maximum=65535" jsonschema_description:"本机监听端口；会匹配占用它的进程并每 10 秒做一次 TCP 探测。"`
	Process   string   `json:"process,omitempty" jsonschema_description:"进程名，例如 nginx 或 nginx.exe；进程消失时告警。"`
	Service   string   `json:"service,omitempty" jsonschema_description:"Windows 服务名（不是显示名），例如 postgresql-x64-16；服务停止时告警。"`
	URL       string   `json:"url,omitempty" jsonschema:"pattern=^https?://" jsonschema_description:"要定期访问的网址；返回非 2xx/3xx 或超时会告警，HTTPS 还会在证书快到期时提醒。"`
	Cmdline   string   `json:"cmdline,omitempty" jsonschema_description:"命令行正则，用来区分同名进程；一般不需要。"`
	Severity  string   `json:"severity,omitempty" jsonschema:"enum=critical,enum=warning,enum=info" jsonschema_description:"告警严重度，默认 critical。"`
	For       string   `json:"for,omitempty" jsonschema_description:"故障持续多久才告警，默认 20s。"`
	Notify    []string `json:"notify,omitempty" jsonschema_description:"通知渠道 ID；省略时使用全部已有渠道，一个都没有时自动创建本机 Windows 通知。"`
	Tags      []string `json:"tags,omitempty"`
	Dashboard *bool    `json:"dashboard,omitempty" jsonschema_description:"是否同时生成一页面板，默认 true。"`
}

// ProbeAnchor is the process an external URL monitor is attached to.
// Every target needs a local match and the collector itself performs the probe, so its own
// process is always present; the monitor's real health comes from the probe results.
const ProbeAnchor = "lgmon.exe"

// ProbeOnly reports whether a target exists only to probe a URL from the collector.
func ProbeOnly(target config.Target) bool {
	if target.Match != (config.Match{Process: ProbeAnchor}) {
		return false
	}
	return slices.ContainsFunc(target.Probes, func(probe config.Probe) bool { return probe.Type == "http" })
}

func loopbackPort(address *url.URL) (int, bool) {
	host := strings.ToLower(address.Hostname())
	if host != "localhost" && host != "::1" && !strings.HasPrefix(host, "127.") {
		return 0, false
	}
	if text := address.Port(); text != "" {
		value, err := strconv.Atoi(text)
		return value, err == nil && value >= 1 && value <= 65535
	}
	if address.Scheme == "https" {
		return 443, true
	}
	return 80, true
}

// MonitorPlan is the configuration a MonitorInput expands into.
type MonitorPlan struct {
	Target   config.Target
	Rules    []config.Rule
	Template string
}

// Slug derives a stable lowercase ID from free text; it returns "" when nothing usable remains.
func Slug(text string) string {
	var out strings.Builder
	dash := true
	for _, char := range strings.ToLower(text) {
		switch {
		case (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9'):
			out.WriteRune(char)
			dash = false
		case !dash:
			out.WriteByte('-')
			dash = true
		}
	}
	value := strings.Trim(out.String(), "-")
	if len(value) > 48 {
		value = strings.Trim(value[:48], "-")
	}
	return value
}

// Plan expands the input into one target and its alert rules. notify must already be resolved.
func Plan(input MonitorInput, notify []string) (MonitorPlan, error) {
	var plan MonitorPlan
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return plan, fmt.Errorf("请提供 name，例如“博客网站”")
	}
	process, service, address := strings.TrimSpace(input.Process), strings.TrimSpace(input.Service), strings.TrimSpace(input.URL)
	if input.Port == nil && process == "" && service == "" && address == "" {
		return plan, fmt.Errorf("至少提供 port、process、service 或 url 之一，说明要监控什么；不确定时先调用 explore 查看这台电脑上有什么")
	}
	if input.Cmdline != "" && process == "" {
		return plan, fmt.Errorf("cmdline 需要同时提供 process")
	}
	var host string
	var parsed *url.URL
	if address != "" {
		var err error
		parsed, err = url.Parse(address)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return plan, fmt.Errorf("url 必须是完整的 http:// 或 https:// 地址")
		}
		host = parsed.Hostname()
	}
	id := input.ID
	if id == "" {
		id = Slug(name)
	}
	if id == "" {
		// A name without ASCII letters still needs a readable, stable ID.
		switch {
		case process != "":
			id = Slug(strings.TrimSuffix(strings.ToLower(process), ".exe"))
		case service != "":
			id = Slug(service)
		case input.Port != nil:
			id = "port-" + strconv.Itoa(*input.Port)
		default:
			id = Slug(host)
		}
	}
	if !idPattern.MatchString(id) {
		return plan, fmt.Errorf("无法从名字生成 ID，请提供由小写字母、数字或横线组成的 id")
	}
	severity := input.Severity
	if severity == "" {
		severity = "critical"
	}
	if !slices.Contains([]string{"critical", "warning", "info"}, severity) {
		return plan, fmt.Errorf("severity 必须是 critical、warning 或 info")
	}
	hold := config.Duration(input.For)
	if hold == "" {
		hold = "20s"
	}
	if duration, err := hold.Parse(); err != nil || duration < 0 {
		return plan, fmt.Errorf("for 必须是 20s、1m 这样的时长")
	}
	if len(notify) == 0 {
		return plan, fmt.Errorf("没有可用的通知渠道")
	}
	target := config.Target{ID: id, Name: name, Collector: strings.TrimSpace(input.Collector), Tags: input.Tags}
	target.Match = config.Match{Process: process, Service: service, Cmdline: input.Cmdline, Port: input.Port}
	if parsed != nil && input.Port == nil && process == "" && service == "" {
		if port, ok := loopbackPort(parsed); ok {
			// A local site is served by whatever listens on its port, so that is what it watches.
			target.Match.Port = &port
		} else {
			target.Match.Process = ProbeAnchor
		}
	}
	rule := func(suffix, kind, label string) config.Rule {
		return config.Rule{ID: id + "-" + suffix, Name: name + label, Type: kind, Target: id, For: hold, Severity: severity, Notify: notify}
	}
	if process != "" {
		plan.Rules = append(plan.Rules, rule("process-down", "process_absent", " 进程不在"))
	}
	if service != "" {
		plan.Rules = append(plan.Rules, rule("service-stopped", "service_stopped", " 服务停止"))
	}
	if input.Port != nil {
		target.Probes = append(target.Probes, config.Probe{Type: "tcp", Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(*input.Port)), Interval: "10s", Timeout: "2s"})
	}
	plan.Template = "service-health"
	if address != "" {
		target.Probes = append(target.Probes, config.Probe{Type: "http", URL: address, Interval: "15s", Timeout: "5s", ExpectStatus: "<400"})
		plan.Template = "website"
		if strings.HasPrefix(strings.ToLower(address), "https://") {
			certificate := config.Rule{ID: id + "-cert-expiry", Name: name + " 证书即将到期", Type: "cert_expiry", Target: id, Severity: "warning", Notify: notify, Within: "14d"}
			plan.Rules = append(plan.Rules, certificate)
		}
	}
	if len(target.Probes) > 0 {
		plan.Rules = append(plan.Rules, rule("unreachable", "probe_failed", " 无法访问"))
	}
	plan.Target = target
	return plan, nil
}
