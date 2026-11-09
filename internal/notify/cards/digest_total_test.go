package cards

import (
	"strings"
	"testing"
)

func TestBoundedRealDigestUsesExactTotalForRemainingRows(t *testing.T) {
	r, _, _ := testRenderer(t)
	input := Sample("firing")
	input.AlertsTotal = 43
	for range 20 {
		input.Alerts = append(input.Alerts, Sample("firing"))
	}
	result := r.Render("digest", input)
	if result.Template != "digest" || !result.Truncated || !strings.Contains(allText(decode(t, result)), "还有 23 条") {
		t.Fatal("real history total lost", result)
	}
}
