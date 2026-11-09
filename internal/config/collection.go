package config

import (
	"fmt"
	"time"
)

type CollectionSettings struct {
	TargetInterval  Duration `yaml:"targetInterval,omitempty" json:"targetInterval,omitempty" jsonschema_description:"本机目标采集周期，默认 2s，500ms～5s，整毫秒；重启采集器生效。"`
	ProcessInterval Duration `yaml:"processInterval,omitempty" json:"processInterval,omitempty" jsonschema_description:"完整进程查询缓存的独立刷新周期，默认 15s，1s～1m，整毫秒；目标/ETW 新事实也更新缓存，重启生效。"`
	SystemInterval  Duration `yaml:"systemInterval,omitempty" json:"systemInterval,omitempty" jsonschema_description:"系统指标周期，默认 5s，1s～1m，整毫秒；重启采集器生效。"`
	ServiceInterval Duration `yaml:"serviceInterval,omitempty" json:"serviceInterval,omitempty" jsonschema_description:"Windows 服务全表周期，默认 10s，1s～1m，整毫秒；重启采集器生效。"`
}

type CollectionIntervals struct{ Target, Process, System, Service time.Duration }

func (s CollectionSettings) intervals() (CollectionIntervals, []settingIssue) {
	result := CollectionIntervals{Target: 2 * time.Second, Process: 15 * time.Second, System: 5 * time.Second, Service: 10 * time.Second}
	var issues []settingIssue
	for _, field := range []struct {
		name             string
		value            Duration
		out              *time.Duration
		minimum, maximum time.Duration
	}{
		{"targetInterval", s.TargetInterval, &result.Target, 500 * time.Millisecond, 5 * time.Second},
		{"processInterval", s.ProcessInterval, &result.Process, time.Second, time.Minute},
		{"systemInterval", s.SystemInterval, &result.System, time.Second, time.Minute},
		{"serviceInterval", s.ServiceInterval, &result.Service, time.Second, time.Minute},
	} {
		if field.value == "" {
			continue
		}
		value, err := field.value.Parse()
		if err != nil || value < field.minimum || value > field.maximum || value%time.Millisecond != 0 {
			issues = append(issues, settingIssue{field.name, fmt.Sprintf("collection.%s 必须为 %s～%s 的整毫秒时长", field.name, field.minimum, field.maximum)})
			continue
		}
		*field.out = value
	}
	return result, issues
}

func (s CollectionSettings) Effective() (CollectionIntervals, error) {
	result, issues := s.intervals()
	if len(issues) > 0 {
		return CollectionIntervals{}, fmt.Errorf("%s", issues[0].message)
	}
	return result, nil
}
