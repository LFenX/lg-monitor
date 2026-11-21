// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package jobs

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

// Status is how a day or a run went.
type Status string

const (
	Pending  Status = "pending"  // 待运行: an enabled day whose run is not due yet
	Running  Status = "running"  // 运行中
	Success  Status = "success"  // 成功: every stage reported success, with receipts where needed
	Partial  Status = "partial"  // 部分失败: something failed, but not the run as a whole
	Failed   Status = "failed"   // 失败: a critical stage failed, every upload failed, or the run aborted
	Missed   Status = "missed"   // 漏跑: an enabled day past its schedule and grace with no run
	Unknown  Status = "unknown"  // 结果未知: no news for too long, a stage unknown, or an upload without receipt
	Disabled Status = "disabled" // 未启用: outside the schedule; never counted as missed
)

// Recorded is a stored event with when lgmon got it.
type Recorded struct {
	Event
	BusinessDate string
	ReceivedAt   int64
}

// StageView is one stage of one run, as its latest event left it.
type StageView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Progress   *int   `json:"progress,omitempty"`
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	Error      string `json:"error,omitempty"`
	Receipt    string `json:"receipt,omitempty"`
	Critical   bool   `json:"critical,omitempty"`
	// NeedsReceipt stages only count as successful with a receipt.
	NeedsReceipt bool `json:"needsReceipt,omitempty"`
	Updates      int  `json:"updates"`
}

// Partition is one region's one mail hour in one run.
type Partition struct {
	Region           string `json:"region"`
	RegionName       string `json:"regionName"`
	Hour             int64  `json:"hour"`
	HourLabel        string `json:"hourLabel" jsonschema_description:"小时分区：国内按 UTC+8、海外按 UTC+0 显示，带年份。"`
	UniqueRecipients *int64 `json:"uniqueRecipients,omitempty"`
	UniqueSenders    *int64 `json:"uniqueSenders,omitempty"`
	ReceivedRows     *int64 `json:"receivedRows,omitempty"`
	SentRows         *int64 `json:"sentRows,omitempty"`
	GzFiles          *int64 `json:"gzFiles,omitempty"`
	CompressedBytes  *int64 `json:"compressedBytes,omitempty"`
	TargetDir        string `json:"targetDir,omitempty"`
	Verify           string `json:"verify,omitempty"`
	Upload           string `json:"upload,omitempty"`
	Receipt          string `json:"receipt,omitempty"`
	TS               int64  `json:"ts"`
}

// Attempt is one run of a day, scheduled or a manual rerun.
type Attempt struct {
	ID          string      `json:"id"`
	Trigger     string      `json:"trigger"`
	Status      Status      `json:"status"`
	Summary     string      `json:"summary"`
	StartedAt   int64       `json:"startedAt"`
	FinishedAt  int64       `json:"finishedAt,omitempty"`
	LastEventAt int64       `json:"lastEventAt"`
	Events      int         `json:"events"`
	Stages      []StageView `json:"stages"`
	Partitions  []Partition `json:"partitions"`
}

// Day is a job's record for one business date: its status is the latest run's, and every run is kept.
type Day struct {
	Date     string    `json:"date"`
	Status   Status    `json:"status"`
	Summary  string    `json:"summary"`
	Enabled  bool      `json:"enabled"`
	Expected int64     `json:"expected,omitempty" jsonschema_description:"计划开始时间，UTC 毫秒；没配计划时为 0。"`
	Attempts []Attempt `json:"attempts"`
}

var regionNames = map[string]string{"cn": "国内", "overseas": "海外"}

// regionZone is how a region's directories name hours: domestic ones in UTC+8, overseas ones in UTC.
func regionZone(region string) (*time.Location, string) {
	if region == "cn" {
		return Beijing, "UTC+8"
	}
	return time.UTC, "UTC+0"
}

// HourLabel names a partition hour the way its region's directories do, year included.
func HourLabel(region string, hour int64) string {
	zone, name := regionZone(region)
	return time.UnixMilli(hour).In(zone).Format("2006/01/02 15") + " 时（" + name + "）"
}

// Enabled reports whether the schedule expects a run on date, and when.
func Enabled(job config.Job, date string) (bool, int64) {
	day, err := time.ParseInLocation(time.DateOnly, date, Beijing)
	if err != nil {
		return false, 0
	}
	schedule := job.Schedule
	if schedule.From != "" && date < schedule.From || slices.Contains(schedule.Skip, date) {
		return false, 0
	}
	if len(schedule.Days) > 0 && !slices.Contains(schedule.Days, strings.ToLower(day.Weekday().String()[:3])) {
		return false, 0
	}
	if schedule.At == "" {
		return true, 0
	}
	at, err := time.Parse("15:04", schedule.At)
	if err != nil {
		return true, 0
	}
	return true, day.Add(time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute).UnixMilli()
}

