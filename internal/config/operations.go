package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type RemoteOperations struct {
	Enabled  bool           `yaml:"enabled,omitempty" json:"enabled,omitempty" jsonschema_description:"本机管理员授权开关，默认 false；不能通过 HTTP/MCP 或目标下发修改。"`
	Services []ServiceGrant `yaml:"services,omitempty" json:"services,omitempty" jsonschema:"maxItems=128"`
}
type ServiceGrant struct {
	ID      string `yaml:"id" json:"id" jsonschema:"minLength=1,maxLength=64,pattern=^[a-z0-9-]+$"`
	Service string `yaml:"service" json:"service" jsonschema:"minLength=1,maxLength=256" jsonschema_description:"精确 Windows SCM 服务名，仅授权无参数 restart_service；不能用路径、通配符、显示名称或命令。"`
}

var operationIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func (r RemoteOperations) Validate() error {
	if len(r.Services) > 128 {
		return fmt.Errorf("remoteOperations.services 最多 128 项")
	}
	if r.Enabled && len(r.Services) == 0 {
		return fmt.Errorf("remoteOperations 启用前至少授权一项精确服务")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, grant := range r.Services {
		if !operationIDPattern.MatchString(grant.ID) || ids[grant.ID] {
			return fmt.Errorf("remoteOperations 的授权 id 必须为 1～64 个小写字母、数字或横线，且不能重复")
		}
		ids[grant.ID] = true
		name := strings.ToLower(grant.Service)
		if !utf8.ValidString(grant.Service) || utf8.RuneCountInString(grant.Service) > 256 || strings.TrimSpace(grant.Service) != grant.Service || grant.Service == "" || strings.ContainsAny(grant.Service, "/\\*?\"'\r\n\x00") || strings.ContainsFunc(grant.Service, unicode.IsControl) || name == "lgmon" || names[name] {
			return fmt.Errorf("remoteOperations.service 必须是唯一的精确 SCM 服务名，不能含路径、通配符、控制字符或 lgmon")
		}
		names[name] = true
	}
	return nil
}

func (r RemoteOperations) Version() string {
	data, _ := json.Marshal(r)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func RemoteOperationsConfig(data []byte, policy RemoteOperations) ([]byte, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("读取本机操作授权失败：%w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("settings 必须为对象")
	}
	var value yaml.Node
	if err := value.Encode(policy); err != nil {
		return nil, fmt.Errorf("编码本机操作授权失败：%w", err)
	}
	found := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "remoteOperations" {
			old := root.Content[i+1]
			value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
			root.Content[i+1] = &value
			found = true
			break
		}
	}
	if !found {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "remoteOperations"}, &value)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, fmt.Errorf("保存本机操作授权失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("关闭本机操作配置编码器失败：%w", err)
	}
	return out.Bytes(), nil
}
