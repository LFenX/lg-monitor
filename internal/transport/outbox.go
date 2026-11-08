package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

const (
	DefaultCacheBytes = config.DefaultCacheMaxBytes
	MinimumCacheBytes = int64(16 * 1024 * 1024)
	cacheWALReserve   = pipeline.CacheWALReserve
	cachePageSize     = int64(4096)
)

var ErrCacheDiverged = errors.New("本地缓存与监控确认水位不一致")

type CacheLocker interface {
	Lock(string) (io.Closer, error)
}
type nativeCacheLocker struct{}

func (nativeCacheLocker) Lock(path string) (io.Closer, error) { return lockCache(path) }

type OutboxOptions struct {
	Path        string
	CollectorID string
	MaxAge      time.Duration
	MaxBytes    int64
	Now         func() time.Time
	Locker      CacheLocker
	Compactor   CacheCompactor
	Logger      *slog.Logger
}

type CacheState struct {
	Allocated uint64
	Acked     uint64
	GapFrom   uint64
	GapTo     uint64
	GapTS     int64
	Bytes     int64
}

type Outbox struct {
	db                *sql.DB
	options           OutboxOptions
	lease             io.Closer
	maxPages          int64
	mu                sync.Mutex
	closed            bool
	changed           chan struct{}
	done              chan struct{}
	effectiveMaxBytes int64
	resizeMessage     string
	limitDirty        bool
	nextResizeTry     time.Time
	writes            cacheWrites
	snapshotMinute    int64
}

