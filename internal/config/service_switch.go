package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const MaxServiceSwitchFile = 1024 * 1024
const MaxServiceSwitchSummary = 4*1024*1024 - 4096

type ServiceSwitchSettings struct {
	Path string `yaml:"path,omitempty" json:"path,omitempty" jsonschema_description:"仅本机设置的 services.json Windows 绝对路径，不经 HTTP 修改或下发；重启采集器生效。"`
}

func (s ServiceSwitchSettings) Validate() error {
	if s.Path == "" {
		return nil
	}
	value := strings.ReplaceAll(s.Path, "/", `\`)
	if strings.HasPrefix(value, `\\?\`) || strings.HasPrefix(value, `\\.\`) {
		return fmt.Errorf("serviceSwitch.path 不能是 Windows 设备路径")
	}
	clean, err := CleanWindowsDirectory(value)
	if err != nil || !strings.EqualFold(clean[strings.LastIndex(clean, `\`)+1:], "services.json") || strings.HasSuffix(value, `\`) {
		return fmt.Errorf("serviceSwitch.path 必须是文件名为 services.json 的 Windows 绝对路径")
	}
	return nil
}

type TargetSource struct {
	Kind      string `yaml:"kind" json:"kind" jsonschema:"enum=service-switch"`
	Collector string `yaml:"collector" json:"collector" jsonschema:"minLength=1"`
	Ref       string `yaml:"ref" json:"ref" jsonschema:"maxLength=47,pattern=^((app:|tunnel:)[a-z0-9][a-z0-9-]*|database)$"`
}

var serviceSwitchID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var serviceSwitchRef = regexp.MustCompile(`^(app:[a-z0-9][a-z0-9-]{0,39}|tunnel:[a-z0-9][a-z0-9-]{0,39}|database)$`)

type ServiceSwitchApp struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Root       string `json:"root,omitempty"`
	Ports      []int  `json:"ports"`
	HealthPath string `json:"healthPath"`
	Domain     string `json:"domain,omitempty"`
	Controller string `json:"controller,omitempty"`
	Guardian   struct {
		StatusURL string `json:"statusUrl,omitempty"`
	} `json:"guardian"`
	Tunnel struct {
		MetricsPort int `json:"metricsPort,omitempty"`
	} `json:"tunnel"`
	Autostart string `json:"autostart,omitempty"`
}

type ServiceSwitchDatabase struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Ctl  string `json:"ctl"`
}

type ServiceSwitchSkip struct {
	Ref    string `json:"ref"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// This allowlist is also the wire format; source commands and environment never leave the collector.
type ServiceSwitchSummary struct {
	Apps     []ServiceSwitchApp     `json:"apps"`
	Database *ServiceSwitchDatabase `json:"database,omitempty"`
	Skipped  []ServiceSwitchSkip    `json:"skipped"`
}

type ServiceSwitchSnapshot struct {
	Summary ServiceSwitchSummary `json:"summary"`
	SHA256  string               `json:"sha256"`
	ReadAt  int64                `json:"readAt"`
}

func ServiceSwitchFingerprint(summary ServiceSwitchSummary) (string, []byte, error) {
	data, err := json.Marshal(summary)
	if err != nil {
		return "", nil, fmt.Errorf("编码服务开关精简结果失败：%w", err)
	}
	if len(data) > MaxServiceSwitchSummary {
		return "", nil, fmt.Errorf("服务开关精简结果超过传输消息大小限制，请减少登记项")
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}

func ParseServiceSwitch(data []byte) (ServiceSwitchSummary, error) {
	result := ServiceSwitchSummary{Apps: []ServiceSwitchApp{}, Skipped: []ServiceSwitchSkip{}}
	if len(data) > MaxServiceSwitchFile || !utf8.Valid(data) {
		return result, fmt.Errorf("services.json 必须是至多 1 MiB 的 UTF-8 JSON 文件")
	}
	var source struct {
		SchemaVersion int               `json:"schemaVersion"`
		Apps          []json.RawMessage `json:"apps"`
		Database      json.RawMessage   `json:"database"`
		Scripts       json.RawMessage   `json:"scripts"`
	}
	if err := json.Unmarshal(data, &source); err != nil || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return result, fmt.Errorf("services.json 的 JSON 或顶层字段类型不合法")
	}
	if source.SchemaVersion != 1 {
		return result, fmt.Errorf("services.json 仅支持 schemaVersion: 1")
	}
	counts := map[string]int{}
	skippedRefs := map[string]bool{}
	for _, raw := range source.Apps {
		var app struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &app) == nil && serviceSwitchID.MatchString(app.ID) {
			counts[app.ID]++
		}
	}
	for i, raw := range source.Apps {
		app := ServiceSwitchApp{HealthPath: "/"}
		reason := ""
		if json.Unmarshal(raw, &app) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			reason = "应用字段类型不合法"
		} else {
			reason = validateServiceSwitchApp(app)
			var fields struct {
				Tunnel struct {
					MetricsPort *int `json:"metricsPort"`
				} `json:"tunnel"`
			}
			if json.Unmarshal(raw, &fields) == nil && fields.Tunnel.MetricsPort != nil && *fields.Tunnel.MetricsPort == 0 {
				reason = "Tunnel metricsPort 必须在 1～65535 之间"
			}
			if counts[app.ID] > 1 {
				reason = "应用 ID 重复，不能确定唯一导入归属"
			}
		}
		if reason != "" {
			ref, name := fmt.Sprintf("skipped:app:%d", i+1), fmt.Sprintf("应用 #%d", i+1)
			if serviceSwitchID.MatchString(app.ID) {
				ref, name = "app:"+app.ID, app.ID
			}
			if !skippedRefs[ref] {
				result.Skipped = append(result.Skipped, ServiceSwitchSkip{Ref: ref, Kind: "app", Name: name, Reason: reason})
				skippedRefs[ref] = true
			}
			continue
		}
		if app.Name == "" {
			app.Name = app.ID
		}
		ports := []int{}
		seenPorts := map[int]bool{}
		for _, port := range app.Ports {
			if !seenPorts[port] {
				ports = append(ports, port)
				seenPorts[port] = true
			}
		}
		app.Ports = ports
		if app.Ports == nil {
			app.Ports = []int{}
		}
		result.Apps = append(result.Apps, app)
	}
	slices.SortFunc(result.Apps, func(a, b ServiceSwitchApp) int { return strings.Compare(a.ID, b.ID) })
	if len(source.Database) > 0 && string(source.Database) != "null" {
		var database ServiceSwitchDatabase
		reason := ""
		if json.Unmarshal(source.Database, &database) != nil {
			reason = "数据库字段类型不合法"
		} else if database.Kind != "postgres" {
			reason = "不支持的数据库类型，请手动添加目标"
		} else if _, err := ExecutableDirectory(database.Ctl); err != nil {
			reason = "数据库 ctl 必须是完整的 Windows 程序绝对路径"
		}
		if reason != "" {
			result.Skipped = append(result.Skipped, ServiceSwitchSkip{Ref: "database", Kind: "database", Name: "数据库", Reason: reason})
		} else {
			result.Database = &database
		}
	}
	if len(source.Scripts) > 0 && string(source.Scripts) != "null" && string(source.Scripts) != "[]" && string(source.Scripts) != "{}" {
		result.Skipped = append(result.Skipped, ServiceSwitchSkip{Ref: "scripts", Kind: "scripts", Name: "脚本", Reason: "脚本不是常驻服务，不导入脚本内容"})
	}
	if _, _, err := ServiceSwitchFingerprint(result); err != nil {
		return ServiceSwitchSummary{}, err
	}
	return result, nil
}

