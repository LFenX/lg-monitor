package config

import "slices"

const DefaultOfflineRuleID = "collector-offline"

func OfflineRules(cfg *Config) []Rule {
	rules := make([]Rule, 0)
	for _, rule := range cfg.Rules {
		if rule.Type == "collector_offline" {
			if rule.For == "" {
				rule.For = "30s"
			}
			rule.Notify = slices.Clone(rule.Notify)
			rules = append(rules, rule)
		}
	}
	if len(rules) != 0 {
		return rules
	}
	rule := Rule{ID: DefaultOfflineRuleID, Name: "采集器失联", Type: "collector_offline", For: "30s", Severity: "warning"}
	for _, notifier := range cfg.Notifiers {
		rule.Notify = append(rule.Notify, notifier.ID)
	}
	return []Rule{rule}
}
