package cards

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type testSource struct{ cfg *config.Config }

func (s testSource) Snapshot() *config.Config { return s.cfg }

func (r *Renderer) RenderTest(id, testID string) (Result, error) {
	return r.RenderTestState(id, testID, rules.StateFiring, nil)
}

func (r *Renderer) RenderTestState(id, testID, state string, draft *config.Template) (Result, error) {
	if state == "" {
		state = rules.StateFiring
	}
	if state != rules.StateFiring && state != rules.StateResolved {
		return Result{}, fmt.Errorf("测试状态必须是 firing 或 resolved")
	}
	if draft != nil {
		if id != "" {
			return Result{}, fmt.Errorf("template 和 draft 只能选择一个")
		}
		if r.source == nil || r.source.Snapshot() == nil {
			return Result{}, fmt.Errorf("模板草稿需要当前配置")
		}
		data, err := json.MarshalIndent(draft, "", "  ")
		if err != nil {
			return Result{}, fmt.Errorf("编码模板草稿失败：%w", err)
		}
		spec, cfg, err := config.ParseTemplate(data, r.source.Snapshot())
		if err != nil {
			return Result{}, err
		}
		renderer, err := New(testSource{cfg}, Options{Now: r.now, Location: r.location, Logger: r.logger})
		if err != nil {
			return Result{}, err
		}
		return renderer.RenderTestState(spec.ID, testID, state, nil)
	}
	id = first(id, "standard")
	if err := r.Check(id); err != nil {
		return Result{}, err
	}
	spec, err := r.definition(id)
	if err != nil {
		return Result{}, err
	}
	in := Sample(state)
	in.DashboardURL = ""
	if r.source != nil && r.source.Snapshot() != nil {
		in.DashboardURL = r.source.Snapshot().Settings.PublicBaseURL
	}
	now := r.now()
	in.Alert.ID, in.Alert.StartedAt = testID, now.UnixMilli()
	if state == rules.StateResolved {
		in.Alert.StartedAt, in.Alert.ResolvedAt = now.Add(-5*time.Minute).UnixMilli(), now.UnixMilli()
	}
	in.Alert.Summary = "这是测试消息：飞书告警渠道连通性测试"
	in.Rule.Name = "飞书接入测试"
	in.Detail = "这是测试消息，不代表真实监控故障。"
	return r.limited(spec, in, r.now(), "这是测试消息，不代表真实监控故障。")
}