func validateServiceSwitchApp(app ServiceSwitchApp) string {
	if !serviceSwitchID.MatchString(app.ID) {
		return "应用 ID 必须为 1～40 位小写字母、数字或横线，首位不能为横线"
	}
	for _, value := range []string{app.Name, app.Root, app.HealthPath, app.Domain, app.Controller, app.Guardian.StatusURL, app.Autostart} {
		if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return "应用字段不能包含控制字符"
		}
	}
	if app.Root != "" {
		if _, err := CleanWindowsDirectory(app.Root); err != nil {
			return "应用 root 必须是 Windows 绝对目录"
		}
	}
	for _, port := range app.Ports {
		if port < 1 || port > 65535 {
			return "应用端口必须在 1～65535 之间"
		}
	}
	if app.Tunnel.MetricsPort < 0 || app.Tunnel.MetricsPort > 65535 {
		return "Tunnel metricsPort 必须在 1～65535 之间"
	}
	if app.HealthPath != "none" {
		parsed, err := url.ParseRequestURI(app.HealthPath)
		if err != nil || !strings.HasPrefix(app.HealthPath, "/") || strings.HasPrefix(app.HealthPath, "//") || parsed.Host != "" || parsed.Fragment != "" {
			return "healthPath 必须是以 / 开头的路径或 none"
		}
	}
	if app.Guardian.StatusURL != "" && !serviceSwitchHTTP(app.Guardian.StatusURL) {
		return "guardian.statusUrl 必须是无凭据的 http 或 https 地址"
	}
	if app.Domain != "" {
		u, err := url.Parse("https://" + app.Domain)
		if err != nil || !serviceSwitchHTTP("https://"+app.Domain) || u.Path != "" || u.RawQuery != "" || u.Host != app.Domain {
			return "domain 必须是域名及可选端口，不能包含路径、凭据或查询参数"
		}
	}
	return ""
}

func serviceSwitchHTTP(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func DecodeServiceSwitchSummary(data []byte) (ServiceSwitchSummary, error) {
	var summary ServiceSwitchSummary
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > MaxServiceSwitchSummary || decoder.Decode(&summary) != nil || decoder.Decode(new(any)) != io.EOF {
		return summary, fmt.Errorf("采集器返回了无效的服务开关精简结果")
	}
	seen := map[string]bool{}
	for _, app := range summary.Apps {
		if validateServiceSwitchApp(app) != "" || seen[app.ID] {
			return summary, fmt.Errorf("采集器返回了无效或重复的应用")
		}
		seen[app.ID] = true
	}
	if summary.Database != nil {
		if _, err := ExecutableDirectory(summary.Database.Ctl); err != nil || summary.Database.Kind != "postgres" {
			return summary, fmt.Errorf("采集器返回了无效的数据库")
		}
	}
	return summary, nil
}
