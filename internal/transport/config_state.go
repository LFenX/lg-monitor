package transport

import (
	"fmt"
	"slices"

	"github.com/LFenX/lg-monitor/internal/config"
)

const MatchDirectoryCapability = "match_dir"

type CollectorConfigState struct {
	State   string `json:"state"`
	Version string `json:"version"`
	Message string `json:"message"`
}

func AwaitingCollectorConfig() CollectorConfigState {
	return CollectorConfigState{State: "pending", Message: "等待采集器连接并确认配置"}
}

func (r *Receiver) ConfigState(id string) CollectorConfigState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state, known := r.configStates[id]; known {
		return state
	}
	return AwaitingCollectorConfig()
}

func (r *Receiver) setConfigState(session *linkSession, state CollectorConfigState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// A cancelled older connection cannot overwrite a reconnect's confirmation.
	if r.active[session.identity.CollectorID] != session {
		return
	}
	r.configStates[session.identity.CollectorID] = state
}

func missingConfigCapability(cfg *config.Config, identity Hello) string {
	if slices.Contains(identity.Capabilities, MatchDirectoryCapability) {
		return ""
	}
	for _, target := range cfg.Targets {
		if !target.AppliesTo(identity.CollectorID, identity.Hostname) {
			continue
		}
		for _, field := range []struct{ name, value string }{{"exeDir", target.Match.ExeDir}, {"workDir", target.Match.WorkDir}} {
			if field.value != "" {
				return fmt.Sprintf("目标 %s 使用 match.%s，采集器版本过旧，请升级以支持 %s", target.ID, field.name, MatchDirectoryCapability)
			}
		}
	}
	return ""
}
