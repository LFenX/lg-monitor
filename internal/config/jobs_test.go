package config

import (
	"strings"
	"testing"
)

func TestJobTargetsAreCheckedAsJobs(t *testing.T) {
	base := func(job Job, collector string) *Config {
		return &Config{Targets: []Target{{ID: "aics-db", Match: Match{Service: "postgresql"}}, {ID: "aics-upload", Collector: collector, Job: &job}}}
	}
	ok := base(Job{Schedule: JobSchedule{At: "02:00", Grace: "2h", Days: []string{"mon", "fri"}, From: "2026-11-20", Skip: []string{"2026-12-25"}}, MaxDuration: "6h", Production: []string{"aics-db"}, Stages: []JobStage{{ID: "fetch"}, {ID: "export", Name: "导出"}}}, "")
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	if err := Validate(base(Job{Source: JobSource{Kind: "file", Path: `D:\aics\events.jsonl`}}, "fake-collector")); err != nil {
		t.Fatal(err)
	}
	for want, cfg := range map[string]*Config{
		"HH:MM":         base(Job{Schedule: JobSchedule{At: "2:00"}}, ""),
		"days":          base(Job{Schedule: JobSchedule{Days: []string{"monday"}}}, ""),
		"from":          base(Job{Schedule: JobSchedule{From: "2026/11/20"}}, ""),
		"maxDuration":   base(Job{MaxDuration: "100h"}, ""),
		"阶段 ID":         base(Job{Stages: []JobStage{{ID: "fetch"}, {ID: "fetch"}}}, ""),
		"production":    base(Job{Production: []string{"aics-upload"}}, ""),
		"Windows 绝对路径":  base(Job{Source: JobSource{Kind: "file", Path: `events.jsonl`}}, "fake-collector"),
		"那台采集器":         base(Job{Source: JobSource{Kind: "file", Path: `D:\aics\events.jsonl`}}, ""),
		"source.kind":   base(Job{Source: JobSource{Kind: "ftp"}}, ""),
		"不需要 path":      base(Job{Source: JobSource{Path: `D:\x`}}, ""),
		"不能同时配置 match":  {Targets: []Target{{ID: "aics-upload", Match: Match{Process: "aics.exe"}, Job: &Job{}}}},
		"production 必须": base(Job{Production: []string{"ghost"}}, ""),
	} {
		if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestOnlyTheAssignedCollectorReceivesAJobFile(t *testing.T) {
	cfg := &Config{Targets: []Target{
		{ID: "aics-db", Match: Match{Service: "postgresql"}},
		{ID: "file-job", Name: "AICS 每日上传", Collector: "fake-collector", Description: "虚构说明", Job: &Job{Schedule: JobSchedule{At: "02:00"}, Production: []string{"aics-db"}, Source: JobSource{Kind: "file", Path: `D:\aics\events.jsonl`}}},
		{ID: "host-job", Collector: "PROD-01", Job: &Job{Source: JobSource{Kind: "file", Path: `D:\aics\other.jsonl`}}},
		{ID: "api-job", Job: &Job{Schedule: JobSchedule{At: "03:00"}}},
	}}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	version, data, err := TargetConfig(cfg, "fake-collector", "prod-01")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, absent := range []string{"api-job", "02:00", "production", "AICS", "虚构说明"} {
		if strings.Contains(text, absent) {
			t.Fatalf("%s sent to the collector: %s", absent, text)
		}
	}
	// What the collector gets must still pass its own check, without the targets it does not watch.
	received, err := ParseTargetConfig(version, data)
	if err != nil {
		t.Fatalf("collector rejects its job files: %v (%s)", err, text)
	}
	files := map[string]string{}
	for _, target := range received.Targets {
		if target.Job != nil {
			files[target.ID] = target.Job.Source.Path
		}
	}
	if len(files) != 2 || files["file-job"] != `D:\aics\events.jsonl` || files["host-job"] != `D:\aics\other.jsonl` {
		t.Fatalf("job files sent = %v", files)
	}
	_, other, err := TargetConfig(cfg, "other-collector", "other-host")
	if err != nil || strings.Contains(string(other), "file-job") || strings.Contains(string(other), "host-job") {
		t.Fatalf("another collector got a job file: %s %v", other, err)
	}
}
