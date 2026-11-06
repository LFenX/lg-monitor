package cards

import (
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestTestCardAlwaysLabelsItself(t *testing.T) {
	r, _, _ := testRenderer(t, config.Template{ID: "literal", Raw: card([]any{markdown("固定内容")})})
	for _, id := range []string{"standard", "compact", "detailed", "digest", "literal"} {
		result := r.RenderTest(id, "test-fake-nonce")
		value := decode(t, result)
		if !strings.Contains(allText(value), "这是测试消息，不代表真实监控故障") {
			t.Fatalf("%s hid the test label", id)
		}
	}
}
