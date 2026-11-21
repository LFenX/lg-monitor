// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// checkpointInterval is how often the write-ahead log is copied back into the database file.
//
// SQLite does this by itself at the end of a commit once the log passes 1000 pages, and the commit does not
// return until the copy and its disk flush are done. On a spinning disk that took ten seconds and more, and the
// writer held its only connection the whole time, so sample batches, maintenance and anything else waiting on that
// connection stalled with it. The writer now never checkpoints; this loop does it on a connection of its own, in
// PASSIVE mode, which neither waits for nor blocks readers and the writer.
const checkpointInterval = 15 * time.Second

func openCheckpointer(ctx context.Context, uri string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("打开数据库检查点连接失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库检查点失败：%w", err)
	}
	return db, nil
}

// walTruncatePages is the size (~32 MiB at 4 KiB pages) at which a clean passive checkpoint also
// truncates the WAL file so an idle disk never stays pinned to a multi-hundred-megabyte log.
const walTruncatePages = 8192

// checkpointLoop keeps a wall-clock ticker of its own: it is housekeeping, not something tests step through.
func (s *Store) checkpointLoop(ticker *time.Ticker) {
	defer close(s.checkpointDone)
	defer ticker.Stop()
	for {
		select {
		case <-s.checkpointStop:
			return
		case <-ticker.C:
			busy, logPages, copied, err := s.checkpointStats(context.Background(), "PASSIVE")
			if err != nil {
				s.logger.Warn("Database checkpoint failed; retrying later", "error", err)
				continue
			}
			if logPages >= walTruncatePages {
				if busy == 0 && copied == logPages {
					if _, _, _, err := s.checkpointStats(context.Background(), "TRUNCATE"); err != nil {
						s.logger.Warn("Database WAL truncation failed; retrying later", "logPages", logPages, "error", err)
					}
				} else if logPages >= walTruncatePages*2 {
					s.logger.Warn("Database WAL is large because active readers prevented a full passive checkpoint", "logPages", logPages, "copied", copied, "busy", busy)
				}
			}
		}
	}
}

// Checkpoint copies committed pages from the write-ahead log into the database file. PASSIVE never waits; TRUNCATE
// (used when closing) waits for readers and empties the log.
func (s *Store) Checkpoint(ctx context.Context, mode string) error {
	_, _, _, err := s.checkpointStats(ctx, mode)
	return err
}

func (s *Store) checkpointStats(ctx context.Context, mode string) (busy, logPages, copied int, err error) {
	if mode != "PASSIVE" && mode != "TRUNCATE" {
		return 0, 0, 0, fmt.Errorf("不支持的检查点模式 %s", mode)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := s.checkpointer.QueryRowContext(ctx, "PRAGMA wal_checkpoint("+mode+")").Scan(&busy, &logPages, &copied); err != nil {
		return 0, 0, 0, fmt.Errorf("执行数据库检查点失败：%w", err)
	}
	return busy, logPages, copied, nil
}
