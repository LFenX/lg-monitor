package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/jobs"
	"github.com/LFenX/lg-monitor/internal/store"
)

type jobEventReader interface {
	JobEvents(ctx context.Context, jobID, from, to string) ([]store.StoredJobEvent, error)
}

type jobSourceReader interface {
	JobSource(ctx context.Context, jobID string) (store.JobSource, bool, error)
}

type jobEventWriter interface {
	AppendJobEvents(context.Context, []store.JobEventRow) (store.JobAppendResult, error)
}

func (b *APIBackend) jobTarget(cfg *config.Config, id string) (config.Target, error) {
	target := targetIndex(cfg)[id]
	if target.ID == "" {
		return target, missingAPI("批处理任务 " + id + " 不存在；先用 put_job_monitor 创建")
	}
	if target.Job == nil {
		return target, invalidAPI(id + " 不是批处理任务，是普通服务监控")
	}
	return target, nil
}

func jobInfo(target config.Target) api.JobInfo {
	job := *target.Job
	info := api.JobInfo{ID: target.ID, Name: target.Name, Stages: job.EffectiveStages(), Source: "api", Production: slices.Clone(job.Production)}
	if info.Name == "" {
		info.Name = target.ID
	}
	if job.Source.Kind == "file" {
		info.Source, info.Path, info.Collector = "file", job.Source.Path, target.Collector
	}
	if info.Production == nil {
		info.Production = []string{}
	}
	info.Schedule = scheduleText(job.Schedule)
	return info
}

var weekdayNames = map[string]string{"mon": "一", "tue": "二", "wed": "三", "thu": "四", "fri": "五", "sat": "六", "sun": "日"}

func scheduleText(schedule config.JobSchedule) string {
	if schedule.At == "" {
		return "按上报的事件判断，不判定漏跑"
	}
	days := "每天"
	if len(schedule.Days) > 0 && len(schedule.Days) < 7 {
		names := make([]string, 0, len(schedule.Days))
		for _, day := range schedule.Days {
			names = append(names, weekdayNames[day])
		}
		days = "每周" + strings.Join(names, "、")
	}
	return days + " " + schedule.At + "（北京时间）"
}