// Days builds the records for dates, newest first, from every stored event of the job.
func Days(job config.Job, events []Recorded, dates []string, now time.Time) []Day {
	byDate := map[string][]Recorded{}
	for _, event := range events {
		byDate[event.BusinessDate] = append(byDate[event.BusinessDate], event)
	}
	grace, maxDuration := job.Limits()
	result := make([]Day, 0, len(dates))
	for _, date := range dates {
		enabled, expected := Enabled(job, date)
		day := Day{Date: date, Enabled: enabled, Expected: expected, Attempts: attempts(job, byDate[date], now, maxDuration)}
		switch {
		case len(day.Attempts) > 0:
			day.Status, day.Summary = day.Attempts[0].Status, day.Attempts[0].Summary
			if len(day.Attempts) > 1 {
				day.Summary += fmt.Sprintf("（共 %d 次运行）", len(day.Attempts))
			}
		case !enabled:
			day.Status, day.Summary = Disabled, "未启用"
		case expected == 0:
			day.Status, day.Summary = Pending, "还没有运行"
		case now.UnixMilli() > expected+grace.Milliseconds():
			day.Status, day.Summary = Missed, fmt.Sprintf("计划 %s 运行，过了宽限 %s还没有任何运行", job.Schedule.At, hours(grace))
		default:
			day.Status, day.Summary = Pending, "计划 "+job.Schedule.At+" 运行"
		}
		result = append(result, day)
	}
	return result
}

func hours(value time.Duration) string {
	if value%time.Hour == 0 {
		return fmt.Sprintf("%d 小时", int(value/time.Hour))
	}
	return fmt.Sprintf("%d 分钟", int(value/time.Minute))
}

// attempts folds a day's events into runs, newest run first.
func attempts(job config.Job, events []Recorded, now time.Time, maxDuration time.Duration) []Attempt {
	// Later events win; equal times fall back to the order lgmon received them.
	slices.SortStableFunc(events, func(a, b Recorded) int {
		return cmp.Or(cmp.Compare(a.TS, b.TS), cmp.Compare(a.ReceivedAt, b.ReceivedAt))
	})
	order := []string{}
	byID := map[string][]Recorded{}
	for _, event := range events {
		if byID[event.AttemptID] == nil {
			order = append(order, event.AttemptID)
		}
		byID[event.AttemptID] = append(byID[event.AttemptID], event)
	}
	result := make([]Attempt, 0, len(order))
	for index, id := range order {
		result = append(result, attempt(job, id, byID[id], index, now, maxDuration))
	}
	slices.SortStableFunc(result, func(a, b Attempt) int { return cmp.Compare(b.StartedAt, a.StartedAt) })
	return result
}

func attempt(job config.Job, id string, events []Recorded, index int, now time.Time, maxDuration time.Duration) Attempt {
	run := Attempt{ID: id, Trigger: "scheduled", Events: len(events), Partitions: []Partition{}}
	if index > 0 {
		run.Trigger = "manual"
	}
	stages := job.EffectiveStages()
	views := make([]StageView, len(stages))
	for i, stage := range stages {
		views[i] = StageView{ID: stage.ID, Name: stage.Name, State: "pending", Critical: stage.Critical != nil && *stage.Critical, NeedsReceipt: stage.Receipt != nil && *stage.Receipt}
	}
	partitions := map[string]int{}
	state := ""
	for _, event := range events {
		run.StartedAt = cmp.Or(run.StartedAt, event.TS)
		run.LastEventAt = max(run.LastEventAt, event.TS)
		switch event.Kind {
		case "attempt":
			state = event.State
			if event.Trigger != "" {
				run.Trigger = event.Trigger
			}
			if event.StartedAt != 0 {
				run.StartedAt = min(run.StartedAt, event.StartedAt)
			}
			if event.State != "running" {
				run.FinishedAt = cmp.Or(event.FinishedAt, event.TS)
			}
		case "stage":
			i := slices.IndexFunc(views, func(view StageView) bool { return view.ID == event.Stage })
			if i < 0 {
				// A stage the job does not list still shows, after the listed ones.
				views = append(views, StageView{ID: event.Stage, Name: event.Stage, State: "pending"})
				i = len(views) - 1
			}
			view := &views[i]
			view.Updates++
			view.State = event.State
			if event.Progress != nil {
				view.Progress = event.Progress
			}
			view.StartedAt = cmp.Or(view.StartedAt, event.StartedAt)
			if event.State == "running" && view.StartedAt == 0 {
				view.StartedAt = event.TS
			}
			if event.State != "running" && event.State != "pending" {
				view.FinishedAt = cmp.Or(event.FinishedAt, event.TS)
			}
			if event.Error != "" || event.State == "success" {
				view.Error = event.Error
			}
			if event.Receipt != "" {
				view.Receipt = event.Receipt
			}
		case "partition":
			key := event.Region + "\x00" + fmt.Sprint(event.Hour)
			row := Partition{Region: event.Region, RegionName: cmp.Or(regionNames[event.Region], event.Region), Hour: event.Hour, HourLabel: HourLabel(event.Region, event.Hour),
				UniqueRecipients: event.UniqueRecipients, UniqueSenders: event.UniqueSenders, ReceivedRows: event.ReceivedRows, SentRows: event.SentRows,
				GzFiles: event.GzFiles, CompressedBytes: event.CompressedBytes, TargetDir: event.TargetDir, Verify: event.Verify, Upload: event.Upload, Receipt: event.Receipt, TS: event.TS}
			if at, ok := partitions[key]; ok {
				run.Partitions[at] = row
			} else {
				partitions[key] = len(run.Partitions)
				run.Partitions = append(run.Partitions, row)
			}
		}
	}
	slices.SortStableFunc(run.Partitions, func(a, b Partition) int { return cmp.Or(cmp.Compare(a.Region, b.Region), cmp.Compare(a.Hour, b.Hour)) })
	run.Stages = views
	run.Status, run.Summary = judge(run, state, now, maxDuration)
	return run
}

