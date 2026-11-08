package transport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

const cacheRecordOverhead = int64(24)

type cacheMinute struct {
	minute int64
	bytes  int64
}

type cacheWrites struct {
	started time.Time
	minutes [60]cacheMinute
	total   int64
}

func (w *cacheWrites) record(at time.Time, bytes int64) {
	minute := at.Unix() / 60
	index := (minute%60 + 60) % 60
	if w.minutes[index].minute != minute {
		w.minutes[index] = cacheMinute{minute: minute}
	}
	w.minutes[index].bytes += bytes
	w.total += bytes
}

func (w *cacheWrites) rate(at time.Time) *float64 {
	elapsed := at.Sub(w.started)
	if elapsed < 5*time.Minute {
		return nil
	}
	minute := at.Unix() / 60
	var bytes int64
	for _, bucket := range w.minutes {
		if bucket.minute <= minute && bucket.minute > minute-60 {
			bytes += bucket.bytes
		}
	}
	if bytes == 0 {
		return nil
	}
	value := float64(bytes) / min(elapsed.Hours(), 1)
	return &value
}

type CacheLimitState struct {
	CollectorID       string
	RequestedMaxBytes int64
	Message           string
}

var ErrCacheStatsUnavailable = errors.New("缓存尚无用量统计，请在新版本采集器启动后查看")

func (r *Receiver) CacheUsage(id string) *pipeline.CacheUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cacheStates[id].Clone()
}

func CacheFromProto(value *lgmonv1.CollectorCache) (*pipeline.CacheUsage, error) {
	if value == nil {
		return nil, nil
	}
	usage, err := pipeline.NewCacheUsage(value.Bytes, value.MaxBytes, value.BytesPerHour)
	if err != nil {
		return nil, err
	}
	return &usage, nil
}

func (p *Outbox) Usage(ctx context.Context) (pipeline.CacheUsage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return pipeline.CacheUsage{}, fmt.Errorf("离线缓存已关闭：%w", io.ErrClosedPipe)
	}
	p.applyEmptyLimit(ctx)
	if p.limitDirty {
		if err := p.saveLimit(ctx, false); err != nil {
			p.options.Logger.Warn("Cache limit status could not be saved")
		}
	}
	bytes, err := cacheUsedBytes(ctx, p.db)
	if err != nil {
		return pipeline.CacheUsage{}, err
	}
	p.saveRateSnapshot(ctx)
	return pipeline.NewCacheUsage(bytes, p.effectiveMaxBytes, p.writes.rate(p.options.Now()))
}

func cacheUsedBytes(ctx context.Context, db cacheQuerier) (int64, error) {
	var pages, free int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return 0, fmt.Errorf("读取缓存页数失败：%w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
		return 0, fmt.Errorf("读取缓存空闲页失败：%w", err)
	}
	if free < 0 || free > pages {
		return 0, fmt.Errorf("缓存已用页数无效")
	}
	return (pages - free) * cachePageSize, nil
}

// This is the last reported estimate, never the window counters used by a new process.
func (p *Outbox) saveRateSnapshot(ctx context.Context) {
	if p.options.Now().Sub(p.writes.started) < 5*time.Minute {
		return
	}
	minute := p.options.Now().Unix() / 60
	if p.snapshotMinute == minute {
		return
	}
	p.snapshotMinute = minute
	if _, err := p.db.ExecContext(ctx, "UPDATE outbox_meta SET bytes_per_hour=? WHERE singleton=1", p.writes.rate(p.options.Now())); err != nil {
		p.options.Logger.Warn("Cache usage snapshot could not be saved")
	}
}

func ReadCacheUsage(ctx context.Context, path string) (pipeline.CacheUsage, CacheLimitState, error) {
	if _, err := os.Stat(path); err != nil {
		return pipeline.CacheUsage{}, CacheLimitState{}, err
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(1000)"}}.Encode()}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return pipeline.CacheUsage{}, CacheLimitState{}, fmt.Errorf("打开缓存状态失败：%w", err)
	}
	defer db.Close()
	var columns int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('outbox_meta') WHERE name='max_bytes'").Scan(&columns); err != nil {
		return pipeline.CacheUsage{}, CacheLimitState{}, fmt.Errorf("读取缓存统计版本失败：%w", err)
	}
	if columns == 0 {
		return pipeline.CacheUsage{}, CacheLimitState{}, ErrCacheStatsUnavailable
	}
	var maxBytes int64
	var rate sql.NullFloat64
	var limit CacheLimitState
	if err := db.QueryRowContext(ctx, "SELECT collector,max_bytes,desired_max_bytes,bytes_per_hour,resize_message FROM outbox_meta WHERE singleton=1").Scan(&limit.CollectorID, &maxBytes, &limit.RequestedMaxBytes, &rate, &limit.Message); err != nil {
		return pipeline.CacheUsage{}, limit, fmt.Errorf("读取缓存上限和最近统计失败：%w", err)
	}
	bytes, err := cacheUsedBytes(ctx, db)
	if err != nil {
		return pipeline.CacheUsage{}, limit, err
	}
	var value *float64
	if rate.Valid {
		value = &rate.Float64
	}
	usage, err := pipeline.NewCacheUsage(bytes, maxBytes, value)
	return usage, limit, err
}