// reportJobEvents stores what a job reported. Each event is checked on its own, so one bad line does not lose
// the rest; the reporter learns which ones to fix. Reporting never asks the job to redo anything.
func (b *APIBackend) reportJobEvents(ctx context.Context, cfg *config.Config, r api.Request) (api.JobReportResult, error) {
	id := r.Params["id"]
	if _, err := b.jobTarget(cfg, id); err != nil {
		return api.JobReportResult{}, err
	}
	var report api.JobReport
	if err := api.DecodeBody(r.Body, &report); err != nil {
		return api.JobReportResult{}, err
	}
	if len(report.Events) == 0 || len(report.Events) > 500 {
		return api.JobReportResult{}, invalidAPI("一次上报 1～500 条事件")
	}
	result := api.JobReportResult{Rejected: []api.JobEventRejected{}}
	rows := make([]store.JobEventRow, 0, len(report.Events))
	for index, raw := range report.Events {
		event, err := jobs.Parse(raw)
		if err == nil && event.JobID != "" && event.JobID != id {
			err = fmt.Errorf("job_id %s 与上报地址里的任务 %s 不一致", event.JobID, id)
		}
		if err != nil {
			result.Rejected = append(result.Rejected, api.JobEventRejected{Index: index, Message: err.Error()})
			continue
		}
		row, err := jobRow(id, "api", event)
		if err != nil {
			return result, err
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return result, invalidAPI("没有一条事件可以接收：第 " + strconv.Itoa(result.Rejected[0].Index+1) + " 条 " + result.Rejected[0].Message)
	}
	writer, ok := b.options.Write.(jobEventWriter)
	if !ok {
		return result, &api.Fault{Status: 503, Code: "jobs_unavailable", Message: "这个监控器还不能保存批处理任务事件"}
	}
	stored, err := writer.AppendJobEvents(ctx, rows)
	if err != nil {
		return result, err
	}
	result.Accepted, result.Duplicates, result.Conflicts = stored.Accepted, stored.Duplicates, stored.Conflicts
	return result, nil
}

func jobRow(id, source string, event jobs.Event) (store.JobEventRow, error) {
	event.JobID = ""
	body, err := json.Marshal(event)
	if err != nil {
		return store.JobEventRow{}, fmt.Errorf("编码任务事件失败：%w", err)
	}
	return store.JobEventRow{JobID: id, EventID: event.EventID, AttemptID: event.AttemptID, BusinessDate: event.BusinessDate, Fallback: jobs.DateOf(event.TS), TS: event.TS, Source: source, Body: body}, nil
}

// jobDays builds the records for the n business days ending today, newest first.
func (b *APIBackend) jobDays(ctx context.Context, target config.Target, n int) ([]jobs.Day, error) {
	now := b.options.Now()
	today, _ := time.ParseInLocation(time.DateOnly, jobs.DateOf(now.UnixMilli()), jobs.Beijing)
	dates := make([]string, n)
	for i := range dates {
		dates[i] = today.AddDate(0, 0, -i).Format(time.DateOnly)
	}
	return b.jobRecords(ctx, target, dates)
}

func (b *APIBackend) jobRecords(ctx context.Context, target config.Target, dates []string) ([]jobs.Day, error) {
	reader, ok := b.options.Read.(jobEventReader)
	if !ok || len(dates) == 0 {
		return jobs.Days(*target.Job, nil, dates, b.options.Now()), nil
	}
	from, to := slices.Min(dates), slices.Max(dates)
	stored, err := reader.JobEvents(ctx, target.ID, from, to)
	if err != nil {
		return nil, err
	}
	recorded := make([]jobs.Recorded, 0, len(stored))
	for _, item := range stored {
		var event jobs.Event
		if json.Unmarshal(item.Body, &event) != nil {
			continue
		}
		recorded = append(recorded, jobs.Recorded{Event: event, BusinessDate: item.BusinessDate, ReceivedAt: item.ReceivedAt})
	}
	return jobs.Days(*target.Job, recorded, dates, b.options.Now()), nil
}

func (b *APIBackend) listJobDays(ctx context.Context, cfg *config.Config, r api.Request) (api.JobDays, error) {
	target, err := b.jobTarget(cfg, r.Params["id"])
	if err != nil {
		return api.JobDays{}, err
	}
	n := 14
	if text := r.Query.Get("days"); text != "" {
		value, err := strconv.Atoi(text)
		if err != nil || value < 1 || value > 90 {
			return api.JobDays{}, invalidAPI("days 必须是 1～90")
		}
		n = value
	}
	days, err := b.jobDays(ctx, target, n)
	if err != nil {
		return api.JobDays{}, err
	}
	info, err := b.jobInfo(ctx, target)
	if err != nil {
		return api.JobDays{}, err
	}
	return api.JobDays{Job: info, Days: days}, nil
}

// jobInfo adds how a file source is being read to what the configuration says.
func (b *APIBackend) jobInfo(ctx context.Context, target config.Target) (api.JobInfo, error) {
	info := jobInfo(target)
	reader, ok := b.options.Read.(jobSourceReader)
	if info.Source != "file" || !ok {
		return info, nil
	}
	source, found, err := reader.JobSource(ctx, target.ID)
	if err != nil {
		return info, err
	}
	if found {
		info.File = &source
	}
	return info, nil
}

func (b *APIBackend) getJobDay(ctx context.Context, cfg *config.Config, r api.Request) (api.JobDayDetail, error) {
	target, err := b.jobTarget(cfg, r.Params["id"])
	if err != nil {
		return api.JobDayDetail{}, err
	}
	date := r.Params["date"]
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return api.JobDayDetail{}, invalidAPI("业务日期必须是北京时间的 YYYY-MM-DD")
	}
	days, err := b.jobRecords(ctx, target, []string{date})
	if err != nil {
		return api.JobDayDetail{}, err
	}
	info, err := b.jobInfo(ctx, target)
	if err != nil {
		return api.JobDayDetail{}, err
	}
	detail := api.JobDayDetail{Job: info, Day: days[0], Production: []api.JobDependency{}}
	if len(detail.Day.Attempts) > 0 {
		for _, stage := range detail.Day.Attempts[0].Stages {
			if stage.ID == "fetch" {
				detail.Fetch = &stage
			}
		}
	}
	if len(target.Job.Production) > 0 {
		monitors, err := b.monitors(ctx, cfg)
		if err != nil {
			return detail, err
		}
		for _, id := range target.Job.Production {
			for _, monitor := range monitors {
				if monitor.ID == id {
					state := monitor.Overall
					if state == "" {
						state = monitor.State
					}
					detail.Production = append(detail.Production, api.JobDependency{ID: id, Name: monitor.Name, State: state, Issue: monitor.Issue})
				}
			}
		}
	}
	return detail, nil
}

// jobBrief picks the day a board row should talk about: today once it has news, otherwise the last day that did.
func jobBrief(days []jobs.Day) (*api.JobBrief, string) {
	for _, day := range days {
		if day.Status == jobs.Disabled || day.Status == jobs.Pending && len(day.Attempts) == 0 {
			continue
		}
		state := "unknown"
		switch day.Status {
		case jobs.Success:
			state = "up"
		case jobs.Failed, jobs.Partial, jobs.Missed:
			state = "down"
		}
		return &api.JobBrief{Date: day.Date, Status: day.Status, Summary: day.Summary}, state
	}
	if len(days) > 0 {
		return &api.JobBrief{Date: days[0].Date, Status: days[0].Status, Summary: days[0].Summary}, "unknown"
	}
	return nil, "unknown"
}
