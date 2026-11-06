package cards

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

var templateID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (r *Renderer) Scaffold(id, from string) ([]byte, error) {
	if !templateID.MatchString(id) || len(id) > 100 {
		return nil, fmt.Errorf("模板 ID 须为 1～100 个小写字母、数字或连字符，并以字母或数字开头")
	}
	if _, exists := r.presets[id]; exists {
		return nil, fmt.Errorf("模板 ID %s 是内置预设，请换一个名称", id)
	}
	spec, ok := r.presets[from]
	if !ok {
		return nil, fmt.Errorf("--from 须为 standard、compact、detailed 或 digest")
	}
	spec.ID, spec.Name = id, id
	data, err := yaml.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("生成模板配置失败：%w", err)
	}
	prefix := "# Change title, colors and fields to customize the selected preset.\n# Variables and raw examples: docs/card-templates.md.\n# mention is opt-in; no one is mentioned by default.\n"
	return append([]byte(prefix), data...), nil
}
