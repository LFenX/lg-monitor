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
		{GetJobDay, "GET", "/api/v1/jobs/{id}/days/{date}", "批处理任务某个业务日期（北京时间）的明细：每次运行的阶段时间线、小时分区数据、生产通路现状与本次取数结果", ScopeRead, nil, JobDayDetail{}, nil},
	}
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
