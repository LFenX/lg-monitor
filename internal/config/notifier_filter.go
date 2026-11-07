package config

import (
	"slices"
	"strings"
)

func (f *NotifierFilter) Match(severity, collector, hostname string, tags []string) bool {
	if f == nil {
		return true
	}
	if f.Severity != nil && !slices.Contains(f.Severity, severity) {
		return false
	}
	if f.Tags != nil && !slices.ContainsFunc(f.Tags, func(tag string) bool { return slices.Contains(tags, tag) }) {
		return false
	}
	if f.Collectors != nil && !slices.ContainsFunc(f.Collectors, func(value string) bool {
		return value == collector || hostname != "" && strings.EqualFold(value, hostname)
	}) {
		return false
	}
	return true
}

func validateNotifierFilter(f *NotifierFilter, loc location, issues *Errors) {
	if node := nodeAt(loc.node); node != nil && node.Tag == "!!null" {
		loc.add(issues, "filter 必须是对象，不限制时请省略 filter")
		return
	}
	if f == nil {
		return
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"severity", f.Severity}, {"tags", f.Tags}, {"collectors", f.Collectors}} {
		if node := nodeAt(loc.node, field.name); node != nil && node.Tag == "!!null" {
			loc.at(field.name).add(issues, "filter.%s 必须是列表，不限制就不要写这一项", field.name)
		}
		if field.values != nil && len(field.values) == 0 {
			loc.at(field.name).add(issues, "filter.%s 不能是空列表，不限制就不要写这一项", field.name)
		}
		seen := map[string]bool{}
		for i, value := range field.values {
			if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
				loc.at(field.name, i).add(issues, "筛选项不能为空白或含首尾空白")
			}
			if seen[value] {
				loc.at(field.name, i).add(issues, "筛选项不能重复")
			}
			seen[value] = true
			if field.name == "severity" && !oneOf(value, "critical", "warning", "info") {
				loc.at(field.name, i).add(issues, "严重程度必须为 critical、warning 或 info")
			}
			if field.name == "collectors" && (value == "*" || len(value) > 255 || !collectorPattern.MatchString(value)) {
				loc.at(field.name, i).add(issues, "采集器筛选须为 ID 或主机名，不能使用 *")
			}
		}
	}
}
