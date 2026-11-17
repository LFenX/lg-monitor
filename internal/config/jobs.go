package config

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Job makes a target a batch job: its state comes only from the job's own business events (see internal/jobs),
// never from a process, port or exit code.
type Job struct {
	Schedule    JobSchedule `yaml:"schedule,omitempty" json:"schedule,omitempty" jsonschema_description:"什么时候应该运行；用来判定待运行和漏跑。"`
	MaxDuration Duration    `yaml:"maxDuration,omitempty" json:"maxDuration,omitempty" jsonschema_description:"一次运行最长多久；超过它还没有新事件也没结束就算结果未知。默认 6h。"`
	Stages      []JobStage  `yaml:"stages,omitempty" json:"stages,omitempty" jsonschema:"maxItems=20" jsonschema_description:"阶段，按执行顺序。省略时用预设的七个：fetch、verify、package、backup、upload_cn、upload_overseas、manifest。"`
	Production  []string    `yaml:"production,omitempty" json:"production,omitempty" jsonschema:"maxItems=10" jsonschema_description:"生产通路上已有的监控 ID；详情里显示它们现在是否正常，与本次取数结果分开。"`
	Source      JobSource   `yaml:"source,omitempty" json:"source,omitempty" jsonschema_description:"事件从哪里来。"`
}

// JobSchedule is when a job should run, in Beijing time.
type JobSchedule struct {
	At    string   `yaml:"at,omitempty" json:"at,omitempty" jsonschema:"pattern=^([01][0-9]|2[0-3]):[0-5][0-9]$" jsonschema_description:"计划开始时间（北京时间 HH:MM）；省略表示不判定漏跑。"`
	Grace Duration `yaml:"grace,omitempty" json:"grace,omitempty" jsonschema_description:"计划时间过后多久还没有任何运行算漏跑；默认 2h。"`
	Days  []string `yaml:"days,omitempty" json:"days,omitempty" jsonschema_description:"一周中哪几天运行：mon、tue、wed、thu、fri、sat、sun；省略表示每天。"`
	From  string   `yaml:"from,omitempty" json:"from,omitempty" jsonschema:"pattern=^[0-9]{4}-[0-9]{2}-[0-9]{2}$" jsonschema_description:"从哪个业务日期开始启用；之前的日期不算漏跑。"`
	Skip  []string `yaml:"skip,omitempty" json:"skip,omitempty" jsonschema:"maxItems=366" jsonschema_description:"不运行的业务日期（YYYY-MM-DD），不算漏跑。"`
}

// JobStage is one step of a run. Critical stages failing fail the whole run; receipt stages need a receipt to
// count as successful.
type JobStage struct {
	ID       string `yaml:"id" json:"id" jsonschema:"pattern=^[a-z][a-z0-9_]{0\\,31}$"`
	Name     string `yaml:"name,omitempty" json:"name,omitempty" jsonschema:"maxLength=40" jsonschema_description:"显示的名字；预设阶段可省略。"`
	Critical *bool  `yaml:"critical,omitempty" json:"critical,omitempty" jsonschema_description:"失败时整次运行算失败；预设的 fetch、verify、package 默认是。"`
	Receipt  *bool  `yaml:"receipt,omitempty" json:"receipt,omitempty" jsonschema_description:"成功必须带回执，否则算结果未知；预设的两个上传阶段默认是。"`
}

// JobSource is where a job's events come from: the report API, or a JSON Lines file a collector reads.
type JobSource struct {
	Kind string `yaml:"kind,omitempty" json:"kind,omitempty" jsonschema:"enum=api,enum=file" jsonschema_description:"api：任务自己调用上报接口（默认）；file：采集器读取一个只追加的 JSON Lines 文件。"`
	Path string `yaml:"path,omitempty" json:"path,omitempty" jsonschema:"maxLength=260" jsonschema_description:"file 时文件在采集器电脑上的绝对路径。"`
}

// presetStages are the AICS daily upload's steps, which most batch jobs resemble.
var presetStages = []JobStage{
	{ID: "fetch", Name: "生产数据获取", Critical: ptr(true)},
	{ID: "verify", Name: "中转校验", Critical: ptr(true)},
	{ID: "package", Name: "打包", Critical: ptr(true)},
	{ID: "backup", Name: "备份"},
	{ID: "upload_cn", Name: "国内 OSS 上传", Receipt: ptr(true)},
	{ID: "upload_overseas", Name: "海外 S3 上传", Receipt: ptr(true)},
	{ID: "manifest", Name: "最终清单核对"},
}

func ptr(value bool) *bool { return &value }

// EffectiveStages fills names and flags of preset stages, and returns the presets when none are configured.
func (j Job) EffectiveStages() []JobStage {
	if len(j.Stages) == 0 {
		return slices.Clone(presetStages)
	}
	result := make([]JobStage, 0, len(j.Stages))
	for _, stage := range j.Stages {
		if index := slices.IndexFunc(presetStages, func(preset JobStage) bool { return preset.ID == stage.ID }); index >= 0 {
			preset := presetStages[index]
			if stage.Name == "" {
				stage.Name = preset.Name
			}
			if stage.Critical == nil {
				stage.Critical = preset.Critical
			}
			if stage.Receipt == nil {
				stage.Receipt = preset.Receipt
			}
		}
		if stage.Name == "" {
			stage.Name = stage.ID
		}
		result = append(result, stage)
	}
	return result
}

