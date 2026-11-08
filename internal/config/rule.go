package config

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

func (r Rule) NeedsTarget() bool {
	return r.Type != "collector_offline" && r.Type != "sql" && !(r.Type == "metric_threshold" && !strings.HasPrefix(r.Metric, "process.") && !strings.HasPrefix(r.Metric, "script."))
}

func (r Rule) ValidateCondition(target Target) error {
	if r.Renotify != "" {
		if delay, err := r.Renotify.Parse(); err != nil || delay <= 0 {
			return fmt.Errorf("renotify 必须为正数合法时长")
		}
	}
	if r.Type != "metric_threshold" && (r.Metric != "" || r.Op != "" || r.Threshold != nil || r.Resource != "" || (r.Probe != nil && r.Type != "cert_expiry")) {
		return fmt.Errorf("metric/op/threshold/resource/probe 只用于 metric_threshold")
	}
	if r.Type != "cert_expiry" && r.Within != "" {
		return fmt.Errorf("within 只用于 cert_expiry")
	}
	if r.Type != "sql" && (r.Query != "" || r.Interval != "") {
		return fmt.Errorf("query/interval 只用于 sql 规则")
	}
	switch r.Type {
	case "cert_expiry":
		if r.For != "" {
			return fmt.Errorf("cert_expiry 不接受 for，请删除该字段")
		}
		if _, err := r.CertWithin(); err != nil {
			return err
		}
		if len(r.CertificateProbes(target)) == 0 {
			return fmt.Errorf("cert_expiry 需要目标的 HTTPS 探测，probe 须指向 HTTPS 探测（下标从 0 开始）")
		}
	case "service_stopped":
		if target.Match.Service == "" {
			return fmt.Errorf("service_stopped 需要目标配置 match.service")
		}
	case "script_failed":
		if !hasScript(target) {
			return fmt.Errorf("script_failed 需要目标配置 script 探测")
		}
	case "sql":
		if err := ValidateRuleSQL(r.Query); err != nil {
			return err
		}
		if _, err := r.SQLInterval(); err != nil {
			return err
		}
	case "metric_threshold":
		if r.Threshold == nil || math.IsNaN(*r.Threshold) || math.IsInf(*r.Threshold, 0) {
			return fmt.Errorf("metric_threshold 需要有限数字 threshold")
		}
		if !oneOf(r.Op, "gt", "gte", "lt", "lte", "eq", "ne") {
			return fmt.Errorf("op 必须为 gt/gte/lt/lte/eq/ne")
		}
		if !oneOf(r.Metric, "system.cpu_percent", "system.memory_percent", "system.uptime_seconds", "disk.used_percent", "disk.free_bytes", "network.send_bytes_per_sec", "network.receive_bytes_per_sec", "process.cpu_percent", "process.rss_bytes") && !(strings.HasPrefix(r.Metric, "script.") && len(r.Metric) > len("script.") && metricPattern.MatchString(strings.TrimPrefix(r.Metric, "script."))) {
			return fmt.Errorf("metric 需要有效的 system/disk/network/process 指标或 script.指标名")
		}
		if strings.HasPrefix(r.Metric, "script.") {
			if !hasScript(target) {
				return fmt.Errorf("脚本指标需要目标配置 script 探测")
			}
			if r.Probe != nil && (*r.Probe < 0 || *r.Probe >= len(target.Probes) || target.Probes[*r.Probe].Type != "script") {
				return fmt.Errorf("probe 必须指向目标的 script 探测（下标从 0 开始）")
			}
		} else if r.Probe != nil {
			return fmt.Errorf("probe 只用于 script 指标")
		}
		if r.Resource != "" && !strings.HasPrefix(r.Metric, "disk.") && !strings.HasPrefix(r.Metric, "network.") {
			return fmt.Errorf("resource 只用于磁盘或网卡指标")
		}
	}
	return nil
}

func (r Rule) CertWithin() (time.Duration, error) {
	value := r.Within
	if value == "" {
		value = "14d"
	}
	within, err := value.Parse()
	if err != nil || within < time.Hour || within > 365*24*time.Hour {
		return 0, fmt.Errorf("cert_expiry 的 within 必须在 1h～365d 之间，默认 14d")
	}
	return within, nil
}

func (r Rule) CertificateProbes(target Target) []int {
	var indices []int
	for index, probe := range target.Probes {
		address, err := url.Parse(probe.URL)
		if probe.Type == "http" && err == nil && address.Scheme == "https" && address.Host != "" &&
			(r.Probe == nil || *r.Probe == index) {
			indices = append(indices, index)
		}
	}
	return indices
}

func (r Rule) SQLInterval() (time.Duration, error) {
	value := r.Interval
	if value == "" {
		value = "1m"
	}
	interval, err := value.Parse()
	if err != nil || interval < 10*time.Second {
		return 0, fmt.Errorf("SQL interval 默认 1m，不能小于 10s")
	}
	return interval, nil
}

func hasScript(target Target) bool {
	for _, probe := range target.Probes {
		if probe.Type == "script" {
			return true
		}
	}
	return false
}
