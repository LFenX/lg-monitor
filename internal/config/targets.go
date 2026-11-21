// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func (t Target) AppliesTo(id, hostname string) bool {
	return t.Collector == "" || t.Collector == "*" || t.Collector == id || (hostname != "" && strings.EqualFold(t.Collector, hostname))
}

func TargetConfig(cfg *Config, id, hostname string) (string, []byte, error) {
	if cfg == nil {
		return "", nil, fmt.Errorf("目标配置不能为空")
	}
	selected := TargetsFile{Targets: make([]Target, 0, len(cfg.Targets))}
	for _, target := range cfg.Targets {
		// Batch jobs are judged on the monitor from their events; a collector only reads the file a job writes them to.
		if target.Job != nil && (target.Job.Source.Kind != "file" || target.Collector != id && !strings.EqualFold(target.Collector, hostname)) {
			continue
		}
		if target.Job != nil {
			selected.Targets = append(selected.Targets, Target{ID: target.ID, Collector: target.Collector, Job: &Job{Source: target.Job.Source}})
			continue
		}
		if target.AppliesTo(id, hostname) {
			// Monitor-side fields stay home: an older collector rejects unknown fields, and a
			// parent may not even be assigned to the same collector.
			target.Source, target.Parent, target.Task = nil, "", ""
			target.Role, target.Description, target.Depends = "", "", nil
			selected.Targets = append(selected.Targets, target)
		}
	}
	data, err := json.Marshal(selected)
	if err != nil {
		return "", nil, fmt.Errorf("编码采集配置失败：%w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}

func ParseTargetConfig(version string, data []byte) (*Config, error) {
	sum := sha256.Sum256(data)
	if version != hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("采集配置版本与内容不一致")
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("采集配置必须是 JSON 对象")
	}
	var document TargetsFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		if strings.Contains(err.Error(), `unknown field "run"`) {
			return nil, fmt.Errorf("旧 run 字段已停用，请改用 script 文件名和 args 参数：%w", err)
		}
		return nil, fmt.Errorf("解析采集配置失败：%w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("采集配置只能包含一个 JSON 对象")
	}
	cfg := &Config{Targets: document.Targets}
	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("采集配置校验失败：%w", err)
	}
	return cfg, nil
}