// Limits returns the grace and maximum run time with their defaults.
func (j Job) Limits() (grace, maxDuration time.Duration) {
	grace, maxDuration = 2*time.Hour, 6*time.Hour
	if value, err := j.Schedule.Grace.Parse(); err == nil && value > 0 {
		grace = value
	}
	if value, err := j.MaxDuration.Parse(); err == nil && value > 0 {
		maxDuration = value
	}
	return grace, maxDuration
}

var (
	jobStageID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	jobTime    = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	weekdays   = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

func validDate(text string) bool {
	_, err := time.Parse(time.DateOnly, text)
	return err == nil
}

// validateJob checks a job target. Jobs are watched through their events, so they carry no match or probes.
func validateJob(target Target, at func(parts ...any) location, targets map[string]Target, issues *Errors) {
	job := target.Job
	if target.HasMatch() || len(target.Probes) > 0 || target.Task != "" || target.EventLog != nil {
		at("job").add(issues, "批处理任务 %s 只靠业务事件判断状态，不能同时配置 match、probes、task 或 eventlog", target.ID)
	}
	schedule := job.Schedule
	if schedule.At != "" && !jobTime.MatchString(schedule.At) {
		at("job", "schedule", "at").add(issues, "批处理任务 %s 的计划时间必须是北京时间 HH:MM", target.ID)
	}
	for field, value := range map[string]Duration{"grace": schedule.Grace} {
		if parsed, err := value.Parse(); value != "" && (err != nil || parsed <= 0 || parsed > 24*time.Hour) {
			at("job", "schedule", field).add(issues, "批处理任务 %s 的 grace 必须是 1s～24h 的时长", target.ID)
		}
	}
	if parsed, err := job.MaxDuration.Parse(); job.MaxDuration != "" && (err != nil || parsed <= 0 || parsed > 72*time.Hour) {
		at("job", "maxDuration").add(issues, "批处理任务 %s 的 maxDuration 必须是 1s～72h 的时长", target.ID)
	}
	for _, day := range schedule.Days {
		if !slices.Contains(weekdays, day) {
			at("job", "schedule", "days").add(issues, "批处理任务 %s 的 days 只能是 mon、tue、wed、thu、fri、sat、sun", target.ID)
			break
		}
	}
	if schedule.From != "" && !validDate(schedule.From) {
		at("job", "schedule", "from").add(issues, "批处理任务 %s 的 from 必须是 YYYY-MM-DD", target.ID)
	}
	if len(schedule.Skip) > 366 || slices.ContainsFunc(schedule.Skip, func(day string) bool { return !validDate(day) }) {
		at("job", "schedule", "skip").add(issues, "批处理任务 %s 的 skip 最多 366 个 YYYY-MM-DD 日期", target.ID)
	}
	if len(job.Stages) > 20 {
		at("job", "stages").add(issues, "批处理任务 %s 最多 20 个阶段", target.ID)
	}
	seen := map[string]bool{}
	for index, stage := range job.Stages {
		if !jobStageID.MatchString(stage.ID) || seen[stage.ID] {
			at("job", "stages", index, "id").add(issues, "阶段 ID 必须是小写字母开头的字母、数字或下划线，最长 32 个字符，不能重复")
		}
		seen[stage.ID] = true
		if utf8.RuneCountInString(stage.Name) > 40 {
			at("job", "stages", index, "name").add(issues, "阶段名字最多 40 个字")
		}
	}
	if len(job.Production) > 10 {
		at("job", "production").add(issues, "批处理任务 %s 最多引用 10 个生产通路监控", target.ID)
	}
	for _, id := range job.Production {
		if id == target.ID || targets[id].ID == "" || targets[id].Job != nil {
			at("job", "production").add(issues, "批处理任务 %s 的 production 必须引用已有的服务监控（不是任务自己或别的任务）：%s", target.ID, id)
		}
	}
	switch job.Source.Kind {
	case "", "api":
		if job.Source.Path != "" {
			at("job", "source", "path").add(issues, "批处理任务 %s 用上报接口时不需要 path", target.ID)
		}
	case "file":
		path := job.Source.Path
		if !filepath.IsAbs(path) || filepath.VolumeName(path) == "" || strings.ContainsAny(path, "*?\"<>|") || utf8.RuneCountInString(path) > 260 {
			at("job", "source", "path").add(issues, "批处理任务 %s 的 path 必须是采集器电脑上的 Windows 绝对路径", target.ID)
		}
		if target.Collector == "" || target.Collector == "*" {
			at("collector").add(issues, "批处理任务 %s 从文件读取事件时，collector 必须指定读取它的那台采集器", target.ID)
		}
	default:
		at("job", "source", "kind").add(issues, "批处理任务 %s 的 source.kind 只能是 api 或 file", target.ID)
	}
}
