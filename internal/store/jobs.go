package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
