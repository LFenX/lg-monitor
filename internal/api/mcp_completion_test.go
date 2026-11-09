package api

import "testing"

func TestMCPTeamCoverageKeepsExplicitHumanOnlyBoundaries(t *testing.T) {
	byOperation := map[Operation]string{}
	for _, binding := range mcpBindings() {
		if binding.operation != "" {
			byOperation[binding.operation] = binding.name
		}
	}
	for _, route := range TeamRoutes() {
		if byOperation[route.ID] == "" {
			t.Errorf("PRD G5 team capability has no MCP entry: %s", route.ID)
		}
	}
	for _, forbidden := range []Operation{GetNotificationPause, PutNotificationPause, DeleteNotificationPause, PutFeishuApp, PutNotifierCredentials} {
		if name := byOperation[forbidden]; name != "" {
			t.Errorf("explicit human-only boundary exposed through %s", name)
		}
	}
	for _, binding := range mcpBindings() {
		if route := mcpRoute(binding.operation); route.ID != "" && (route.ID == Stream || route.ID == OpenAPI) {
			t.Errorf("streaming HTTP resource was misrepresented as finite tool %s", binding.name)
		}
	}
}
