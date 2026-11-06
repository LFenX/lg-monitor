package config

import (
	"fmt"
	"io/fs"
	"maps"

	"gopkg.in/yaml.v3"
)

func LoadRuleFile(fsys fs.FS, name string, base *Config) (Rule, error) {
	if base == nil {
		return Rule{}, fmt.Errorf("草稿规则需要当前配置以检查引用")
	}
	if _, err := fs.Stat(fsys, name); err != nil {
		return Rule{}, fmt.Errorf("读取规则草稿 %s 失败：%w", name, err)
	}
	var rule Rule
	var issues Errors
	sources := map[string]*yaml.Node{}
	readDocument(fsys, name, &rule, sources, &issues)
	if len(issues) > 0 {
		return Rule{}, issues
	}
	copy := base.clone()
	copy.Rules = []Rule{rule}
	copy.sources = maps.Clone(base.sources)
	if copy.sources == nil {
		copy.sources = map[string]*yaml.Node{}
	}
	if document := sources[name]; document != nil && len(document.Content) > 0 {
		copy.sources["rules.yaml"] = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "rules"}, {Kind: yaml.SequenceNode, Content: []*yaml.Node{document.Content[0]}}}}}}
	}
	if err := Validate(copy); err != nil {
		if errors, ok := err.(Errors); ok {
			for i := range errors {
				if errors[i].File == "rules.yaml" {
					errors[i].File = name
				}
			}
		}
		return Rule{}, err
	}
	return rule, nil
}
