package cards

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

type FrozenTemplate struct {
	renderer *Renderer
	spec     config.Template
}

func (r *Renderer) Freeze(id string) (*FrozenTemplate, error) {
	spec, err := r.definition(first(id, "standard"))
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("复制通知模板失败：%w", err)
	}
	var owned config.Template
	if err := json.Unmarshal(data, &owned); err != nil {
		return nil, fmt.Errorf("读取通知模板副本失败：%w", err)
	}
	return &FrozenTemplate{renderer: r, spec: owned}, nil
}

func (f *FrozenTemplate) UsesEvents() bool {
	return f.spec.Raw == nil && f.spec.Extends == "detailed" || config.TemplateUsesField(f.spec, "Events")
}

func (f *FrozenTemplate) RenderAt(input Input, at time.Time) Result {
	result, err := f.renderer.limited(f.spec, input, at, "")
	if err != nil {
		return f.renderer.standardFallback(f.spec.ID, input, at)
	}
	return result
}

func UpdateEnabled(card json.RawMessage) bool {
	var value struct {
		Config struct {
			UpdateMulti bool `json:"update_multi"`
		} `json:"config"`
	}
	return json.Unmarshal(card, &value) == nil && value.Config.UpdateMulti
}
