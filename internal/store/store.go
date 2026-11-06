package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	_ "modernc.org/sqlite"
)

type Writer interface {
	pipeline.Sink
	Flush(context.Context) error
}
type Reader interface {
	CurrentTargets(context.Context) ([]TargetRecord, error)
}

type Ticker interface {
	Ticks() <-chan time.Time
	Stop()
}
type Clock interface{ NewTicker(time.Duration) Ticker }
type nativeClock struct{}
type nativeTicker struct{ *time.Ticker }

func (nativeClock) NewTicker(interval time.Duration) Ticker {
	return nativeTicker{time.NewTicker(interval)}
}
func (t nativeTicker) Ticks() <-chan time.Time { return t.C }

type Options struct {
	Path  string
	Now   func() time.Time
	Clock Clock
}

type request struct {
	record *record
	action func(context.Context, *sql.Tx) error
	done   chan error
}

type Store struct {
	db             *sql.DB
	now            func() time.Time
	clock          Clock
	requests       chan request
	stopping, done chan struct{}
	mu             sync.RWMutex
	once           sync.Once
	failureMu      sync.Mutex
	failure        error
	closeErr       error
}

func Open(ctx context.Context, options Options) (*Store, error) {
	if options.Path == "" {
		return nil, fmt.Errorf("数据库路径不能为空")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Clock == nil {
		options.Clock = nativeClock{}
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败：%w", err)
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"_pragma": {"busy_timeout(5000)", "foreign_keys(1)", "journal_mode(WAL)"}, "_txlock": {"immediate"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("打开监控数据库失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接监控数据库失败：%w", err)
	}
	if err := migrate(ctx, db, options.Now); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, now: options.Now, clock: options.Clock, requests: make(chan request, 2048), stopping: make(chan struct{}), done: make(chan struct{})}
	go s.writeLoop()
	return s, nil
}

func (s *Store) Err() error     { s.failureMu.Lock(); defer s.failureMu.Unlock(); return s.failure }
func (s *Store) fail(err error) { s.failureMu.Lock(); s.failure = err; s.failureMu.Unlock() }

func (s *Store) stoppedError() error {
	if err := s.Err(); err != nil {
		return err
	}
	return fmt.Errorf("监控存储已关闭：%w", io.ErrClosedPipe)
}

func (s *Store) submit(ctx context.Context, req request) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("数据库写入已取消：%w", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	select {
	case <-s.stopping:
		return s.stoppedError()
	case <-s.done:
		return s.stoppedError()
	default:
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("数据库写入已取消：%w", ctx.Err())
	case <-s.stopping:
		return s.stoppedError()
	case <-s.done:
		return s.stoppedError()
	case s.requests <- req:
		return nil
	}
}

func (s *Store) operation(ctx context.Context, action func(context.Context, *sql.Tx) error) error {
	done := make(chan error, 1)
	if err := s.submit(ctx, request{action: action, done: done}); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("等待数据库提交已取消：%w", ctx.Err())
	case <-s.done:
		return s.Err()
	}
}

func (s *Store) Flush(ctx context.Context) error { return s.operation(ctx, nil) }

func (s *Store) Close() error {
	s.once.Do(func() {
		close(s.stopping)
		s.mu.Lock()
		close(s.requests)
		s.mu.Unlock()
		<-s.done
		s.closeErr = errors.Join(s.Err(), s.db.Close())
	})
	if s.closeErr != nil {
		return fmt.Errorf("关闭监控存储失败：%w", s.closeErr)
	}
	return nil
}

func (s *Store) writeLoop() {
	defer close(s.done)
	flushTicker, pruneTicker := s.clock.NewTicker(500*time.Millisecond), s.clock.NewTicker(time.Hour)
	defer flushTicker.Stop()
	defer pruneTicker.Stop()
	batch := make([]record, 0, 500)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.appendBatch(context.Background(), batch)
		batch = batch[:0]
		return err
	}
	for {
		var err error
		select {
		case req, ok := <-s.requests:
			if !ok {
				if err := flush(); err != nil {
					s.fail(err)
				}
				return
			}
			if req.record != nil {
				batch = append(batch, *req.record)
				if len(batch) < 500 {
					continue
				}
				err = flush()
			} else {
				err = flush()
				if err == nil && req.action != nil {
					err = s.transaction(context.Background(), req.action)
				}
				req.done <- err
			}
		case <-flushTicker.Ticks():
			err = flush()
		case <-pruneTicker.Ticks():
			err = flush()
			if err == nil {
				err = s.transaction(context.Background(), s.prune)
			}
		}
		if err != nil {
			s.fail(err)
			return
		}
	}
}

func (s *Store) transaction(ctx context.Context, action func(context.Context, *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始数据库写入失败：%w", err)
	}
	defer tx.Rollback()
	if err := action(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库写入失败：%w", err)
	}
	return nil
}

func (s *Store) prune(ctx context.Context, tx *sql.Tx) error {
	cutoff := s.now().UTC().Add(-24 * time.Hour).UnixMilli()
	for _, table := range []string{"target_status", "probe_results", "process_samples"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE ts < ?", cutoff); err != nil {
			return fmt.Errorf("清理 %s 的过期原始数据失败：%w", table, err)
		}
	}
	return nil
}

func (s *Store) Prune(ctx context.Context) error { return s.operation(ctx, s.prune) }
