package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceSwitchCollector struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
}

type ServiceSwitchImportItem struct {
	Ref       string  `json:"ref"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	Autostart string  `json:"autostart"`
	Action    string  `json:"action"`
	TargetID  string  `json:"targetId"`
	CoveredBy *string `json:"coveredBy" jsonschema:"nullable"`
	Reason    *string `json:"reason" jsonschema:"nullable"`
	Target    *Target `json:"target" jsonschema:"nullable"`
}

type ServiceSwitchPreview struct {
	Collector      ServiceSwitchCollector    `json:"collector"`
	SHA256         string                    `json:"sha256"`
	ReadAt         int64                     `json:"readAt"`
	TargetsVersion string                    `json:"targetsVersion"`
	Items          []ServiceSwitchImportItem `json:"items"`
}

type ServiceSwitchImportInput struct {
	SHA256         string   `json:"sha256" jsonschema:"minLength=64,maxLength=64,pattern=^[0-9a-f]{64}$"`
	TargetsVersion string   `json:"targetsVersion" jsonschema:"minLength=64,maxLength=64,pattern=^[0-9a-f]{64}$"`
	Refs           []string `json:"refs,omitempty"`
	RemoveMissing  bool     `json:"removeMissing,omitempty"`
	PublicProbes   *bool    `json:"publicProbes,omitempty"`
}

type ServiceSwitchImportResult struct {
	Created        []string `json:"created"`
	Updated        []string `json:"updated"`
	Removed        []string `json:"removed"`
	TargetsVersion string   `json:"targetsVersion"`
}

func TargetsFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ServiceSwitchPreviewTargets(host ServiceSwitchCollector, snapshot ServiceSwitchSnapshot, existing []Target, version string, public bool) ServiceSwitchPreview {
	result := ServiceSwitchPreview{Collector: host, SHA256: snapshot.SHA256, ReadAt: snapshot.ReadAt, TargetsVersion: version, Items: []ServiceSwitchImportItem{}}
	used, present := map[string]bool{}, map[string]bool{}
	for _, target := range existing {
		used[target.ID] = true
	}
	add := func(ref, kind, name, autostart string, target Target) {
		present[ref] = true
		target.Name, target.Collector, target.Tags = name, host.ID, []string{"service-switch"}
		target.Source = &TargetSource{Kind: "service-switch", Collector: host.ID, Ref: ref}
		item := ServiceSwitchImportItem{Ref: ref, Kind: kind, Name: name, Autostart: autostart, Action: "create"}
		for _, old := range existing {
			if old.Source != nil && old.Source.Kind == "service-switch" && old.Source.Collector == host.ID && old.Source.Ref == ref {
				target.ID, item.TargetID, item.Action = old.ID, old.ID, "update"
				if equalServiceSwitchTargets(old, target) {
					item.Action = "unchanged"
				} else {
					item.Target = &target
				}
				result.Items = append(result.Items, item)
				return
			}
		}
		for _, old := range existing {
			assigned := old.Collector == host.ID || (host.Hostname != "" && strings.EqualFold(old.Collector, host.Hostname))
			if old.Source == nil && assigned && sameServiceSwitchMatch(old.Match, target.Match) {
				id := old.ID
				item.Action, item.CoveredBy, item.TargetID = "covered", &id, id
				result.Items = append(result.Items, item)
				return
			}
		}
		base := target.ID
		for suffix := 2; used[target.ID]; suffix++ {
			target.ID = base + "-" + strconv.Itoa(suffix)
		}
		used[target.ID], item.TargetID, item.Target = true, target.ID, &target
		result.Items = append(result.Items, item)
	}
	for _, app := range snapshot.Summary.Apps {
		if len(app.Ports) == 0 {
			present["app:"+app.ID] = true
			reason := "没有端口，无法自动识别进程，请手动添加目标"
			result.Items = append(result.Items, ServiceSwitchImportItem{Ref: "app:" + app.ID, Kind: "app", Name: app.Name, Autostart: app.Autostart, Action: "skipped", Reason: &reason})
		} else {
			port := app.Ports[0]
			target := Target{ID: app.ID, Match: Match{Port: &port}}
			if app.HealthPath != "none" {
				target.Probes = append(target.Probes, serviceSwitchProbe("http://127.0.0.1:"+strconv.Itoa(port)+app.HealthPath, "<500", "30s", "10s"))
			}
			for _, other := range app.Ports[1:] {
				target.Probes = append(target.Probes, Probe{Type: "tcp", Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(other)), Interval: "30s", Timeout: "10s"})
			}
			if app.Controller == "lfen-guardian" && app.Guardian.StatusURL != "" {
				target.Probes = append(target.Probes, serviceSwitchProbe(app.Guardian.StatusURL, "2xx", "30s", "10s"))
			}
			if public && app.Domain != "" {
				path := app.HealthPath
				if path == "none" || path == "" {
					path = "/"
				}
				target.Probes = append(target.Probes, serviceSwitchProbe("https://"+app.Domain+path, "<500", "5m", "15s"))
			}
			add("app:"+app.ID, "app", app.Name, app.Autostart, target)
		}
		if port := app.Tunnel.MetricsPort; port > 0 {
			add("tunnel:"+app.ID, "tunnel", app.Name+" Tunnel", app.Autostart, Target{ID: app.ID + "-tunnel", Match: Match{Port: &port}, Probes: []Probe{serviceSwitchProbe("http://127.0.0.1:"+strconv.Itoa(port)+"/ready", "200", "30s", "10s")}})
		}
	}
	if database := snapshot.Summary.Database; database != nil {
		directory, _ := ExecutableDirectory(database.Ctl)
		name := database.Name
		if name == "" {
			name = "PostgreSQL"
		}
		add("database", "database", name, "", Target{ID: "postgresql", Match: Match{Process: "postgres.exe", ExeDir: directory}})
	}
	for _, skipped := range snapshot.Summary.Skipped {
		present[skipped.Ref] = true
		if strings.HasPrefix(skipped.Ref, "app:") {
			present["tunnel:"+strings.TrimPrefix(skipped.Ref, "app:")] = true
		}
		reason := skipped.Reason
		result.Items = append(result.Items, ServiceSwitchImportItem{Ref: skipped.Ref, Kind: skipped.Kind, Name: skipped.Name, Action: "skipped", Reason: &reason})
	}
	for _, old := range existing {
		if old.Source != nil && old.Source.Kind == "service-switch" && old.Source.Collector == host.ID && !present[old.Source.Ref] {
			reason := "这次源文件中已不存在，默认保留"
			result.Items = append(result.Items, ServiceSwitchImportItem{Ref: old.Source.Ref, Kind: strings.Split(old.Source.Ref, ":")[0], Name: old.Name, Action: "missing", TargetID: old.ID, Reason: &reason})
		}
	}
	return result
}

func serviceSwitchProbe(address, status string, interval, timeout Duration) Probe {
	return Probe{Type: "http", URL: address, ExpectStatus: Status(status), Interval: interval, Timeout: timeout}
}

func sameServiceSwitchMatch(a, b Match) bool {
	if b.Port != nil {
		return a.Port != nil && *a.Port == *b.Port
	}
	dir, err := CleanWindowsDirectory(a.ExeDir)
	return err == nil && strings.EqualFold(a.Process, b.Process) && strings.EqualFold(dir, b.ExeDir)
}

func ServiceSwitchTargets(data []byte) ([]Target, error) {
	var file TargetsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("读取当前目标文件失败：%w", err)
	}
	return file.Targets, nil
}

func ApplyServiceSwitchImport(data []byte, preview ServiceSwitchPreview, input ServiceSwitchImportInput) ([]byte, ServiceSwitchImportResult, error) {
	result := ServiceSwitchImportResult{Created: []string{}, Updated: []string{}, Removed: []string{}}
	selected := map[string]bool{}
	for _, ref := range input.Refs {
		if selected[ref] || !slices.ContainsFunc(preview.Items, func(item ServiceSwitchImportItem) bool {
			return item.Ref == ref && (item.Action == "create" || item.Action == "update")
		}) {
			return nil, result, fmt.Errorf("refs 只能包含预览中不重复的 create 或 update 项")
		}
		selected[ref] = true
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, result, fmt.Errorf("解析目标 YAML 失败：%w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, result, fmt.Errorf("targets.yaml 必须是映射对象")
	}
	var list *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "targets" {
			list = root.Content[i+1]
			if list.Tag == "!!null" {
				list.Kind, list.Tag, list.Value = yaml.SequenceNode, "!!seq", ""
			}
			break
		}
	}
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "targets"}, list)
	}
	if list.Kind != yaml.SequenceNode {
		return nil, result, fmt.Errorf("targets 必须是数组")
	}
	changed := false
	for _, item := range preview.Items {
		write := (item.Action == "create" || item.Action == "update") && (input.Refs == nil || selected[item.Ref])
		remove := item.Action == "missing" && input.RemoveMissing
		if !write && !remove {
			continue
		}
		position := -1
		for i, node := range list.Content {
			var target Target
			if node.Decode(&target) == nil && target.ID == item.TargetID {
				position = i
				break
			}
		}
		if remove {
			if position >= 0 {
				list.Content = slices.Delete(list.Content, position, position+1)
				result.Removed = append(result.Removed, item.TargetID)
				changed = true
			}
			continue
		}
		if item.Target == nil {
			return nil, result, fmt.Errorf("预览缺少待写入目标")
		}
		var replacement yaml.Node
		if err := replacement.Encode(item.Target); err != nil {
			return nil, result, fmt.Errorf("编码导入目标失败：%w", err)
		}
		if position >= 0 {
			old := list.Content[position]
			replacement.HeadComment, replacement.LineComment, replacement.FootComment = old.HeadComment, old.LineComment, old.FootComment
			list.Content[position] = &replacement
			result.Updated = append(result.Updated, item.TargetID)
		} else {
			list.Content = append(list.Content, &replacement)
			result.Created = append(result.Created, item.TargetID)
		}
		changed = true
	}
	if !changed {
		result.TargetsVersion = TargetsFingerprint(data)
		return bytes.Clone(data), result, nil
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, result, fmt.Errorf("编码目标 YAML 失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, result, fmt.Errorf("完成目标 YAML 编码失败：%w", err)
	}
	result.TargetsVersion = TargetsFingerprint(buffer.Bytes())
	return buffer.Bytes(), result, nil
}

func equalServiceSwitchTargets(a, b Target) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
