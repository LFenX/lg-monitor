package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/jobs"
)

// 2026-11-18 10:00 in Beijing: the 02:00 run's two-hour grace is over.
var jobNow = time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)

const jobTargets = "targets: [{id: web, name: 生产通路, match: {process: fake.exe}}, {id: aics-upload, name: AICS 每日上传, job: {schedule: {at: '02:00'}, production: [web]}}]\n"

func newJobFixture(t *testing.T) *apiFixture {
	t.Helper()
	f := newAPIFixtureAt(t, jobNow)
	f.files.MapFS["targets.yaml"] = &fstest.MapFile{Data: []byte(jobTargets)}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	return f
}

func jobEvents(events ...string) string {
	return `{"events":[` + strings.Join(events, ",") + `]}`
}

func jobTS(clock string) int64 {
	moment, err := time.ParseInLocation("2006-01-02 15:04", clock, jobs.Beijing)
	if err != nil {
		panic(err)
	}
	return moment.UnixMilli()
}

func jobStage(attempt, stage, state, clock, extra string) string {
	return fmt.Sprintf(`{"event_id":"%s-%s-%s","attempt_id":"%s","kind":"stage","stage":"%s","state":"%s","ts":%d%s}`, attempt, stage, state, attempt, stage, state, jobTS(clock), extra)
}

func jobAttempt(attempt, state, clock, trigger string) string {
	return fmt.Sprintf(`{"event_id":"%s-%s","attempt_id":"%s","kind":"attempt","state":"%s","trigger":"%s","business_date":"2026-11-18","ts":%d}`, attempt, state, attempt, state, trigger, jobTS(clock))
}

func successfulRun(attempt, trigger, hour string) []string {
	at := func(minute string) string { return "2026-11-18 " + hour + ":" + minute }
	events := []string{jobAttempt(attempt, "running", at("00"), trigger)}
	for _, stage := range []string{"fetch", "verify", "package", "backup", "manifest"} {
		events = append(events, jobStage(attempt, stage, "success", at("10"), ""))
	}
	for _, stage := range []string{"upload_cn", "upload_overseas"} {
		events = append(events, jobStage(attempt, stage, "success", at("20"), `,"receipt":"etag-fake-`+stage+`"`))
	}
	return append(events, jobAttempt(attempt, "finished", at("30"), trigger))
}

func TestJobReportAPIStoresEventsOnceAndJudgesTheDay(t *testing.T) {
	f := newJobFixture(t)
	days := decodeJobDays(t, f.request(t, "GET", "/api/v1/jobs/aics-upload/days?days=3", "read", "", 200))
	if days.Days[0].Date != "2026-11-18" || days.Days[0].Status != jobs.Missed || days.Days[2].Date != "2026-11-16" {
		t.Fatalf("days before any run = %+v", days.Days)
	}
	if days.Job.Schedule != "每天 02:00（北京时间）" || len(days.Job.Stages) != 7 || days.Job.Production[0] != "web" {
		t.Fatalf("job info = %+v", days.Job)
	}
	body := jobEvents(successfulRun("run-1", "scheduled", "02")...)
	result := decodeJobReport(t, f.request(t, "POST", "/api/v1/jobs/aics-upload/events", "config", body, 200))
	if result.Accepted != 9 || result.Duplicates != 0 || len(result.Rejected) != 0 {
		t.Fatalf("first report = %+v", result)
	}
	// A reporter retrying after a lost response must not count anything twice.
	result = decodeJobReport(t, f.request(t, "POST", "/api/v1/jobs/aics-upload/events", "config", body, 200))
	if result.Accepted != 0 || result.Duplicates != 9 {
		t.Fatalf("repeated report = %+v", result)
	}
	days = decodeJobDays(t, f.request(t, "GET", "/api/v1/jobs/aics-upload/days?days=2", "read", "", 200))
	if days.Days[0].Status != jobs.Success || len(days.Days[0].Attempts) != 1 {
		t.Fatalf("day after a full run = %+v", days.Days[0])
	}
	monitors := f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.String()
	if !strings.Contains(monitors, `"kind":"job"`) || !strings.Contains(monitors, `"status":"success"`) {
		t.Fatalf("monitor list does not show the job: %s", monitors)
	}
}

