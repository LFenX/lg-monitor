package config

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

func WebhookConfig(data []byte, id, kind, urlRef string, secretRef *string) ([]byte, error) {
	if !idPattern.MatchString(id) || !oneOf(kind, "feishu_webhook", "webhook") || urlRef == "" {
		return nil, fmt.Errorf("Webhook 渠道编号、类型或地址引用无效")
	}
	cfg, err := ParseNotifiers(data)
	if err != nil {
		return nil, err
	}
	current := Notifier{ID: id, Type: kind}
	for _, item := range cfg.Notifiers {
		if item.ID == id {
			if item.Type != kind {
				return nil, fmt.Errorf("通知渠道类型不一致，请刷新配置后再保存凭据")
			}
			current = item
			break
		}
	}
	current.URLRef = urlRef
	if secretRef != nil {
		current.SecretRef = *secretRef
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析通知配置失败：%w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("notifiers.yaml 根节点必须是对象")
	}
	var sequence *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "notifiers" {
			sequence = root.Content[i+1]
			break
		}
	}
	if sequence == nil {
		sequence = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "notifiers"}, sequence)
	}
	if sequence.Kind == yaml.AliasNode {
		sequence = sequence.Alias
	}
	if sequence.Kind == yaml.ScalarNode && sequence.Tag == "!!null" {
		sequence.Kind, sequence.Tag, sequence.Value = yaml.SequenceNode, "!!seq", ""
	}
	if sequence.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("notifiers.yaml 的 notifiers 必须是列表")
	}
	var encoded yaml.Node
	if err := encoded.Encode(current); err != nil {
		return nil, fmt.Errorf("编码 Webhook 渠道配置失败：%w", err)
	}
	replaced := false
	for i, node := range sequence.Content {
		var value Notifier
		if err := node.Decode(&value); err != nil {
			return nil, fmt.Errorf("读取已有通知渠道失败：%w", err)
		}
		if value.ID != id {
			continue
		}
		if node.Kind == yaml.AliasNode {
			node = node.Alias
		}
		encoded.HeadComment, encoded.LineComment, encoded.FootComment = node.HeadComment, node.LineComment, node.FootComment
		for j := 0; j+1 < len(encoded.Content); j += 2 {
			for k := 0; k+1 < len(node.Content); k += 2 {
				if encoded.Content[j].Value == node.Content[k].Value {
					encoded.Content[j].HeadComment, encoded.Content[j].LineComment = node.Content[k].HeadComment, node.Content[k].LineComment
					encoded.Content[j+1].HeadComment, encoded.Content[j+1].LineComment, encoded.Content[j+1].FootComment = node.Content[k+1].HeadComment, node.Content[k+1].LineComment, node.Content[k+1].FootComment
				}
			}
		}
		sequence.Content[i], replaced = &encoded, true
		break
	}
	if !replaced {
		sequence.Content = append(sequence.Content, &encoded)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, fmt.Errorf("编码通知配置失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("完成通知配置编码失败：%w", err)
	}
	return out.Bytes(), nil
}
