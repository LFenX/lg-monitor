package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/jobs"
	"github.com/LFenX/lg-monitor/internal/store"
)

// Batch jobs: a job reports business events, and lgmon keeps a record per business day.
const (
	ReportJobEvents Operation = "report_job_events"
	ListJobDays     Operation = "list_job_days"
	GetJobDay       Operation = "get_job_day"
	PutJobMonitor   Operation = "put_job_monitor"
)

// JobReport is a batch of events from a job.
type JobReport struct {
	Events []json.RawMessage `json:"events" jsonschema:"minItems=1,maxItems=500" jsonschema_description:"事件，每条只含约定字段：event_id、attempt_id、ts、kind（attempt、stage、partition）以及对应的状态、数量、摘要和回执。"`
}

// JobReportResult says what happened to each event; a rejected one names the field to fix.
type JobReportResult struct {
	Accepted   int                `json:"accepted"`
	Duplicates int                `json:"duplicates" jsonschema_description:"已经收过的 event_id，没有再记一次。"`
	Conflicts  int                `json:"conflicts" jsonschema_description:"event_id 收过但内容不同：保留第一次的。"`
	Rejected   []JobEventRejected `json:"rejected"`
}

type JobEventRejected struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
}

// JobInfo describes a job the way its pages show it.
type JobInfo struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Schedule   string            `json:"schedule" jsonschema_description:"人话描述的计划，例如“每天 02:00（北京时间）”。"`
	Stages     []config.JobStage `json:"stages"`
	Source     string            `json:"source" jsonschema:"enum=api,enum=file"`
	Production []string          `json:"production"`
	// File is how the job's JSON Lines file is being read; absent for jobs that report through the API, and
	// before the collector first reported on it.
	File *store.JobSource `json:"file,omitempty" jsonschema_description:"数据来源是文件时：哪个采集器在读、路径、能否读取（state）、最近一次问题（problem，第几行）和最近一次收到新事件的时间。"`
	// Path and Collector are where a file source is configured to be read.
	Path      string `json:"path,omitempty"`
	Collector string `json:"collector,omitempty"`
}

// JobDays is a job's recent daily records, newest first.
type JobDays struct {
	Job  JobInfo    `json:"job"`
	Days []jobs.Day `json:"days"`
}

// JobDependency is the current state of a production-path monitor the job relies on.
type JobDependency struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state" jsonschema:"enum=up,enum=down,enum=unknown"`
	Issue string `json:"issue,omitempty"`
}

// JobDayDetail is one business day: every run, and the production path shown two ways — as it is now, and
// whether this run's fetch from it succeeded.
type JobDayDetail struct {
	Job        JobInfo         `json:"job"`
	Day        jobs.Day        `json:"day"`
	Production []JobDependency `json:"production" jsonschema_description:"生产通路监控现在是否正常。"`
	Fetch      *jobs.StageView `json:"fetch,omitempty" jsonschema_description:"最近一次运行的生产数据获取阶段：本次取数是否成功。"`
}

// JobBrief is a job's latest meaningful day, shown on its board row.
type JobBrief struct {
	Date    string      `json:"date"`
	Status  jobs.Status `json:"status"`
	Summary string      `json:"summary"`
}

func jobRoutes() []Route {
	days := []Parameter{{"days", "最近几天，1～90，默认 14"}}
	return []Route{
		{ReportJobEvents, "POST", "/api/v1/jobs/{id}/events", "批处理任务上报业务事件：用这个任务的上报密钥（lgmon job key 生成，只能上报）或 config 令牌；按 event_id 去重，重复发送无害", ScopeConfig, JobReport{}, JobReportResult{}, nil},
		{ListJobDays, "GET", "/api/v1/jobs/{id}/days", "批处理任务最近每天一条记录：状态、说明和各次运行", ScopeRead, nil, JobDays{}, days},
		{PutJobMonitor, "PUT", "/api/v1/jobs/{id}", "创建或修改批处理任务监控：计划、阶段、生产通路依赖、数据来源，挂在哪个主服务下、放进哪个面板；不发放上报密钥（用 lgmon job key 在监控器电脑上生成）", ScopeConfig, JobMonitorInput{}, JobMonitorResult{}, nil},
		{GetJobDay, "GET", "/api/v1/jobs/{id}/days/{date}", "批处理任务某个业务日期（北京时间）的明细：每次运行的阶段时间线、小时分区数据、生产通路现状与本次取数结果", ScopeRead, nil, JobDayDetail{}, nil},
	}
}