func TestJobReportRejectsForbiddenFieldsOneByOne(t *testing.T) {
	f := newJobFixture(t)
	good := jobStage("run-1", "fetch", "failed", "2026-11-18 02:05", `,"error":"connect user=ops password=hunter2 to db failed for alice@example.test"`)
	body := jobEvents(good, `{"event_id":"bad","attempt_id":"run-1","kind":"stage","stage":"fetch","state":"success","ts":1,"mail_body":"hello"}`, `{"event_id":"other","attempt_id":"run-1","kind":"stage","stage":"fetch","state":"success","ts":1763431200000,"job_id":"someone-else"}`)
	result := decodeJobReport(t, f.request(t, "POST", "/api/v1/jobs/aics-upload/events", "config", body, 200))
	if result.Accepted != 1 || len(result.Rejected) != 2 || result.Rejected[0].Index != 1 || result.Rejected[1].Index != 2 || !strings.Contains(result.Rejected[1].Message, "someone-else") {
		t.Fatalf("report = %+v", result)
	}
	detail := f.request(t, "GET", "/api/v1/jobs/aics-upload/days/2026-11-18", "read", "", 200).Body.String()
	if strings.Contains(detail, "hunter2") || strings.Contains(detail, "alice@") || !strings.Contains(detail, "a***@example.test") {
		t.Fatalf("detail keeps a secret: %s", detail)
	}
	f.request(t, "POST", "/api/v1/jobs/aics-upload/events", "config", jobEvents(`{"event_id":"x"}`), 400)
	f.request(t, "POST", "/api/v1/jobs/web/events", "config", jobEvents(good), 400)
	f.request(t, "POST", "/api/v1/jobs/missing/events", "config", jobEvents(good), 404)
	f.request(t, "POST", "/api/v1/jobs/aics-upload/events", "read", jobEvents(good), 403)
}

func TestJobDayDetailSeparatesDependencyStateFromFetchResult(t *testing.T) {
	f := newJobFixture(t)
	events := []string{jobAttempt("run-1", "running", "2026-11-18 02:00", "scheduled"), jobStage("run-1", "fetch", "failed", "2026-11-18 02:05", `,"error":"timeout"`), jobAttempt("run-1", "finished", "2026-11-18 02:06", "scheduled")}
	events = append(events, successfulRun("rerun-1", "manual", "08")...)
	f.request(t, "POST", "/api/v1/jobs/aics-upload/events", "config", jobEvents(events...), 200)
	var detail api.JobDayDetail
	if err := json.Unmarshal(f.request(t, "GET", "/api/v1/jobs/aics-upload/days/2026-11-18", "read", "", 200).Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Day.Attempts) != 2 || detail.Day.Attempts[0].ID != "rerun-1" || detail.Day.Attempts[0].Trigger != "manual" || detail.Day.Attempts[1].Status != jobs.Failed {
		t.Fatalf("attempts = %+v", detail.Day.Attempts)
	}
	if detail.Day.Status != jobs.Success || detail.Fetch == nil || detail.Fetch.State != "success" {
		t.Fatalf("day = %+v fetch = %+v", detail.Day, detail.Fetch)
	}
	if len(detail.Production) != 1 || detail.Production[0].ID != "web" || detail.Production[0].Name != "生产通路" || detail.Production[0].State == "" {
		t.Fatalf("production = %+v", detail.Production)
	}
	f.request(t, "GET", "/api/v1/jobs/aics-upload/days/2026-13-01", "read", "", 400)
	f.request(t, "GET", "/api/v1/jobs/aics-upload/days?days=91", "read", "", 400)
}

func TestJobKeyReportsOnlyForItsOwnJob(t *testing.T) {
	f := newJobFixture(t)
	f.files.MapFS["targets.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(jobTargets, "]\n", ", {id: other-job, job: {}}]\n", 1))}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	key := api.JobKeyPrefix + "FAKE_JOB_KEY_FOR_TESTS_ONLY"
	digest := api.TokenHash(key)
	if err := f.authDB.SetJobKey(context.Background(), "aics-upload", digest[:]); err != nil {
		t.Fatal(err)
	}
	send := func(method, path, bearer, body string) int {
		r := httptest.NewRequest(method, "http://127.0.0.1:7444"+path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.5:1234"
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		return w.Code
	}
	body := jobEvents(successfulRun("run-1", "scheduled", "02")[0])
	if code := send("POST", "/api/v1/jobs/aics-upload/events", key, body); code != 200 {
		t.Fatalf("own job report = %d", code)
	}
	for _, check := range []struct{ method, path, bearer string }{
		{"POST", "/api/v1/jobs/other-job/events", key},
		{"GET", "/api/v1/jobs/aics-upload/days", key},
		{"GET", "/api/v1/monitors", key},
		{"POST", "/api/v1/jobs/aics-upload/events", api.JobKeyPrefix + "WRONG"},
	} {
		if code := send(check.method, check.path, check.bearer, body); code != 401 {
			t.Fatalf("%s %s with a job key = %d, want 401", check.method, check.path, code)
		}
	}
	replaced := api.TokenHash(api.JobKeyPrefix + "REPLACED")
	if err := f.authDB.SetJobKey(context.Background(), "aics-upload", replaced[:]); err != nil {
		t.Fatal(err)
	}
	if code := send("POST", "/api/v1/jobs/aics-upload/events", key, body); code != 401 {
		t.Fatalf("old key after replacement = %d", code)
	}
}

func decodeJobDays(t *testing.T, w *httptest.ResponseRecorder) api.JobDays {
	t.Helper()
	var days api.JobDays
	if err := json.Unmarshal(w.Body.Bytes(), &days); err != nil {
		t.Fatal(err)
	}
	return days
}

func decodeJobReport(t *testing.T, w *httptest.ResponseRecorder) api.JobReportResult {
	t.Helper()
	var result api.JobReportResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
