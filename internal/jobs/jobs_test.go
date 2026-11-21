// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package jobs

import (
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

// 2026-11-18 02:00 in Beijing is 2026-11-17 18:00 UTC.
var dayStart = time.Date(2026, 11, 18, 2, 0, 0, 0, Beijing)

func at(minutes int) int64 { return dayStart.Add(time.Duration(minutes) * time.Minute).UnixMilli() }

var aics = config.Job{Schedule: config.JobSchedule{At: "02:00", Grace: "2h"}, MaxDuration: "6h"}

type script struct {
	attempt string
	events  []Recorded
	seq     int
}

func (s *script) add(e Event) *script {
	s.seq++
	e.EventID = s.attempt + "-" + string(rune('a'+s.seq))
	e.AttemptID = s.attempt
	s.events = append(s.events, Recorded{Event: e, BusinessDate: "2026-11-18", ReceivedAt: int64(s.seq)})
	return s
}

func (s *script) stage(minute int, stage, state, receipt string) *script {
	return s.add(Event{TS: at(minute), Kind: "stage", Stage: stage, State: state, Receipt: receipt})
}

// fullRun is a healthy AICS daily upload; mutate the returned events to break it.
func fullRun(attempt string, offset int) *script {
	s := &script{attempt: attempt}
	s.add(Event{TS: at(offset), Kind: "attempt", State: "running"})
	for i, stage := range []string{"fetch", "verify", "package", "backup"} {
		s.stage(offset+i*5, stage, "running", "").stage(offset+i*5+4, stage, "success", "")
	}
	s.stage(offset+20, "upload_cn", "success", "oss request_id=fake-cn-1")
	s.stage(offset+22, "upload_overseas", "success", "s3 etag=fake-etag-1")
	s.stage(offset+24, "manifest", "success", "")
	hour := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC).UnixMilli()
	one := int64(1)
	for _, region := range []string{"cn", "overseas"} {
		s.add(Event{TS: at(offset + 21), Kind: "partition", Region: region, Hour: hour, GzFiles: &one, Upload: "ok", Verify: "ok"})
	}
	return s.add(Event{TS: at(offset + 25), Kind: "attempt", State: "finished"})
}

func dayOf(t *testing.T, events []Recorded, now time.Time) Day {
	t.Helper()
	days := Days(aics, events, []string{"2026-11-18"}, now)
	if len(days) != 1 {
		t.Fatalf("days=%v", days)
	}
	return days[0]
}

func TestScenariosGiveTheRightDailyStatus(t *testing.T) {
	later := dayStart.Add(3 * time.Hour)
	for _, test := range []struct {
		name    string
		events  func() []Recorded
		now     time.Time
		status  Status
		summary string
	}{
		{"正常执行", func() []Recorded { return fullRun("a1", 0).events }, later, Success, "全部阶段成功"},
		{"中途失败", func() []Recorded {
			s := &script{attempt: "a1"}
			s.add(Event{TS: at(0), Kind: "attempt", State: "running"})
			s.stage(0, "fetch", "success", "").stage(5, "verify", "running", "")
			s.add(Event{TS: at(9), Kind: "stage", Stage: "verify", State: "failed", Error: "校验失败 user@example.test password=hunter2"})
			return s.add(Event{TS: at(10), Kind: "attempt", State: "aborted"}).events
		}, later, Failed, "中转校验失败"},
		{"单端上传失败", func() []Recorded {
			s := fullRun("a1", 0)
			s.stage(30, "upload_overseas", "failed", "")
			return s.events
		}, later, Partial, "海外 S3 上传失败"},
		{"两端上传都失败", func() []Recorded {
			s := fullRun("a1", 0)
			s.stage(30, "upload_overseas", "failed", "").stage(31, "upload_cn", "failed", "")
			return s.events
		}, later, Failed, "上传全部失败"},
		{"未知回执", func() []Recorded {
			s := fullRun("a1", 0)
			for i := range s.events {
				if s.events[i].Stage == "upload_cn" {
					s.events[i].Receipt = ""
				}
			}
			return s.events
		}, later, Unknown, "国内 OSS 上传没有回执"},
		{"上传结果未知", func() []Recorded { return fullRun("a1", 0).stage(40, "upload_cn", "unknown", "").events }, later, Unknown, "国内 OSS 上传结果未知"},
		{"漏跑", func() []Recorded { return nil }, dayStart.Add(2*time.Hour + time.Minute), Missed, "过了宽限 2 小时"},
		{"待运行", func() []Recorded { return nil }, dayStart.Add(-time.Hour), Pending, "计划 02:00"},
		{"运行中", func() []Recorded {
			s := &script{attempt: "a1"}
			progress := 45
			s.add(Event{TS: at(0), Kind: "attempt", State: "running"}).stage(1, "fetch", "success", "")
			return s.add(Event{TS: at(2), Kind: "stage", Stage: "verify", State: "running", Progress: &progress}).events
		}, dayStart.Add(10 * time.Minute), Running, "正在中转校验 45%"},
		{"长时间没消息", func() []Recorded {
			s := &script{attempt: "a1"}
			return s.add(Event{TS: at(0), Kind: "attempt", State: "running"}).stage(1, "fetch", "running", "").events
		}, dayStart.Add(7 * time.Hour), Unknown, "超过 6 小时没有新事件"},
		{"手动补跑", func() []Recorded {
			first := fullRun("a1", 0)
			first.stage(30, "upload_overseas", "failed", "")
			second := fullRun("a2", 120)
			second.events[0].Trigger = "manual"
			return append(first.events, second.events...)
		}, dayStart.Add(5 * time.Hour), Success, "共 2 次运行"},
	} {
		t.Run(test.name, func(t *testing.T) {
			day := dayOf(t, test.events(), test.now)
			if day.Status != test.status || !strings.Contains(day.Summary, test.summary) {
				t.Fatalf("status=%s summary=%q, want %s containing %q", day.Status, day.Summary, test.status, test.summary)
			}
		})
	}
}

