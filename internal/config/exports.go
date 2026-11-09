package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type ExportSettings struct {
	Prometheus PrometheusSettings `yaml:"prometheus,omitempty" json:"prometheus,omitempty"`
	OTLP       OTLPSettings       `yaml:"otlp,omitempty" json:"otlp,omitempty"`
}

type PrometheusSettings struct {
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty" jsonschema_description:"启用带 read 鉴权的 GET /metrics，默认关闭。"`
}

type OTLPSettings struct {
	Enabled          bool      `yaml:"enabled,omitempty" json:"enabled,omitempty" jsonschema_description:"启用监控器 OTLP/HTTP protobuf 指标推送，默认关闭。"`
	Endpoint         string    `yaml:"endpoint,omitempty" json:"endpoint,omitempty" jsonschema:"maxLength=2048" jsonschema_description:"完整指标接收 URL，例如 http://127.0.0.1:4318/v1/metrics；远端必须 HTTPS，不允许用户信息、查询或片段。"`
	Interval         Duration  `yaml:"interval,omitempty" json:"interval,omitempty" jsonschema_description:"推送周期，默认 30s，范围 10s～1h。"`
	Timeout          Duration  `yaml:"timeout,omitempty" json:"timeout,omitempty" jsonschema_description:"整个导出周期预算，默认 10s，范围 1s～30s。"`
	AuthorizationRef string    `yaml:"authorizationRef,omitempty" json:"authorizationRef,omitempty" jsonschema:"maxLength=256" jsonschema_description:"密钥库中完整 Authorization 请求头的引用，不填写凭据正文。"`
	TLS              ExportTLS `yaml:"tls,omitempty" json:"tls,omitempty"`
}

type ExportTLS struct {
	CARef   string `yaml:"caRef,omitempty" json:"caRef,omitempty" jsonschema:"maxLength=256" jsonschema_description:"可选 PEM 根证书的加密密钥引用，追加到系统可信根。"`
	CertRef string `yaml:"certRef,omitempty" json:"certRef,omitempty" jsonschema:"maxLength=256" jsonschema_description:"可选 mTLS 客户端 PEM 证书引用，与 keyRef 成对。"`
	KeyRef  string `yaml:"keyRef,omitempty" json:"keyRef,omitempty" jsonschema:"maxLength=256" jsonschema_description:"可选 mTLS 客户端私钥引用，与 certRef 成对。"`
}

func (s OTLPSettings) EffectiveInterval() time.Duration {
	if s.Interval == "" {
		return 30 * time.Second
	}
	d, _ := s.Interval.Parse()
	return d
}

func (s OTLPSettings) EffectiveTimeout() time.Duration {
	if s.Timeout == "" {
		return 10 * time.Second
	}
	d, _ := s.Timeout.Parse()
	return d
}

func (s OTLPSettings) EndpointURL() (*url.URL, error) {
	u, err := url.Parse(s.Endpoint)
	if err != nil || len(s.Endpoint) > 2048 || u == nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("OTLP endpoint 必须是完整 HTTPS 指标 URL，不能含用户信息、查询或片段")
	}
	host := u.Hostname()
	if strings.ContainsAny(host, "*%\\") || strings.IndexFunc(host, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("OTLP endpoint 的主机无效")
	}
	ip, parseErr := netip.ParseAddr(host)
	if parseErr == nil && (ip.IsUnspecified() || ip.IsMulticast()) {
		return nil, fmt.Errorf("OTLP endpoint 不能是通配或组播地址")
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("OTLP endpoint 端口必须为 1～65535")
		}
	}
	if u.Scheme == "http" && !((parseErr == nil && ip.Unmap().IsLoopback()) || strings.EqualFold(host, "localhost")) {
		return nil, fmt.Errorf("OTLP 明文 HTTP 只允许回环地址，远端请使用 HTTPS")
	}
	return u, nil
}

type exportSettingIssue struct{ path, message string }

func (s ExportSettings) issues() []exportSettingIssue {
	var result []exportSettingIssue
	add := func(path, message string) { result = append(result, exportSettingIssue{path, message}) }
	o := s.OTLP
	if o.Enabled || o.Endpoint != "" {
		if _, err := o.EndpointURL(); err != nil {
			add("otlp.endpoint", err.Error())
		}
	}
	for _, item := range []struct {
		path             string
		value            Duration
		fallback, lo, hi time.Duration
	}{
		{"otlp.interval", o.Interval, 30 * time.Second, 10 * time.Second, time.Hour},
		{"otlp.timeout", o.Timeout, 10 * time.Second, time.Second, 30 * time.Second},
	} {
		d, err := item.value.Parse()
		if item.value == "" {
			d, err = item.fallback, nil
		}
		if err != nil || d < item.lo || d > item.hi {
			add(item.path, fmt.Sprintf("%s 必须在 %s～%s 之间", item.path, item.lo, item.hi))
		}
	}
	for _, item := range []struct{ path, ref string }{{"otlp.authorizationRef", o.AuthorizationRef}, {"otlp.tls.caRef", o.TLS.CARef}, {"otlp.tls.certRef", o.TLS.CertRef}, {"otlp.tls.keyRef", o.TLS.KeyRef}} {
		path, ref := item.path, item.ref
		if len(ref) > 256 || strings.IndexFunc(ref, unicode.IsControl) >= 0 || (ref != "" && strings.TrimSpace(ref) != ref) {
			add(path, "密钥引用最多 256 字节，不能含控制字符或首尾空白")
		}
	}
	if (o.TLS.CertRef == "") != (o.TLS.KeyRef == "") {
		add("otlp.tls.certRef", "mTLS certRef 与 keyRef 必须成对填写")
	}
	if o.Endpoint != "" && strings.HasPrefix(o.Endpoint, "http:") && (o.TLS.CARef != "" || o.TLS.CertRef != "" || o.TLS.KeyRef != "") {
		add("otlp.tls", "TLS 引用只能用于 HTTPS endpoint")
	}
	return result
}

func (s ExportSettings) Validate() error {
	if issues := s.issues(); len(issues) > 0 {
		return fmt.Errorf("exports.%s: %s", issues[0].path, issues[0].message)
	}
	return nil
}
