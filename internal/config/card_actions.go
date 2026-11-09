package config

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

func cardActionGrants(file NotifiersFile) map[string]bool {
	apps := map[string]FeishuApp{}
	for _, app := range file.FeishuApps {
		apps[app.ID] = app
	}
	grants := map[string]bool{}
	for _, channel := range file.Notifiers {
		if !channel.CardActions || channel.Type != "feishu_app" {
			continue
		}
		app := apps[channel.App]
		data, _ := json.Marshal([]string{channel.ID, channel.App, app.AppID, app.Domain, channel.ChatID})
		grants[string(data)] = true
	}
	return grants
}

// CardActionGrantsAdded detects delegation changes independently of YAML formatting.
func CardActionGrantsAdded(before, after NotifiersFile) bool {
	previous := cardActionGrants(before)
	for key := range cardActionGrants(after) {
		if !previous[key] {
			return true
		}
	}
	return false
}

func CardActionsConfig(data []byte, id string, enabled bool) ([]byte, error) {
	file, err := ParseNotifiers(data)
	if err != nil {
		return nil, err
	}
	found := false
	for _, channel := range file.Notifiers {
		if channel.ID == id {
			found = true
			if channel.Type != "feishu_app" || channel.OpenID != "" {
				return nil, fmt.Errorf("卡片动作只能用于飞书应用群渠道")
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("通知渠道不存在，请刷新后重试")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析动作配置失败：%w", err)
	}
	sequence := nodeAt(&doc, "notifiers")
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("通知渠道列表格式无效")
	}
	for _, entry := range sequence.Content {
		var channel Notifier
		if err := entry.Decode(&channel); err != nil {
			return nil, fmt.Errorf("读取动作渠道失败：%w", err)
		}
		if channel.ID != id {
			continue
		}
		if entry.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("通知渠道必须为对象")
		}
		// A merge inheriting this entry must retain its prior delegation, not gain the new one.
		detachCardActionAliases(&doc, entry)
		value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(enabled)}
		replaced := false
		for i := 0; i+1 < len(entry.Content); i += 2 {
			if entry.Content[i].Value == "cardActions" {
				old := entry.Content[i+1]
				detachCardActionAliases(&doc, old)
				value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
				entry.Content[i+1] = value
				replaced = true
				break
			}
		}
		if !replaced {
			entry.Content = append(entry.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "cardActions"}, value)
		}
		break
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, fmt.Errorf("编码卡片动作配置失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("完成卡片动作配置失败：%w", err)
	}
	return out.Bytes(), nil
}

func copyActionYAMLNode(source *yaml.Node) *yaml.Node {
	copy := *source
	copy.Anchor = ""
	copy.Content = make([]*yaml.Node, len(source.Content))
	for i, child := range source.Content {
		copy.Content[i] = copyActionYAMLNode(child)
	}
	return &copy
}

func detachCardActionAliases(node, target *yaml.Node) {
	if node.Kind == yaml.AliasNode && node.Alias == target {
		saved := copyActionYAMLNode(target)
		*node = *saved
		return
	}
	for _, child := range node.Content {
		detachCardActionAliases(child, target)
	}
}