// judge decides a run's status. Only stage events count as evidence: an exit code or an open port never does.
func judge(run Attempt, state string, now time.Time, maxDuration time.Duration) (Status, string) {
	terminal := func(view StageView) bool { return view.State != "pending" && view.State != "running" }
	done := state == "finished" || state == "aborted" || !slices.ContainsFunc(run.Stages, func(view StageView) bool { return !terminal(view) })
	if !done {
		if now.UnixMilli()-run.LastEventAt > maxDuration.Milliseconds() {
			return Unknown, fmt.Sprintf("超过 %s没有新事件，也没有结束", hours(maxDuration))
		}
		for _, view := range run.Stages {
			if view.State == "running" {
				if view.Progress != nil {
					return Running, fmt.Sprintf("正在%s %d%%", view.Name, *view.Progress)
				}
				return Running, "正在" + view.Name
			}
		}
		return Running, "运行中"
	}
	var failed, critical, unknown []string
	receipts, receiptFailures := 0, 0
	for _, view := range run.Stages {
		if view.NeedsReceipt {
			receipts++
		}
		switch {
		case view.State == "failed":
			failed = append(failed, view.Name)
			if view.Critical {
				critical = append(critical, view.Name)
			}
			if view.NeedsReceipt {
				receiptFailures++
			}
		case view.State == "success" && view.NeedsReceipt && view.Receipt == "":
			unknown = append(unknown, view.Name+"没有回执")
		case view.State == "unknown":
			unknown = append(unknown, view.Name+"结果未知")
		case view.State == "pending" || view.State == "running":
			unknown = append(unknown, view.Name+"没有上报结果")
		}
	}
	for _, row := range run.Partitions {
		label := row.RegionName + " " + row.HourLabel
		switch {
		case row.Upload == "failed" || row.Verify == "failed":
			failed = append(failed, label)
		case row.Upload == "unknown" || row.Verify == "unknown":
			unknown = append(unknown, label+"结果未知")
		}
	}
	switch {
	case len(critical) > 0:
		return Failed, strings.Join(critical, "、") + "失败" + firstError(run.Stages, critical)
	case receipts > 0 && receiptFailures == receipts:
		return Failed, "上传全部失败" + firstError(run.Stages, failed)
	case state == "aborted":
		return Failed, "运行中止"
	case len(failed) > 0:
		return Partial, listed(failed) + "失败"
	case len(unknown) > 0:
		return Unknown, listed(unknown)
	}
	return Success, "全部阶段成功"
}

func firstError(views []StageView, names []string) string {
	for _, view := range views {
		if view.Error != "" && slices.Contains(names, view.Name) {
			return "：" + view.Error
		}
	}
	return ""
}

// listed names up to three items and counts the rest, e.g. "A、B、C 等 5 项".
func listed(items []string) string {
	if len(items) > 3 {
		return strings.Join(items[:3], "、") + fmt.Sprintf(" 等 %d 项", len(items))
	}
	return strings.Join(items, "、")
}
