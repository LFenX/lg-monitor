package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type eventLogLimit struct {
	mu    sync.Mutex
	start time.Time
	count int
}

func (l *eventLogLimit) admit(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.start.IsZero() || now.Sub(l.start) >= time.Second {
		l.start, l.count = now, 0
	}
	if l.count >= 100 {
		return false
	}
	l.count++
	return true
}

func processTargets(targets []config.Target) []config.Target {
	result := make([]config.Target, 0, len(targets))
	for _, target := range targets {
		if target.EventLog == nil || target.HasMatch() {
			result = append(result, target)
		}
	}
	return result
}

func eventLogSignature(subscription config.EventLogSubscription) string {
	subscription.IDs, subscription.Levels = slices.Clone(subscription.IDs), slices.Clone(subscription.Levels)
	slices.Sort(subscription.IDs)
	slices.Sort(subscription.Levels)
	data, _ := json.Marshal(subscription)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func eventLogFilters(channel string, targets []config.Target) []collector.EventLogFilter {
	var filters []collector.EventLogFilter
	if channel == "Application" {
		filters = []collector.EventLogFilter{{Provider: "Application Error", IDs: []uint16{1000}}, {Provider: "Application Hang", IDs: []uint16{1002}}}
	} else {
		filters = []collector.EventLogFilter{{Provider: "Service Control Manager", IDs: []uint16{7031, 7034}}, {Provider: "EventLog", IDs: []uint16{6008}}, {Provider: "Microsoft-Windows-Kernel-Power", IDs: []uint16{41}}}
	}
	for _, target := range targets {
		if target.EventLog == nil || target.EventLog.Channel != channel {
			continue
		}
		filter := collector.EventLogFilter{Provider: target.EventLog.Provider}
		for _, id := range target.EventLog.IDs {
			filter.IDs = append(filter.IDs, uint16(id))
		}
		filters = append(filters, filter)
	}
	return filters
}

func eventLogMatches(record collector.EventLogRecord, targets []config.Target, fences map[string]pipeline.EventLogFence) collector.EventLogRecord {
	record.Presets, record.Targets = collector.EventLogPresets(record), nil
	for _, target := range targets {
		subject := false
		if record.Subject != "" && slices.Contains(record.Presets, "app_crash") && target.Match.Process != "" {
			name := func(value string) string {
				return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".exe")
			}
			subject = name(record.Subject) == name(target.Match.Process)
		}
		if record.Subject != "" && slices.Contains(record.Presets, "service_crash") && target.Match.Service != "" {
			subject = strings.EqualFold(record.Subject, target.Match.Service)
		}
		subscription := false
		if spec := target.EventLog; spec != nil && spec.Channel == record.Channel && spec.Provider == record.Provider && record.RecordID > fences[target.ID].AfterID {
			idMatches := len(spec.IDs) == 0 || slices.Contains(spec.IDs, int(record.EventID))
			levelMatches := len(spec.Levels) == 0
			for _, level := range spec.Levels {
				value, known := config.EventLogLevel(level)
				levelMatches = levelMatches || known && value == record.Level
			}
			subscription = idMatches && levelMatches
		}
		via := ""
		switch {
		case subject && subscription:
			via = "both"
		case subject:
			via = "subject"
		case subscription:
			via = "subscription"
		}
		if via != "" {
			record.Targets = append(record.Targets, collector.EventLogTarget{ID: target.ID, Via: via})
		}
	}
	slices.SortFunc(record.Targets, func(a, b collector.EventLogTarget) int {
		aSubject, bSubject := a.Via != "subscription", b.Via != "subscription"
		if aSubject != bSubject {
			if aSubject {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(record.Targets) > 32 {
		record.Targets = record.Targets[:32]
	}
	return record
}

type eventLogRead struct {
	item collector.EventLogItem
	err  error
}
type eventLogStream struct {
	reader collector.EventLogReader
	cancel context.CancelFunc
	done   chan struct{}
	items  chan eventLogRead
}

func startEventLogStream(ctx context.Context, reader collector.EventLogReader) *eventLogStream {
	ctx, cancel := context.WithCancel(ctx)
	s := &eventLogStream{reader: reader, cancel: cancel, done: make(chan struct{}), items: make(chan eventLogRead, 1)}
	go func() {
		defer close(s.done)
		for {
			item, err := reader.Next(ctx)
			select {
			case s.items <- eventLogRead{item, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

func (s *eventLogStream) close() {
	s.cancel()
	_ = s.reader.Close()
	<-s.done
}

func (c *Collector) runEventLogs(ctx context.Context, emitter *pipeline.Emitter) error {
	if c.options.EventLogs == nil {
		return nil
	}
	if _, ok := c.options.Sink.(pipeline.EventLogCheckpointSink); !ok {
		return fmt.Errorf("事件日志需要支持书签的持久缓存")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var limit eventLogLimit
	results := make(chan error, 2)
	for _, channel := range []string{"Application", "System"} {
		go func() {
			err := c.runEventLogChannel(ctx, emitter, channel, &limit)
			if err != nil {
				cancel()
			}
			results <- err
		}()
	}
	return errors.Join(<-results, <-results)
}

func (c *Collector) runEventLogChannel(ctx context.Context, emitter *pipeline.Emitter, channel string, limit *eventLogLimit) (failure error) {
	defer func() {
		if ctx.Err() != nil && errors.Is(failure, ctx.Err()) {
			failure = nil
		}
	}()
	opts := c.options
	sink := opts.Sink.(pipeline.EventLogCheckpointSink)
	state, exists, err := sink.EventLogCheckpointState(ctx, channel)
	if err != nil {
		return err
	}
	if !exists {
		bookmark, err := opts.EventLogs.Anchor(ctx, channel)
		if err != nil {
			return fmt.Errorf("获取 %s 事件日志起点失败：%w", channel, err)
		}
		state = pipeline.EventLogCheckpoint{Channel: channel, Bookmark: bookmark}
		if err := emitter.EmitEventLogCheckpoint(ctx, nil, state); err != nil {
			return err
		}
	}
	updates := opts.Config.Subscribe(ctx)
	var active *config.Config
	var stream *eventLogStream
	var incoming <-chan eventLogRead
	defer func() {
		if stream != nil {
			stream.close()
		}
	}()
	ticker := opts.Clock.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var retryAt time.Time
	var messages []pipeline.Message
	var dropped uint64
	processed := 0
	flush := func() error {
		if processed == 0 && dropped == 0 && len(messages) == 0 {
			return nil
		}
		if dropped > 0 {
			notice := &collector.EventLogNotice{Kind: "eventlog_dropped", Channel: channel, Count: dropped, Message: "事件日志超过采集器每秒 100 条限额"}
			messages = append(messages, pipeline.Message{Kind: pipeline.KindEventLogNotice, EventLogNotice: notice})
		}
		if err := emitter.EmitEventLogCheckpoint(ctx, messages, state); err != nil {
			return err
		}
		messages, dropped, processed = nil, 0, 0
		return nil
	}
	reset := func() error {
		previous, err := collector.EventLogBookmarkRecordID(state.Bookmark, channel)
		if err != nil {
			return err
		}
		bookmark, err := opts.EventLogs.Anchor(ctx, channel)
		if err != nil {
			return err
		}
		latest, err := collector.EventLogBookmarkRecordID(bookmark, channel)
		if err != nil {
			return err
		}
		cleared := latest < previous
		for _, fence := range state.Fences {
			cleared = cleared || fence.AfterID > latest
		}
		state.Bookmark = collector.EventLogEmptyBookmark
		if cleared {
			state.Fences = maps.Clone(state.Fences)
			for id, fence := range state.Fences {
				fence.AfterID = 0
				state.Fences[id] = fence
			}
		}
		notice := &collector.EventLogNotice{Kind: "eventlog_gap", Channel: channel, Message: "事件日志书签已失效，从现存最早事件继续；停止期间存在漏读缺口"}
		return emitter.EmitEventLogCheckpoint(ctx, []pipeline.Message{{Kind: pipeline.KindEventLogNotice, EventLogNotice: notice}}, state)
	}
	activateFences := func() error {
		fences := make(map[string]pipeline.EventLogFence)
		var anchor *uint64
		for _, target := range active.Targets {
			if target.EventLog == nil || target.EventLog.Channel != channel {
				continue
			}
			signature := eventLogSignature(*target.EventLog)
			fence, known := state.Fences[target.ID]
			if !known || fence.Signature != signature {
				if anchor == nil {
					bookmark, err := opts.EventLogs.Anchor(ctx, channel)
					if err != nil {
						return err
					}
					id, err := collector.EventLogBookmarkRecordID(bookmark, channel)
					if err != nil {
						return err
					}
					anchor = &id
				}
				fence = pipeline.EventLogFence{Signature: signature, AfterID: *anchor}
			}
			fences[target.ID] = fence
		}
		state.Fences = fences
		return emitter.EmitEventLogCheckpoint(ctx, nil, state)
	}
	open := func() error {
		reader, err := opts.EventLogs.Subscribe(ctx, collector.EventLogQuery{Channel: channel, Filters: eventLogFilters(channel, active.Targets), Bookmark: state.Bookmark})
		if errors.Is(err, collector.ErrEventLogBookmarkStale) {
			if err := reset(); err != nil {
				return err
			}
			reader, err = opts.EventLogs.Subscribe(ctx, collector.EventLogQuery{Channel: channel, Filters: eventLogFilters(channel, active.Targets), Bookmark: state.Bookmark})
		}
		if err != nil {
			opts.Logger.Warn("Event log subscription failed; retrying", "channel", channel, "error", err)
			retryAt = opts.Now().Add(time.Second)
			return nil
		}
		// Establish new subscription fences only after recovering the previous log cursor.
		if err := activateFences(); err != nil {
			_ = reader.Close()
			return err
		}
		stream = startEventLogStream(ctx, reader)
		incoming = stream.items
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case cfg, ok := <-updates:
			if !ok {
				updates = nil
				if active == nil {
					return fmt.Errorf("事件日志未收到初始配置")
				}
				continue
			}
			if cfg == nil {
				return fmt.Errorf("事件日志收到空配置")
			}
			if err := flush(); err != nil {
				return err
			}
			if stream != nil {
				stream.close()
				stream, incoming = nil, nil
			}
			active = cfg
			if err := open(); err != nil {
				return err
			}
		case result := <-incoming:
			if result.err != nil {
				if err := flush(); err != nil {
					return err
				}
				stream.close()
				stream, incoming = nil, nil
				if ctx.Err() != nil {
					return nil
				}
				if errors.Is(result.err, collector.ErrEventLogBookmarkStale) {
					if err := reset(); err != nil {
						return err
					}
				}
				opts.Logger.Warn("Event log reading failed; resuming from durable bookmark", "channel", channel, "error", result.err)
				retryAt = opts.Now().Add(time.Second)
				continue
			}
			record := result.item.Event
			if record.Channel != channel {
				return fmt.Errorf("事件日志返回的日志与订阅不符")
			}
			if slices.Contains(collector.EventLogPresets(record), "service_crash") && opts.Services != nil {
				var names []string
				for _, target := range active.Targets {
					if target.Match.Service != "" {
						names = append(names, target.Match.Service)
					}
				}
				if len(names) > 0 {
					services, err := opts.Services.Lookup(ctx, names)
					if err == nil {
						for _, service := range services {
							if strings.EqualFold(record.Subject, service.DisplayName) {
								record.Subject = service.Name
								break
							}
						}
					}
				}
			}
			record = eventLogMatches(record, active.Targets, state.Fences)
			if len(record.Presets) > 0 || len(record.Targets) > 0 {
				if limit.admit(opts.Now()) {
					messages = append(messages, pipeline.Message{Kind: pipeline.KindEventLog, TS: record.TS, EventLog: &record})
				} else {
					dropped++
				}
			}
			state.Bookmark = result.item.Bookmark
			processed++
			if processed >= 250 {
				if err := flush(); err != nil {
					return err
				}
			}
		case <-ticker.Ticks():
			if err := flush(); err != nil {
				return err
			}
			if stream == nil && active != nil && !opts.Now().Before(retryAt) {
				if err := open(); err != nil {
					return err
				}
			}
		}
	}
}
