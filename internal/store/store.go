package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	Path       string
	Now        func() time.Time
	Clock      Clock
	Logger     *slog.Logger
	MaxPending int
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
	logger         *slog.Logger
	maxPending     int
	dropped        atomic.Uint64
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
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.MaxPending == 0 {
		options.MaxPending = 10_000
	}
	if options.MaxPending < 1 {
		return nil, fmt.Errorf("原始采样积压上限必须大于零")
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
	s := &Store{db: db, now: options.Now, clock: options.Clock, requests: make(chan request, 2048), stopping: make(chan struct{}), done: make(chan struct{}), logger: options.Logger, maxPending: options.MaxPending}
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
		select {
		case err := <-done:
			return err
		default:
			return s.stoppedError()
		}
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
	batch := rawBuffer{limit: s.maxPending}
	var retry Ticker
	var retryTicks <-chan time.Time
	delay := 500 * time.Millisecond
	stopRetry := func() {
		if retry != nil {
			retry.Stop()
		}
		retry, retryTicks = nil, nil
	}
	defer stopRetry()
	flush := func(force bool) error {
		if len(batch.rows) == 0 {
			return nil
		}
		if retryTicks != nil && !force {
			return s.Err()
		}
		err := s.appendBatch(context.Background(), batch.ordered())
		if err != nil {
			s.fail(err)
			s.logger.Warn("Raw sample batch failed; retaining records for retry", "pending", len(batch.rows), "retryDelay", delay)
			stopRetry()
			retry = s.clock.NewTicker(delay)
			retryTicks = retry.Ticks()
			delay = min(delay*2, 30*time.Second)
			return err
		}
		batch.clear()
		stopRetry()
		delay = 500 * time.Millisecond
		s.fail(nil)
		return err
	}
	for {
		var err error
		select {
		case req, ok := <-s.requests:
			if !ok {
				if err := flush(true); err != nil {
					s.fail(err)
				}
				return
			}
			if req.record != nil {
				if batch.append(*req.record) {
					s.dropped.Add(1)
					s.logger.Warn("Raw sample backlog full; dropped oldest sample", "dropped", s.dropped.Load(), "limit", s.maxPending)
				}
				if len(batch.rows) < min(500, s.maxPending) || retryTicks != nil {
					continue
				}
				err = flush(false)
			} else {
				err = flush(false)
				if err == nil && req.action != nil {
					err = s.transaction(context.Background(), req.action)
				}
				req.done <- err
			}
		case <-flushTicker.Ticks():
			if retryTicks != nil {
				continue
			}
			err = flush(false)
		case <-retryTicks:
			stopRetry()
			err = flush(true)
		case <-pruneTicker.Ticks():
			if retryTicks != nil {
				continue
			}
			err = flush(false)
			if err == nil {
				err = s.transaction(context.Background(), s.prune)
			}
		}
		if err != nil {
			s.logger.Warn("Storage operation failed; writer remains active")
		}
	}
}

type rawBuffer struct {
	rows  []record
	head  int
	limit int
}

func (b *rawBuffer) append(row record) bool {
	if len(b.rows) < b.limit {
		b.rows = append(b.rows, row)
		return false
	}
	b.rows[b.head] = row
	b.head = (b.head + 1) % b.limit
	return true
}

func (b *rawBuffer) ordered() []record {
	if b.head == 0 {
		return b.rows
	}
	rows := make([]record, 0, len(b.rows))
	rows = append(rows, b.rows[b.head:]...)
	return append(rows, b.rows[:b.head]...)
}

func (b *rawBuffer) clear() {
	clear(b.rows)
	b.rows, b.head = b.rows[:0], 0
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
	for _, table := range []string{"target_status", "probe_results", "process_samples", "system_metrics", "disk_metrics", "network_metrics", "script_metrics"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE ts < ?", cutoff); err != nil {
			return fmt.Errorf("清理 %s 的过期原始数据失败：%w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_status WHERE ts < ? AND ts < (SELECT max(latest.ts) FROM service_status AS latest WHERE latest.collector_id=service_status.collector_id AND latest.name=service_status.name)`, cutoff); err != nil {
		return fmt.Errorf("清理旧服务状态失败：%w", err)
	}
	return nil
}

func (s *Store) Prune(ctx context.Context) error { return s.operation(ctx, s.prune) }
