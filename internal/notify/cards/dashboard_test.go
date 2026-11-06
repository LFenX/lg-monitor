package cards

import (
	"strings"
	"testing"
)

func TestDashboardURLFollowsSettingsInNormalTestAndFallbackCards(t *testing.T) {
	renderer, source, _ := testRenderer(t)
	for _, url := range []string{"http://first.example.test", "http://updated.example.test"} {
		source.cfg.Settings.PublicBaseURL = url
		input := Sample("firing")
		input.DashboardURL = ""
		for _, result := range []Result{renderer.Render("standard", input), renderer.RenderTest("standard", "fake-test"), renderer.StandardFallback("missing", input)} {
			if !strings.Contains(string(result.JSON), url) {
				t.Fatalf("card omitted current dashboard URL %s: %s", url, result.JSON)
			}
		}
	}
	definition, err := renderer.Definition("standard")
	if err != nil {
		t.Fatal(err)
	}
	definition.Colors["critical"] = "fake-color"
	unchanged, err := renderer.Definition("standard")
	if err != nil || unchanged.Colors["critical"] == "fake-color" {
		t.Fatal("template API mutated shared preset state")
	}
}
