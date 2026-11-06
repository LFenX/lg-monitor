package config

import (
	"fmt"
	"time"
)

type Settings struct {
	Retention Retention `yaml:"retention,omitempty" json:"retention,omitempty"`
}

type Retention struct {
	Raw    Duration `yaml:"raw,omitempty" json:"raw,omitempty" jsonschema_description:"原始数据保留时长，默认 24h。"`
	Minute Duration `yaml:"minute,omitempty" json:"minute,omitempty" jsonschema_description:"分钟汇总保留时长，默认 336h（14 天）。"`
	Hour   Duration `yaml:"hour,omitempty" json:"hour,omitempty" jsonschema_description:"小时汇总保留时长，默认 8760h（365 天）。"`
}

type RetentionDurations struct{ Raw, Minute, Hour time.Duration }

func (r Retention) Effective() (RetentionDurations, error) {
	result := RetentionDurations{Raw: 24 * time.Hour, Minute: 14 * 24 * time.Hour, Hour: 365 * 24 * time.Hour}
	for _, field := range []struct {
		name  string
		value Duration
		out   *time.Duration
	}{{"raw", r.Raw, &result.Raw}, {"minute", r.Minute, &result.Minute}, {"hour", r.Hour, &result.Hour}} {
		if field.value == "" {
			continue
		}
		value, err := field.value.Parse()
		if err != nil || value < time.Millisecond {
			return result, fmt.Errorf("retention.%s 必须为至少 1ms 的正数时长，例如 24h 或 336h", field.name)
		}
		*field.out = value
	}
	if result.Raw < 24*time.Hour {
		return result, fmt.Errorf("原始数据的保留时长不能短于 24h，以覆盖离线缓存和规则回放")
	}
	if result.Minute < result.Raw {
		return result, fmt.Errorf("分钟级数据的保留时长（%s）不能短于原始数据（%s）", retentionText(r.Minute, "14d"), retentionText(r.Raw, "24h"))
	}
	if result.Hour < result.Minute {
		return result, fmt.Errorf("小时级数据的保留时长（%s）不能短于分钟级数据（%s）", retentionText(r.Hour, "365d"), retentionText(r.Minute, "14d"))
	}
	return result, nil
}

func retentionText(value Duration, fallback string) string {
	if value == "" {
		return fallback
	}
	return string(value)
}
