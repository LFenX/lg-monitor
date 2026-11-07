package config

import (
	"bytes"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

func AddRuleNotifier(data []byte, ids []string, notifier string) ([]byte, error) {
	var cfg RulesFile
	var issues Errors
	readDocument(documentOverlay{name: "rules.yaml", data: data}, "rules.yaml", &cfg, map[string]*yaml.Node{}, &issues)
	if len(issues) > 0 {
		return nil, issues
	}
	for _, id := range ids {
		if !slices.ContainsFunc(cfg.Rules, func(rule Rule) bool { return rule.ID == id }) {
			return nil, fmt.Errorf("规则 %s 已不存在，请重新选择", id)
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析规则配置失败：%w", err)
	}
	var sequence *yaml.Node
	if len(doc.Content) > 0 {
		root := doc.Content[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "rules" {
				sequence = root.Content[i+1]
				break
			}
		}
	}
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("rules 必须是规则列表")
	}
	// Materialize shared notify lists before changing their anchor so other rules retain their recipients.
	for index, rule := range sequence.Content {
		if rule.Kind == yaml.AliasNode {
			copy := *rule.Alias
			copy.Anchor = ""
			copy.Content = append([]*yaml.Node(nil), copy.Content...)
			copy.HeadComment, copy.LineComment, copy.FootComment = rule.HeadComment, rule.LineComment, rule.FootComment
			rule, sequence.Content[index] = &copy, &copy
		}
		for i := 0; i+1 < len(rule.Content); i += 2 {
			if rule.Content[i].Value == "notify" {
				old := rule.Content[i+1]
				source := old
				if old.Kind == yaml.AliasNode {
					source = old.Alias
				}
				copy := *source
				copy.Anchor = ""
				copy.Content = append([]*yaml.Node(nil), copy.Content...)
				copy.HeadComment, copy.LineComment, copy.FootComment = old.HeadComment, old.LineComment, old.FootComment
				rule.Content[i+1] = &copy
			}
		}
	}
	for index, rule := range cfg.Rules {
		if !slices.Contains(ids, rule.ID) || slices.Contains(rule.Notify, notifier) {
			continue
		}
		node := sequence.Content[index]
		var notify *yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "notify" {
				notify = node.Content[i+1]
				break
			}
		}
		if notify == nil {
			notify = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "notify"}, notify)
		}
		if notify.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("规则 %s 的 notify 必须是列表", rule.ID)
		}
		notify.Content = append(notify.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: notifier})
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, fmt.Errorf("编码规则通知配置失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("完成规则通知配置编码失败：%w", err)
	}
	return out.Bytes(), nil
}
