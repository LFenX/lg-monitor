package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// A batch job reports with a key of its own that can do nothing else: it is not an API token, so handing it
// to the job's environment does not give the job any read or configuration access.

func (s *AuthDB) initJobKeys(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS job_keys(job_id TEXT PRIMARY KEY,key_hash BLOB NOT NULL UNIQUE CHECK(typeof(key_hash)='blob' AND length(key_hash)=32),created_at INTEGER NOT NULL)`)
	if err != nil {
		return fmt.Errorf("初始化任务上报密钥失败：%w", err)
	}
	return nil
}

// SetJobKey replaces a job's report key; the old one stops working at once.
func (s *AuthDB) SetJobKey(ctx context.Context, jobID string, hash []byte) error {
	if jobID == "" || len(hash) != 32 {
		return fmt.Errorf("任务上报密钥需要任务 ID 和 32 字节摘要")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_keys(job_id,key_hash,created_at) VALUES(?,?,?) ON CONFLICT(job_id) DO UPDATE SET key_hash=excluded.key_hash,created_at=excluded.created_at`, jobID, hash, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("保存任务上报密钥失败：%w", err)
	}
	return nil
}

// JobForKey finds the job a report key belongs to.
func (s *AuthDB) JobForKey(ctx context.Context, hash [32]byte) (string, bool, error) {
	var jobID string
	err := s.db.QueryRowContext(ctx, `SELECT job_id FROM job_keys WHERE key_hash=?`, hash[:]).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("核对任务上报密钥失败：%w", err)
	}
	return jobID, true, nil
}
