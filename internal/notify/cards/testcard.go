package cards

import (
	"encoding/json"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func (r *Renderer) RenderTest(id, testID string) Result {
	in := Sample(rules.StateFiring)
	in.DashboardURL = ""
	in.Alert.ID, in.Alert.StartedAt = testID, r.now().UnixMilli()
	in.Alert.Summary = "这是测试消息：飞书告警渠道连通性测试"
	in.Rule.Name = "飞书接入测试"
	in.Detail = "这是测试消息，不代表真实监控故障。"
	result := r.Render(id, in)
	var value map[string]any
	_ = json.Unmarshal(result.JSON, &value)
	appendElement(value, markdown("这是测试消息，不代表真实监控故障。"))
	data, _ := json.Marshal(value)
	if len(data) > MaxBytes {
		result = r.Render("standard", in)
		result.Fallback, result.Truncated = true, true
		result.Warning = "测试卡片超过大小限制，已使用标准模板"
		return result
	}
	result.JSON = data
	return result
}
