// Package jobs turns a batch job's business events into daily records: which runs happened on a business day,
// how each stage went, the data each hourly partition carried, and one status for the day.
package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Beijing is the business calendar: a business date is a day in UTC+8, whatever the machine's zone.
var Beijing = time.FixedZone("UTC+8", 8*60*60)

// Event is one thing a job reports. The fields are fixed and anything else is refused, so a mail body, a database
// password or a cloud key cannot ride along: only states, counts, summaries and receipts are kept.
type Event struct {
	EventID      string `json:"event_id"`
	JobID        string `json:"job_id,omitempty"`
	AttemptID    string `json:"attempt_id"`
	BusinessDate string `json:"business_date,omitempty"`
	// TS is when it happened, in real UTC milliseconds.
	TS   int64  `json:"ts"`
	Kind string `json:"kind"`
	// attempt: trigger and state (running, finished, aborted).
	Trigger string `json:"trigger,omitempty"`
	State   string `json:"state,omitempty"`
	// stage: which stage, its state, progress and times.
	Stage      string `json:"stage,omitempty"`
	Progress   *int   `json:"progress,omitempty"`
	StartedAt  int64  `json:"started_at,omitempty"`
	FinishedAt int64  `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
	Receipt    string `json:"receipt,omitempty"`
	// partition: one region's one mail hour, with its counts and where it went.
	Region           string `json:"region,omitempty"`
	Hour             int64  `json:"hour,omitempty"`
	UniqueRecipients *int64 `json:"unique_recipients,omitempty"`
	UniqueSenders    *int64 `json:"unique_senders,omitempty"`
	ReceivedRows     *int64 `json:"received_rows,omitempty"`
	SentRows         *int64 `json:"sent_rows,omitempty"`
	GzFiles          *int64 `json:"gz_files,omitempty"`
	CompressedBytes  *int64 `json:"compressed_bytes,omitempty"`
	TargetDir        string `json:"target_dir,omitempty"`
	Verify           string `json:"verify,omitempty"`
	Upload           string `json:"upload,omitempty"`
}

var (
	eventID    = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	attemptID  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	stageID    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	regionID   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	stageState = []string{"pending", "running", "success", "failed", "unknown", "skipped"}
	outcome    = []string{"ok", "failed", "unknown"}
)

// Earliest and latest timestamps accepted: a millisecond count outside them is a unit mistake (seconds, say).
const (
	minTS = int64(1_600_000_000_000)
	maxTS = int64(4_000_000_000_000)
)

// Parse reads one event, refusing unknown fields, and returns it checked and redacted.
func Parse(data []byte) (Event, error) {
	var event Event
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return Event{}, fmt.Errorf("事件必须是只含约定字段的 JSON 对象（不接收邮件正文、密码、密钥等其他字段）：%w", err)
	}
	if err := event.Check(); err != nil {
		return Event{}, err
	}
	event.Redact()
	return event, nil
}

func one(value string, choices []string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func validTime(ms int64) bool { return ms == 0 || ms >= minTS && ms <= maxTS }

// Check reports the first thing wrong with an event, in words a reporter can act on.
func (e Event) Check() error {
	switch {
	case !eventID.MatchString(e.EventID):
		return fmt.Errorf("event_id 必须是 1～128 个字母、数字或 . _ : -，并且同一任务内稳定唯一")
	case !attemptID.MatchString(e.AttemptID):
		return fmt.Errorf("attempt_id 必须是 1～64 个字母、数字或 . _ : -；手动补跑用新的 attempt_id")
	case e.TS < minTS || e.TS > maxTS:
		return fmt.Errorf("ts 必须是事件发生时的 UTC 毫秒时间戳")
	case !validTime(e.StartedAt) || !validTime(e.FinishedAt):
		return fmt.Errorf("started_at、finished_at 必须是 UTC 毫秒时间戳")
	case e.StartedAt != 0 && e.FinishedAt != 0 && e.FinishedAt < e.StartedAt:
		return fmt.Errorf("finished_at 不能早于 started_at")
	}
	if e.BusinessDate != "" {
		if _, err := time.Parse(time.DateOnly, e.BusinessDate); err != nil {
			return fmt.Errorf("business_date 必须是北京时间的 YYYY-MM-DD")
		}
	}
	switch e.Kind {
	case "attempt":
		if !one(e.State, []string{"running", "finished", "aborted"}) {
			return fmt.Errorf("attempt 事件的 state 必须是 running、finished 或 aborted")
		}
		if e.Trigger != "" && !one(e.Trigger, []string{"scheduled", "manual"}) {
			return fmt.Errorf("trigger 只能是 scheduled 或 manual")
		}
	case "stage":
		if !stageID.MatchString(e.Stage) {
			return fmt.Errorf("stage 必须是阶段 ID，例如 fetch、upload_cn")
		}
		if !one(e.State, stageState) {
			return fmt.Errorf("stage 事件的 state 必须是 pending、running、success、failed、unknown 或 skipped")
		}
		if e.Progress != nil && (*e.Progress < 0 || *e.Progress > 100) {
			return fmt.Errorf("progress 必须是 0～100")
		}
	case "partition":
		if !regionID.MatchString(e.Region) {
			return fmt.Errorf("partition 事件的 region 必须是区域 ID，例如 cn、overseas")
		}
		if e.Hour < minTS || e.Hour > maxTS || e.Hour%int64(time.Hour/time.Millisecond) != 0 {
			return fmt.Errorf("hour 必须是邮件发生时间所在小时开始的 UTC 毫秒时间戳（整点，不做时区偏移）")
		}
		for _, count := range []*int64{e.UniqueRecipients, e.UniqueSenders, e.ReceivedRows, e.SentRows, e.GzFiles, e.CompressedBytes} {
			if count != nil && *count < 0 {
				return fmt.Errorf("数量不能是负数")
			}
		}
		if e.Verify != "" && !one(e.Verify, outcome) || e.Upload != "" && !one(e.Upload, outcome) {
			return fmt.Errorf("verify、upload 只能是 ok、failed 或 unknown")
		}
	default:
		return fmt.Errorf("kind 必须是 attempt、stage 或 partition")
	}
	return nil
}

var (
	email     = regexp.MustCompile(`([A-Za-z0-9._%+-])[A-Za-z0-9._%+-]*@([A-Za-z0-9.-]+\.[A-Za-z]{2,})`)
	assigned  = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|access[_-]?key(?:[_-]?id)?|secret[_-]?key|authorization|api[_-]?key)(\s*[:=]\s*)("[^"]*"|'[^']*'|\S+)`)
	cloudKeys = regexp.MustCompile(`\b(AKIA|ASIA|LTAI)[0-9A-Za-z]{8,}`)
	longToken = regexp.MustCompile(`[A-Za-z0-9+/=_-]{40,}`)
)

// redact masks what must never be stored: addresses, assigned secrets and cloud key IDs; tokens lose their
// middle. Control characters go and the text is cut to limit runes.
func redact(text string, limit int, tokens bool) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(text))
	text = email.ReplaceAllString(text, "$1***@$2")
	text = assigned.ReplaceAllString(text, "$1$2***")
	text = cloudKeys.ReplaceAllString(text, "$1***")
	if tokens {
		text = longToken.ReplaceAllStringFunc(text, func(value string) string { return value[:4] + "…" })
	}
	if utf8.RuneCountInString(text) > limit {
		text = string([]rune(text)[:limit-1]) + "…"
	}
	return text
}

// Redact cleans the free-text fields in place.
func (e *Event) Redact() {
	e.Error = redact(e.Error, 500, true)
	e.Receipt = redact(e.Receipt, 200, false)
	e.TargetDir = redact(e.TargetDir, 300, false)
}

// DateOf is the Beijing business date of a moment.
func DateOf(ms int64) string { return time.UnixMilli(ms).In(Beijing).Format(time.DateOnly) }