// JobMonitorInput creates or updates a batch job monitor. The job's own settings are replaced as a whole on update;
// description and parent keep their saved values when omitted.
type JobMonitorInput struct {
	Name        string             `json:"name" jsonschema:"minLength=1,maxLength=80" jsonschema_description:"显示的名字，例如 AICS 每日上传。"`
	Description *string            `json:"description,omitempty" jsonschema:"maxLength=300" jsonschema_description:"一两句说明：它做什么、出问题影响什么。"`
	Parent      *string            `json:"parent,omitempty" jsonschema_description:"挂在哪个主服务下面（主服务 ID，例如 AICS 主服务）；空字符串改回独立的服务。"`
	Board       string             `json:"board,omitempty" jsonschema_description:"放进哪个面板（board ID）；省略时跟随主服务，或放进第一个面板。"`
	Schedule    config.JobSchedule `json:"schedule,omitempty" jsonschema_description:"计划：at 北京时间 HH:MM，省略表示不判定漏跑；days、from、skip 决定哪些日期启用。"`
	MaxDuration config.Duration    `json:"maxDuration,omitempty" jsonschema_description:"一次运行最长多久，超过还没有新事件也没结束算结果未知；默认 6h。"`
	Stages      []config.JobStage  `json:"stages,omitempty" jsonschema:"maxItems=20" jsonschema_description:"阶段；省略用预设七个：fetch、verify、package、backup、upload_cn、upload_overseas、manifest。"`
	Production  []string           `json:"production,omitempty" jsonschema:"maxItems=10" jsonschema_description:"生产通路上已有的监控 ID（list_monitors 里找），详情里分开显示它们的现状和本次取数结果。"`
	Source      config.JobSource   `json:"source,omitempty" jsonschema_description:"事件从哪里来：api（默认，任务自己上报）或 file（采集器读取 JSON Lines 文件，需要 path 和 collector）。"`
	Collector   string             `json:"collector,omitempty" jsonschema_description:"source.kind 为 file 时，读文件的那台采集器的 ID 或主机名（list_collectors 里找）。"`
}

// JobMonitorResult is the saved job, where it shows, and how it gets its events.
type JobMonitorResult struct {
	Created    bool     `json:"created" jsonschema_description:"false 表示更新了已有的任务监控。"`
	Job        JobInfo  `json:"job"`
	Board      string   `json:"board,omitempty"`
	BoardURL   string   `json:"boardUrl,omitempty"`
	MonitorURL string   `json:"monitorUrl" jsonschema_description:"任务详情页：每天一条记录，可以展开看。"`
	Next       []string `json:"next" jsonschema_description:"接着要做的事，可直接转述给用户。"`
}

// JobKeys finds the job a report key belongs to.
type JobKeys interface {
	JobForKey(context.Context, [32]byte) (string, bool, error)
}

// JobKeyPrefix marks report keys, so they are never mistaken for API tokens.
const JobKeyPrefix = "lgjob_"

// jobKeyAuth lets a job report with its own key; any other caller goes through the usual token check.
func (s *Server) jobKeyAuth(handler, protected http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !strings.HasPrefix(value, JobKeyPrefix) || s.options.JobKeys == nil {
			protected.ServeHTTP(w, r)
			return
		}
		if err := s.options.Auth.CheckHost(r); err != nil {
			writeError(w, 403, "host_forbidden", "请求的主机名不在允许范围内")
			return
		}
		job, found, err := s.options.JobKeys.JobForKey(r.Context(), sha256.Sum256([]byte(value)))
		if err != nil {
			writeError(w, 500, "internal_error", "核对上报密钥失败，请检查服务日志")
			return
		}
		if !found || job != r.PathValue("id") {
			writeError(w, 401, "unauthorized", "上报密钥无效，或不属于这个任务；用 lgmon job key 重新生成")
			return
		}
		principal := Principal{Name: "任务上报 " + job, Scope: ScopeRead}
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}