func TestManualRerunsKeepEveryRunNewestFirst(t *testing.T) {
	first := fullRun("a1", 0)
	first.stage(30, "upload_overseas", "failed", "")
	second := fullRun("a2", 120)
	day := dayOf(t, append(first.events, second.events...), dayStart.Add(5*time.Hour))
	if len(day.Attempts) != 2 || day.Attempts[0].ID != "a2" || day.Attempts[0].Trigger != "manual" || day.Attempts[1].Status != Partial {
		t.Fatalf("attempts=%+v", day.Attempts)
	}
	// Each run keeps its own partitions: a rerun appends files rather than replacing them.
	if len(day.Attempts[0].Partitions) != 2 || len(day.Attempts[1].Partitions) != 2 {
		t.Fatalf("partitions=%d/%d", len(day.Attempts[0].Partitions), len(day.Attempts[1].Partitions))
	}
}

func TestStagesUpdateIndependentlyAndKeepTheirTimes(t *testing.T) {
	run := dayOf(t, fullRun("a1", 0).events, dayStart.Add(time.Hour)).Attempts[0]
	verify := run.Stages[1]
	if verify.ID != "verify" || verify.State != "success" || verify.StartedAt != at(5) || verify.FinishedAt != at(9) || verify.Updates != 2 || !verify.Critical {
		t.Fatalf("verify=%+v", verify)
	}
	if upload := run.Stages[4]; !upload.NeedsReceipt || upload.Receipt == "" {
		t.Fatalf("upload=%+v", upload)
	}
}

func TestDisabledDaysAreNeverMissed(t *testing.T) {
	job := aics
	job.Schedule.From, job.Schedule.Skip, job.Schedule.Days = "2026-11-10", []string{"2026-11-16"}, []string{"mon", "tue", "wed", "thu", "fri"}
	days := Days(job, nil, []string{"2026-11-18", "2026-11-16", "2026-11-15", "2026-11-09"}, dayStart.Add(30*24*time.Hour))
	got := []Status{}
	for _, day := range days {
		got = append(got, day.Status)
	}
	// Wednesday is missed; the skipped Monday, a Sunday and a date before "from" are not.
	if strings.Join([]string{string(got[0]), string(got[1]), string(got[2]), string(got[3])}, ",") != "missed,disabled,disabled,disabled" {
		t.Fatalf("statuses=%v", got)
	}
}

func TestPartitionHoursAreNamedByTheirRegionsClock(t *testing.T) {
	hour := time.Date(2026, 12, 31, 18, 0, 0, 0, time.UTC).UnixMilli()
	if got := HourLabel("cn", hour); got != "2027/01/01 02 时（UTC+8）" {
		t.Fatalf("cn=%q", got)
	}
	if got := HourLabel("overseas", hour); got != "2026/12/31 18 时（UTC+0）" {
		t.Fatalf("overseas=%q", got)
	}
}

func TestEventsCarryOnlyStatesCountsSummariesAndReceipts(t *testing.T) {
	good := `{"event_id":"e1","attempt_id":"a1","ts":1794940000000,"kind":"stage","stage":"fetch","state":"failed","error":"连接 db.prod 失败 user=ops password=hunter2 发给 alice@example.test token=abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH AKIAFAKEFAKEFAKE1234 ` + strings.Repeat("长", 600) + `"}`
	event, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2", "alice@", "abcdefghijklmnopqrstuvwxyz0123456789", "FAKEFAKEFAKE1234"} {
		if strings.Contains(event.Error, secret) {
			t.Fatalf("%q survived: %q", secret, event.Error)
		}
	}
	if !strings.Contains(event.Error, "a***@example.test") || len([]rune(event.Error)) > 500 {
		t.Fatalf("error=%q", event.Error)
	}
	for body, want := range map[string]string{
		`{"event_id":"e1","attempt_id":"a1","ts":1794940000000,"kind":"stage","stage":"fetch","state":"success","body":"邮件正文"}`: "约定字段",
		`{"event_id":"e1","attempt_id":"a1","ts":1794940000,"kind":"stage","stage":"fetch","state":"success"}`:                  "毫秒",
		`{"event_id":"","attempt_id":"a1","ts":1794940000000,"kind":"stage","stage":"fetch","state":"success"}`:                 "event_id",
		`{"event_id":"e1","attempt_id":"a1","ts":1794940000000,"kind":"partition","region":"cn","hour":1794940000001}`:          "整点",
		`{"event_id":"e1","attempt_id":"a1","ts":1794940000000,"kind":"stage","stage":"fetch","state":"done"}`:                  "state",
		`{"event_id":"e1","attempt_id":"a1","ts":1794940000000,"kind":"report"}`:                                                "kind",
	} {
		if _, err := Parse([]byte(body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", body, err)
		}
	}
	if DateOf(time.Date(2026, 11, 17, 16, 30, 0, 0, time.UTC).UnixMilli()) != "2026-11-18" {
		t.Fatal("business dates are not Beijing dates")
	}
}
