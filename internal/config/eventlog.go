package config

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type EventLogSubscription struct {
	Channel  string   `yaml:"channel" json:"channel" jsonschema:"enum=Application,enum=System"`
	Provider string   `yaml:"provider" json:"provider" jsonschema:"minLength=1"`
	IDs      []int    `yaml:"ids,omitempty" json:"ids,omitempty" jsonschema:"uniqueItems=true"`
	Levels   []string `yaml:"levels,omitempty" json:"levels,omitempty" jsonschema:"uniqueItems=true"`
}

func (s EventLogSubscription) Validate() error {
	if s.Channel != "Application" && s.Channel != "System" {
		return fmt.Errorf("eventlog.channel 只允许 Application 或 System")
	}
	if strings.TrimSpace(s.Provider) == "" || strings.ContainsRune(s.Provider, 0) || !utf8.ValidString(s.Provider) {
		return fmt.Errorf("eventlog.provider 需要不含 NUL 的非空 UTF-8 来源名称")
	}
	ids := map[int]bool{}
	for _, id := range s.IDs {
		if id < 0 || id > 65535 || ids[id] {
			return fmt.Errorf("eventlog.ids 必须为不重复的 0～65535 整数，省略表示该来源全部 ID")
		}
		ids[id] = true
	}
	levels := map[string]bool{}
	for _, level := range s.Levels {
		if _, known := EventLogLevel(level); !known || levels[level] {
			return fmt.Errorf("eventlog.levels 只允许不重复的 always、critical、error、warning、info、verbose")
		}
		levels[level] = true
	}
	return nil
}

func EventLogLevel(name string) (uint8, bool) {
	for index, level := range []string{"always", "critical", "error", "warning", "info", "verbose"} {
		if name == level {
			return uint8(index), true
		}
	}
	return 0, false
}

func EventLogPreset(preset string) bool {
	return preset == "app_crash" || preset == "service_crash" || preset == "unexpected_shutdown"
}

func (t Target) HasMatch() bool {
	m := t.Match
	return strings.TrimSpace(m.Process) != "" || strings.TrimSpace(m.Cmdline) != "" || m.ExeDir != "" || m.WorkDir != "" || m.Port != nil || strings.TrimSpace(m.Service) != ""
}

func (r Rule) EventLogWindow() (time.Duration, error) {
	value := r.Window
	if value == "" {
		value = "10m"
	}
	window, err := value.Parse()
	if err != nil || window < time.Second || window > 24*time.Hour {
		return 0, fmt.Errorf("eventlog.window 必须在 1s～24h 之间，默认 10m")
	}
	return window, nil
}

func (r Rule) EventLogCount() int {
	if r.Count == nil {
		return 1
	}
	return *r.Count
}

func (r Rule) validateEventLog(target Target) error {
	if r.For != "" {
		return fmt.Errorf("eventlog 达到事件次数即触发，不接受 for")
	}
	if _, err := r.EventLogWindow(); err != nil {
		return err
	}
	if count := r.EventLogCount(); count < 1 || count > 8640000 {
		return fmt.Errorf("eventlog.count 必须在 1～8640000 之间，默认 1")
	}
	if r.Target == "" {
		if !EventLogPreset(r.Preset) {
			return fmt.Errorf("不指定 target 的 eventlog 规则必须指定有效 preset")
		}
		return nil
	}
	if r.Preset == "" {
		if target.EventLog == nil {
			return fmt.Errorf("未指定 preset 的 eventlog 规则需要目标配置 eventlog 订阅")
		}
		return nil
	}
	if !EventLogPreset(r.Preset) {
		return fmt.Errorf("eventlog.preset 只允许 app_crash、service_crash、unexpected_shutdown")
	}
	if r.Preset == "app_crash" && target.Match.Process != "" || r.Preset == "service_crash" && target.Match.Service != "" {
		return nil
	}
	return fmt.Errorf("带 target 和 preset 的 eventlog 规则需要对应的 match.process 或 match.service；unexpected_shutdown 只用于无 target 的全局规则")
}
