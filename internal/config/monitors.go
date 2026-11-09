package config

import (
	"fmt"
	"regexp"
	"strings"
)

const MaxAdditionalMonitors = 7

type CollectorMonitor struct {
	ID              string `yaml:"id" json:"id" jsonschema:"pattern=^[a-z][a-z0-9-]*$,minLength=1,maxLength=32"`
	Enabled         *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty" jsonschema_description:"省略为 true；重启采集器生效。"`
	AllowOperations bool   `yaml:"allowOperations,omitempty" json:"allowOperations,omitempty" jsonschema_description:"默认 false；仍须满足本机 remoteOperations 和受保护 LocalSystem 授权，重启生效。"`
}

func (m CollectorMonitor) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

var monitorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func ValidateMonitorID(id string) error {
	reserved := id == "con" || id == "prn" || id == "aux" || id == "nul" || (len(id) == 4 && (strings.HasPrefix(id, "com") || strings.HasPrefix(id, "lpt")) && id[3] >= '1' && id[3] <= '9')
	if id == "default" || reserved || !monitorIDPattern.MatchString(id) {
		return fmt.Errorf("监控器 ID 须为 1～32 位小写字母开头的字母、数字或横线，且不能为 default 或 Windows 设备名")
	}
	return nil
}

func ValidateCollectorMonitors(monitors []CollectorMonitor) error {
	if len(monitors) > MaxAdditionalMonitors {
		return fmt.Errorf("collectorMonitors 最多配置 %d 个附加监控器", MaxAdditionalMonitors)
	}
	seen := make(map[string]bool, len(monitors))
	for _, monitor := range monitors {
		if err := ValidateMonitorID(monitor.ID); err != nil {
			return err
		}
		if seen[monitor.ID] {
			return fmt.Errorf("collectorMonitors 的 ID %s 重复", monitor.ID)
		}
		seen[monitor.ID] = true
	}
	return nil
}
