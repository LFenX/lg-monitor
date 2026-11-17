package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/jobs"
)

// JobEventRow is one checked batch job event ready to store. BusinessDate may be empty: the run's existing date is
// used, else Fallback (the Beijing date of the event), so a run that crosses midnight stays on one day.
type JobEventRow struct {
	JobID, EventID, AttemptID string
	BusinessDate, Fallback    string
	TS                        int64
	Source                    string
	Body                      []byte
}

// JobAppendResult counts what a report did: new events, exact repeats, and repeats of an ID with other content.
type JobAppendResult struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Conflicts  int `json:"conflicts" jsonschema_description:"同一个 event_id 内容却不同：保留第一次收到的。"`
}

// StoredJobEvent is a job event as kept.
type StoredJobEvent struct {
	EventID, AttemptID, BusinessDate, Source string
	TS, ReceivedAt                           int64
	Body                                     []byte
}

// AppendJobEvents stores events once each; a repeated event ID changes nothing, so reporters can resend freely.
func (s *Store) AppendJobEvents(ctx context.Context, rows []JobEventRow) (JobAppendResult, error) {
	var result JobAppendResult
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result = JobAppendResult{}
		return appendJobEvents(ctx, tx, rows, s.now().UnixMilli(), &result)
	})
	return result, err
}

func appendJobEvents(ctx context.Context, tx *sql.Tx, rows []JobEventRow, now int64, result *JobAppendResult) error {
	for _, row := range rows {
		var existing []byte
		err := tx.QueryRowContext(ctx, `SELECT body FROM job_events WHERE job_id=? AND event_id=?`, row.JobID, row.EventID).Scan(&existing)
		if err == nil {
			if string(existing) == string(row.Body) {
				result.Duplicates++
			} else {
				result.Conflicts++
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("检查任务事件是否重复失败：%w", err)
		}
		date := row.BusinessDate
		if date == "" {
			err := tx.QueryRowContext(ctx, `SELECT business_date FROM job_events WHERE job_id=? AND attempt_id=? ORDER BY ts LIMIT 1`, row.JobID, row.AttemptID).Scan(&date)
			if errors.Is(err, sql.ErrNoRows) {
				date = row.Fallback
			} else if err != nil {
				return fmt.Errorf("读取这次运行的业务日期失败：%w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_id,business_date,attempt_id,ts,received_at,source,body) VALUES(?,?,?,?,?,?,?,?)`,
			row.JobID, row.EventID, date, row.AttemptID, row.TS, now, row.Source, string(row.Body)); err != nil {
			return fmt.Errorf("保存任务事件失败：%w", err)
		}
		result.Accepted++
	}
	return nil
}

// JobEvents reads a job's events for business dates from..to (inclusive, YYYY-MM-DD).
func (v *View) JobEvents(ctx context.Context, jobID, from, to string) ([]StoredJobEvent, error) {
	rows, err := v.db.QueryContext(ctx, `SELECT event_id,attempt_id,business_date,source,ts,received_at,body FROM job_events WHERE job_id=? AND business_date BETWEEN ? AND ? ORDER BY ts,received_at`, jobID, from, to)
	if err != nil {
		return nil, fmt.Errorf("读取任务事件失败：%w", err)
	}
	defer rows.Close()
	result := []StoredJobEvent{}
	for rows.Next() {
		var item StoredJobEvent
		var body string
		if err := rows.Scan(&item.EventID, &item.AttemptID, &item.BusinessDate, &item.Source, &item.TS, &item.ReceivedAt, &body); err != nil {
			return nil, fmt.Errorf("读取任务事件失败：%w", err)
		}
		item.Body = []byte(body)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取任务事件失败：%w", err)
	}
	return result, nil
}

// JobSource is how a job's file is being read, as its collector last reported.
type JobSource struct {
	CollectorID string `json:"collectorId"`
	Path        string `json:"path"`
	State       string `json:"state" jsonschema:"enum=ok,enum=error"`
	Problem     string `json:"problem,omitempty"`
	ProblemLine int64  `json:"problemLine,omitempty"`
	ProblemAt   int64  `json:"problemAt,omitempty"`
	UpdatedAt   int64  `json:"updatedAt"`
	LastEventAt int64  `json:"lastEventAt,omitempty"`
}

// appendJobFileRecord stores one line read from a job's file, checked again here, and what it says about the file.
func appendJobFileRecord(ctx context.Context, tx *sql.Tx, row record, receivedAt int64) error {
	file := row.job
	if file == nil {
		return fmt.Errorf("任务事件记录缺少内容")
	}
	var source JobSource
	err := tx.QueryRowContext(ctx, `SELECT state,problem,problem_line,problem_at,last_event_at FROM job_sources WHERE job_id=?`, file.JobID).Scan(&source.State, &source.Problem, &source.ProblemLine, &source.ProblemAt, &source.LastEventAt)
	if errors.Is(err, sql.ErrNoRows) {
		source.State = "ok"
	} else if err != nil {
		return fmt.Errorf("读取任务事件文件状态失败：%w", err)
	}
	problem := file.Error
	if len(file.Event) > 0 {
		event, err := jobs.Parse(file.Event)
		if err != nil {
			problem = fmt.Sprintf("第 %d 行：%s", file.Line, err)
		} else {
			event.JobID = ""
			body, err := json.Marshal(event)
			if err != nil {
				return fmt.Errorf("编码任务事件失败：%w", err)
			}
			var result JobAppendResult
			if err := appendJobEvents(ctx, tx, []JobEventRow{{JobID: file.JobID, EventID: event.EventID, AttemptID: event.AttemptID, BusinessDate: event.BusinessDate, Fallback: jobs.DateOf(event.TS), TS: event.TS, Source: "collector:" + row.collector, Body: body}}, receivedAt, &result); err != nil {
				return err
			}
			if result.Accepted > 0 {
				source.LastEventAt = receivedAt
			}
		}
	}
	switch {
	case problem != "":
		source.Problem, source.ProblemLine, source.ProblemAt = problem, int64(file.Line), receivedAt
		if file.Line == 0 {
			source.State = "error"
		}
	case file.Line == 0 && len(file.Event) == 0:
		source.State = "ok"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_sources(job_id,collector_id,path,state,problem,problem_line,problem_at,updated_at,last_event_at) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(job_id) DO UPDATE SET collector_id=excluded.collector_id,path=excluded.path,state=excluded.state,problem=excluded.problem,problem_line=excluded.problem_line,problem_at=excluded.problem_at,updated_at=excluded.updated_at,last_event_at=excluded.last_event_at`,
		file.JobID, row.collector, file.Path, source.State, source.Problem, source.ProblemLine, source.ProblemAt, receivedAt, source.LastEventAt)
	if err != nil {
		return fmt.Errorf("保存任务事件文件状态失败：%w", err)
	}
	return nil
}

// JobSource reads how a job's file is being read; found is false before its collector first reported.
func (v *View) JobSource(ctx context.Context, jobID string) (JobSource, bool, error) {
	var source JobSource
	err := v.db.QueryRowContext(ctx, `SELECT collector_id,path,state,problem,problem_line,problem_at,updated_at,last_event_at FROM job_sources WHERE job_id=?`, jobID).Scan(&source.CollectorID, &source.Path, &source.State, &source.Problem, &source.ProblemLine, &source.ProblemAt, &source.UpdatedAt, &source.LastEventAt)
	if errors.Is(err, sql.ErrNoRows) {
		return source, false, nil
	}
	if err != nil {
		return source, false, fmt.Errorf("读取任务事件文件状态失败：%w", err)
	}
	return source, true, nil
}