func OpenOutbox(ctx context.Context, options OutboxOptions) (*Outbox, error) {
	if options.Path == "" || options.CollectorID == "" {
		return nil, fmt.Errorf("离线缓存需要路径和采集器身份")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("打开离线缓存已取消：%w", err)
	}
	if options.MaxAge == 0 {
		options.MaxAge = 24 * time.Hour
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultCacheBytes
	}
	if options.MaxAge < 0 || options.MaxBytes < MinimumCacheBytes {
		return nil, fmt.Errorf("缓存保留时间必须为正数，磁盘上限至少为 16 MiB")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Locker == nil {
		options.Locker = nativeCacheLocker{}
	}
	if options.Compactor == nil {
		options.Compactor = nativeCacheCompactor{}
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return nil, fmt.Errorf("解析离线缓存路径失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建离线缓存目录失败：%w", err)
	}
	lease, err := options.Locker.Lock(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("离线缓存已被其他进程占用或无法锁定：%w", err)
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	maxPages := (options.MaxBytes - cacheWALReserve) / cachePageSize
	uri.RawQuery = url.Values{"_pragma": {"page_size(4096)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(FULL)", fmt.Sprintf("max_page_count(%d)", maxPages), "wal_autocheckpoint(64)", "journal_size_limit(1048576)", "secure_delete(OFF)"}, "_txlock": {"immediate"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		lease.Close()
		return nil, fmt.Errorf("打开离线缓存失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	cache := &Outbox{db: db, options: options, lease: lease, maxPages: maxPages, changed: make(chan struct{}, 1), done: make(chan struct{}),
		writes: cacheWrites{started: options.Now()}, snapshotMinute: -1}
	if err := cache.initialize(ctx); err != nil {
		db.Close()
		lease.Close()
		return nil, err
	}
	return cache, nil
}

func (p *Outbox) initialize(ctx context.Context) error {
	var pageSize int64
	if err := p.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return fmt.Errorf("读取缓存页大小失败：%w", err)
	}
	if pageSize != cachePageSize {
		return fmt.Errorf("缓存页大小不是 4096，请恢复原缓存文件，不要删除未确认数据")
	}
	var actualLimit int64
	if err := p.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", p.maxPages)).Scan(&actualLimit); err != nil {
		return fmt.Errorf("设置缓存磁盘上限失败：%w", err)
	}
	p.maxPages = actualLimit
	_, err := p.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS outbox_meta(singleton INTEGER PRIMARY KEY CHECK(singleton=1),collector TEXT NOT NULL,allocated INTEGER NOT NULL,acked INTEGER NOT NULL,gap_from INTEGER NOT NULL,gap_to INTEGER NOT NULL,gap_ts INTEGER NOT NULL,bytes INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS outbox_records(seq INTEGER PRIMARY KEY,queued_at INTEGER NOT NULL,payload BLOB NOT NULL,size INTEGER NOT NULL);
INSERT OR IGNORE INTO outbox_meta(singleton,collector,allocated,acked,gap_from,gap_to,gap_ts,bytes) VALUES(1,?,0,0,0,0,0,0);`, p.options.CollectorID)
	if err != nil {
		return fmt.Errorf("初始化离线缓存失败：%w", err)
	}
	var id string
	if err := p.db.QueryRowContext(ctx, `SELECT collector FROM outbox_meta WHERE singleton=1`).Scan(&id); err != nil {
		return fmt.Errorf("读取缓存身份失败：%w", err)
	}
	if id != p.options.CollectorID {
		return fmt.Errorf("离线缓存与采集器身份不符，请恢复同一份身份、证书和缓存")
	}
	if err := p.initializeUsage(ctx); err != nil {
		return err
	}
	p.effectiveMaxBytes = p.options.MaxBytes
	if p.maxPages > p.requestedPages() {
		p.effectiveMaxBytes = p.maxPages*cachePageSize + cacheWALReserve
		p.trySmallerLimit(ctx)
	}
	return p.saveLimit(ctx, true)
}

func (p *Outbox) State(ctx context.Context) (CacheState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cacheState(ctx, p.db)
}

type cacheQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func cacheState(ctx context.Context, query cacheQuerier) (CacheState, error) {
	var state CacheState
	err := query.QueryRowContext(ctx, `SELECT allocated,acked,gap_from,gap_to,gap_ts,bytes FROM outbox_meta WHERE singleton=1`).Scan(&state.Allocated, &state.Acked, &state.GapFrom, &state.GapTo, &state.GapTS, &state.Bytes)
	if err != nil {
		return state, fmt.Errorf("读取离线缓存水位失败：%w", err)
	}
	if state.Acked > state.Allocated || state.Allocated > pipeline.MaxSequence || state.Bytes < 0 || ((state.GapFrom == 0) != (state.GapTo == 0)) || (state.GapTo > 0 && (state.GapFrom != state.Acked+1 || state.GapFrom > state.GapTo || state.GapTo > state.Allocated)) {
		return state, fmt.Errorf("缓存水位或缺口元数据损坏，请恢复完整备份")
	}
	return state, nil
}

func (p *Outbox) Write(ctx context.Context, message pipeline.Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	if message.CollectorID != p.options.CollectorID || message.Kind == pipeline.KindDataGap {
		return fmt.Errorf("缓存消息身份不符，或调用方试图自行分配缺口")
	}
	// JSON validation rejects non-finite values before they can poison a durable replay.
	if _, err := json.Marshal(message); err != nil {
		return fmt.Errorf("采集消息不能安全持久化：%w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	if err := p.pruneAge(ctx); err != nil {
		return err
	}
	for attempts := 0; attempts < 4; attempts++ {
		state, err := cacheState(ctx, p.db)
		if err != nil {
			return err
		}
		if state.Allocated >= pipeline.MaxSequence {
			return fmt.Errorf("离线缓存序号已耗尽")
		}
		message.Seq = state.Allocated + 1
		record, err := Encode(message)
		if err != nil {
			return err
		}
		data, err := proto.Marshal(record)
		if err != nil {
			return fmt.Errorf("编码离线记录失败：%w", err)
		}
		if len(data)+32 > MaxMessageSize {
			return fmt.Errorf("离线记录超过 4 MiB 发送上限")
		}
		err = p.insert(ctx, message.Seq, data)
		if err == nil {
			p.writes.record(p.options.Now(), int64(len(data))+cacheRecordOverhead)
			p.saveRateSnapshot(ctx)
			p.signal()
			return nil
		}
		var sqliteError *sqlite.Error
		if !errors.As(err, &sqliteError) || sqliteError.Code()&255 != 13 {
			return err
		}
		var pages, free int64
		if queryErr := p.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); queryErr != nil {
			return fmt.Errorf("读取缓存容量失败：%w", queryErr)
		}
		if queryErr := p.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); queryErr != nil {
			return fmt.Errorf("读取缓存空闲页失败：%w", queryErr)
		}
		needed := int64((len(data)+4079)/4080) + 8
		if p.maxPages-(pages-free) >= needed {
			return fmt.Errorf("缓存写入磁盘失败，请检查磁盘可用空间：%w", err)
		}
		if err := p.freeCapacity(ctx, needed); err != nil {
			return err
		}
	}
	return fmt.Errorf("缓存容量整理后仍无法写入，请检查磁盘状态")
}

func (p *Outbox) insert(ctx context.Context, seq uint64, data []byte) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始缓存事务失败：%w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO outbox_records(seq,queued_at,payload,size) VALUES(?,?,?,?)`, seq, p.options.Now().UnixMilli(), data, len(data)); err != nil {
		return fmt.Errorf("写入未确认消息失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE outbox_meta SET allocated=?,bytes=bytes+? WHERE singleton=1`, seq, len(data)); err != nil {
		return fmt.Errorf("保存缓存连续序号失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交离线消息失败：%w", err)
	}
	return nil
}

func (p *Outbox) signal() {
	select {
	case p.changed <- struct{}{}:
	default:
	}
}
func (p *Outbox) Changes() <-chan struct{} { return p.changed }
func (p *Outbox) Done() <-chan struct{}    { return p.done }

func (p *Outbox) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	close(p.done)
	return errors.Join(p.db.Close(), p.lease.Close())
}
