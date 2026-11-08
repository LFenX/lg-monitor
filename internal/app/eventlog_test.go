package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type eventLogFeed struct{ values chan *config.Config }

func (f eventLogFeed) Subscribe(context.Context) <-chan *config.Config { return f.values }

type eventLogSourceRequest struct {
	query  collector.EventLogQuery
	reader *eventLogTestReader
}
type eventLogTestSource struct {
	anchor   atomic.Uint64
	requests chan eventLogSourceRequest
	stale    atomic.Bool
}

func eventLogTestBookmark(channel string, id uint64) string {
	return fmt.Sprintf(`<BookmarkList><Bookmark Channel="%s" RecordId="%d" IsCurrent="true"/></BookmarkList>`, channel, id)
}
func (s *eventLogTestSource) Anchor(_ context.Context, channel string) (string, error) {
	if s.anchor.Load() == 0 {
		return collector.EventLogEmptyBookmark, nil
	}
	return eventLogTestBookmark(channel, s.anchor.Load()), nil
}
func (s *eventLogTestSource) Subscribe(ctx context.Context, q collector.EventLogQuery) (collector.EventLogReader, error) {
	if s.stale.Swap(false) {
		return nil, collector.ErrEventLogBookmarkStale
	}
	r := &eventLogTestReader{values: make(chan collector.EventLogItem, 300), closed: make(chan struct{})}
	select {
	case s.requests <- eventLogSourceRequest{q, r}:
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type eventLogTestReader struct {
	values chan collector.EventLogItem
	closed chan struct{}
	once   sync.Once
}

func (r *eventLogTestReader) Next(ctx context.Context) (collector.EventLogItem, error) {
	select {
	case value := <-r.values:
		return value, nil
	case <-ctx.Done():
		return collector.EventLogItem{}, ctx.Err()
	case <-r.closed:
		return collector.EventLogItem{}, context.Canceled
	}
}
func (r *eventLogTestReader) Close() error { r.once.Do(func() { close(r.closed) }); return nil }

func TestEventLogSubscriptionActivationSurvivesRestartWithoutBackfill(t *testing.T) {
	ctx := context.Background()
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	source := &eventLogTestSource{requests: make(chan eventLogSourceRequest, 10)}
	source.anchor.Store(100)
	clock := &fakeClock{created: make(chan *fakeTicker, 10)}
	clock.millis.Store(100000)
	feed := eventLogFeed{values: make(chan *config.Config, 10)}
	sub := func(id string) config.Target {
		return config.Target{ID: id, EventLog: &config.EventLogSubscription{Channel: "Application", Provider: "Fake", IDs: []int{1}}}
	}
	feed.values <- &config.Config{Targets: []config.Target{sub("first")}}
	c := &Collector{options: CollectorOptions{ID: "fake", Config: feed, EventLogs: source, Sink: box, Now: clock.Now, Clock: clock, Logger: slog.Default()}}
	start := func() (context.CancelFunc, chan error, *fakeTicker) {
		work, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		emitter, err := pipeline.NewEmitter("fake", box, clock.Now)
		if err != nil {
			t.Fatal(err)
		}
		go func() { done <- c.runEventLogChannel(work, emitter, "Application", &eventLogLimit{}) }()
		t.Cleanup(cancel)
		return cancel, done, receive(t, clock.created)
	}
	waitBookmark := func(ticker *fakeTicker, want string) {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		poll := time.NewTicker(time.Millisecond)
		defer poll.Stop()
		for {
			bookmark, _, err := box.EventLogBookmark(ctx, "Application")
			if err != nil {
				t.Fatal(err)
			}
			if bookmark == want {
				return
			}
			select {
			case <-deadline.C:
				t.Fatalf("bookmark did not reach %q", want)
			case <-poll.C:
				clock.tick(ticker, 0)
			}
		}
	}
	item := func(id uint64) collector.EventLogItem {
		return collector.EventLogItem{Event: collector.EventLogRecord{Channel: "Application", Provider: "Fake", EventID: 1, Level: 2, RecordID: id, TS: int64(id) * 1000, Message: "fake"}, Bookmark: eventLogTestBookmark("Application", id)}
	}
	cancel, done, ticker := start()
	first := receive(t, source.requests)
	if first.query.Bookmark != eventLogTestBookmark("Application", 100) {
		t.Fatal("initial subscription imported history")
	}
	first.reader.values <- item(101)
	waitBookmark(ticker, item(101).Bookmark)
	source.anchor.Store(200)
	feed.values <- &config.Config{Targets: []config.Target{sub("first"), sub("second")}}
	second := receive(t, source.requests)
	if second.query.Bookmark != item(101).Bookmark {
		t.Fatal("configuration update skipped unread events")
	}
	second.reader.values <- item(150)
	second.reader.values <- item(201)
	waitBookmark(ticker, item(201).Bookmark)
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	state, exists, err := box.EventLogCheckpointState(ctx, "Application")
	if err != nil || !exists || state.Fences["first"].AfterID != 100 || state.Fences["second"].AfterID != 200 {
		t.Fatalf("activation boundaries: %+v %v", state, err)
	}
	source.anchor.Store(300)
	feed.values <- &config.Config{Targets: []config.Target{sub("first"), sub("second")}}
	cancel, done, ticker = start()
	restarted := receive(t, source.requests)
	if restarted.query.Bookmark != item(201).Bookmark {
		t.Fatal("restart forgot durable bookmark")
	}
	restarted.reader.values <- item(202)
	waitBookmark(ticker, item(202).Bookmark)
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	batch, err := box.NextBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 4 {
		t.Fatalf("events lost or duplicated: %+v", batch)
	}
	for index, message := range batch {
		want := []collector.EventLogTarget{{ID: "first", Via: "subscription"}}
		if index >= 2 {
			want = append(want, collector.EventLogTarget{ID: "second", Via: "subscription"})
		}
		if message.Seq != uint64(index+1) || !slices.Equal(message.EventLog.Targets, want) {
			t.Fatalf("event %d attribution changed: %+v", index, message.EventLog)
		}
	}
}

func TestEventLogBothAttributionLimitAndGlobalRate(t *testing.T) {
	row := collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Subject: "FAKE.EXE", RecordID: 10, TS: 1000}
	var targets []config.Target
	fences := map[string]pipeline.EventLogFence{}
	for index := 39; index >= 0; index-- {
		target := config.Target{ID: fmt.Sprintf("t%02d", index), EventLog: &config.EventLogSubscription{Channel: "Application", Provider: "Application Error"}}
		if index >= 30 {
			target.Match.Process = "fake"
		}
		targets = append(targets, target)
	}
	got := eventLogMatches(row, targets, fences)
	if len(got.Targets) != 32 || len(got.Presets) != 1 || got.Targets[0] != (collector.EventLogTarget{ID: "t30", Via: "both"}) || got.Targets[10] != (collector.EventLogTarget{ID: "t00", Via: "subscription"}) || got.Targets[31].ID != "t21" {
		t.Fatalf("double attribution or truncation differs: %+v", got)
	}
	var limit eventLogLimit
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 150 {
				if limit.admit(time.UnixMilli(1000)) {
					admitted.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 100 || limit.admit(time.UnixMilli(999)) || !limit.admit(time.UnixMilli(2000)) {
		t.Fatal("rate limit is not shared by both log workers")
	}
}

func TestEventLogStaleBookmarkCreatesGapAndResumesOldest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	state := pipeline.EventLogCheckpoint{Channel: "Application", Bookmark: eventLogTestBookmark("Application", 99)}
	if err := box.WriteEventLogCheckpoint(ctx, nil, state); err != nil {
		t.Fatal(err)
	}
	source := &eventLogTestSource{requests: make(chan eventLogSourceRequest, 2)}
	source.stale.Store(true)
	feed := eventLogFeed{values: make(chan *config.Config, 1)}
	feed.values <- &config.Config{}
	clock := &fakeClock{created: make(chan *fakeTicker, 2)}
	clock.millis.Store(1000)
	c := &Collector{options: CollectorOptions{Config: feed, EventLogs: source, Sink: box, Now: clock.Now, Clock: clock, Logger: slog.Default()}}
	emitter, _ := pipeline.NewEmitter("fake", box, clock.Now)
	done := make(chan error, 1)
	go func() { done <- c.runEventLogChannel(ctx, emitter, "Application", &eventLogLimit{}) }()
	request := receive(t, source.requests)
	if request.query.Bookmark != collector.EventLogEmptyBookmark {
		t.Fatal("stale bookmark did not fall back to oldest")
	}
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	batch, err := box.NextBatch(context.Background())
	if err != nil || len(batch) != 1 {
		t.Fatalf("missing gap: %v %v", batch, err)
	}
	message := batch[0]
	if message.EventLogNotice == nil || message.EventLogNotice.Kind != "eventlog_gap" {
		t.Fatalf("incorrect gap: %+v", message)
	}
}

func TestEventLogRateDropsAndBookmarkCommitWithoutSequenceHoles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	source := &eventLogTestSource{requests: make(chan eventLogSourceRequest, 2)}
	source.anchor.Store(100)
	feed := eventLogFeed{values: make(chan *config.Config, 1)}
	feed.values <- &config.Config{}
	clock := &fakeClock{created: make(chan *fakeTicker, 2)}
	clock.millis.Store(1000000)
	c := &Collector{options: CollectorOptions{Config: feed, EventLogs: source, Sink: box, Now: clock.Now, Clock: clock, Logger: slog.Default()}}
	emitter, _ := pipeline.NewEmitter("fake", box, clock.Now)
	done := make(chan error, 1)
	go func() { done <- c.runEventLogChannel(ctx, emitter, "Application", &eventLogLimit{}) }()
	request := receive(t, source.requests)
	for id := uint64(101); id <= 350; id++ {
		request.reader.values <- collector.EventLogItem{Event: collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, RecordID: id, TS: clock.Now().UnixMilli(), Message: "fake event"}, Bookmark: eventLogTestBookmark("Application", id)}
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		bookmark, _, err := box.EventLogBookmark(ctx, "Application")
		if err != nil {
			t.Fatal(err)
		}
		if bookmark == eventLogTestBookmark("Application", 350) {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("rate-limited batch did not checkpoint")
		case <-tick.C:
		}
	}
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	batch, err := box.NextBatch(context.Background())
	if err != nil || len(batch) != 101 {
		t.Fatalf("rate accounting: %d %v", len(batch), err)
	}
	for index, row := range batch {
		if row.Seq != uint64(index+1) {
			t.Fatal("intentional drops consumed transport sequences")
		}
	}
	notice := batch[len(batch)-1].EventLogNotice
	if notice == nil || notice.Kind != "eventlog_dropped" || notice.Count != 150 || batch[0].EventLog.RecordID != 101 || batch[99].EventLog.RecordID != 200 {
		t.Fatalf("rate audit or accepted records differ: %+v", batch)
	}
}

func TestEventLogStaleRecoveryPreservesActivationFencesAndNewSubscriptions(t *testing.T) {
	for _, latest := range []uint64{50, 200} {
		t.Run(fmt.Sprint(latest), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake"})
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			sub := &config.EventLogSubscription{Channel: "Application", Provider: "Fake"}
			checkpoint := pipeline.EventLogCheckpoint{Channel: "Application", Bookmark: eventLogTestBookmark("Application", 101), Fences: map[string]pipeline.EventLogFence{"old": {Signature: eventLogSignature(*sub), AfterID: 100}}}
			if err := box.WriteEventLogCheckpoint(ctx, nil, checkpoint); err != nil {
				t.Fatal(err)
			}
			source := &eventLogTestSource{requests: make(chan eventLogSourceRequest, 3)}
			source.anchor.Store(latest)
			source.stale.Store(true)
			feed := eventLogFeed{values: make(chan *config.Config, 1)}
			feed.values <- &config.Config{Targets: []config.Target{{ID: "old", EventLog: sub}, {ID: "new", EventLog: sub}}}
			clock := &fakeClock{created: make(chan *fakeTicker, 2)}
			clock.millis.Store(1000000)
			c := &Collector{options: CollectorOptions{Config: feed, EventLogs: source, Sink: box, Now: clock.Now, Clock: clock, Logger: slog.Default()}}
			emitter, _ := pipeline.NewEmitter("fake", box, clock.Now)
			done := make(chan error, 1)
			go func() { done <- c.runEventLogChannel(ctx, emitter, "Application", &eventLogLimit{}) }()
			ticker := receive(t, clock.created)
			request := receive(t, source.requests)
			if request.query.Bookmark != collector.EventLogEmptyBookmark {
				t.Fatal("stale cursor was reused")
			}
			for _, id := range []uint64{latest - 1, latest + 1} {
				request.reader.values <- collector.EventLogItem{Event: collector.EventLogRecord{Channel: "Application", Provider: "Fake", EventID: 1, RecordID: id, TS: 1000000 + int64(id), Message: "fake"}, Bookmark: eventLogTestBookmark("Application", id)}
			}
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			for {
				bookmark, _, err := box.EventLogBookmark(ctx, "Application")
				if err != nil {
					t.Fatal(err)
				}
				if bookmark == eventLogTestBookmark("Application", latest+1) {
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("stale recovery did not finish")
				case <-poll.C:
					clock.tick(ticker, 0)
				}
			}
			cancel()
			if err := receive(t, done); err != nil {
				t.Fatal(err)
			}
			state, _, err := box.EventLogCheckpointState(context.Background(), "Application")
			if err != nil {
				t.Fatal(err)
			}
			oldFence := uint64(100)
			if latest == 50 {
				oldFence = 0
			}
			if state.Fences["old"].AfterID != oldFence || state.Fences["new"].AfterID != latest {
				t.Fatalf("recovery changed activation fences: %+v", state.Fences)
			}
			batch, err := box.NextBatch(context.Background())
			if err != nil || len(batch) != 3 {
				t.Fatalf("recovered batch: %+v %v", batch, err)
			}
			if len(batch[1].EventLog.Targets) != 1 || batch[1].EventLog.Targets[0].ID != "old" || len(batch[2].EventLog.Targets) != 2 {
				t.Fatal("new subscription backfilled pre-activation events")
			}
		})
	}
}

func TestEventLogLargeAttributionBatchesPreserveLongIDsAndDurableCursor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &fakeClock{created: make(chan *fakeTicker, 2)}
	clock.millis.Store(1000000)
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake", Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	cfg := &config.Config{}
	for index := range 32 {
		cfg.Targets = append(cfg.Targets, config.Target{ID: strings.Repeat("a", 4096) + fmt.Sprintf("-%02d", index), Match: config.Match{Process: "fake"}, EventLog: &config.EventLogSubscription{Channel: "Application", Provider: "Application Error"}})
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("regression fixture is not accepted by existing config validation: %v", err)
	}
	source := &eventLogTestSource{requests: make(chan eventLogSourceRequest, 2)}
	source.anchor.Store(100)
	feed := eventLogFeed{values: make(chan *config.Config, 1)}
	feed.values <- cfg
	c := &Collector{options: CollectorOptions{Config: feed, EventLogs: source, Sink: box, Now: clock.Now, Clock: clock, Logger: slog.Default()}}
	emitter, _ := pipeline.NewEmitter("fake", box, clock.Now)
	done := make(chan error, 1)
	go func() { done <- c.runEventLogChannel(ctx, emitter, "Application", &eventLogLimit{}) }()
	ticker := receive(t, clock.created)
	request := receive(t, source.requests)
	for id := uint64(101); id <= 108; id++ {
		request.reader.values <- collector.EventLogItem{Event: collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, RecordID: id, TS: 1000000, Subject: "fake.exe", Message: "fake"}, Bookmark: eventLogTestBookmark("Application", id)}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	split := false
	for {
		state, err := box.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state.Allocated > 0 {
			split = true
		}
		if state.Allocated == 8 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("large event attribution did not split before the record-count limit")
		case <-poll.C:
			if split {
				clock.tick(ticker, 0)
			}
		}
	}
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	checkpoint, exists, err := box.EventLogCheckpointState(context.Background(), "Application")
	if err != nil || !exists || len(checkpoint.Fences) != 32 || checkpoint.Bookmark != eventLogTestBookmark("Application", 108) {
		t.Fatalf("large attribution lost its checkpoint: %+v %v", checkpoint, err)
	}
	seen := 0
	for seen < 8 {
		batch, err := box.NextBatch(context.Background())
		if err != nil || len(batch) == 0 {
			t.Fatalf("missing durable events: %d %v", seen, err)
		}
		for _, row := range batch {
			if row.Seq != uint64(seen+1) || row.EventLog == nil || len(row.EventLog.Targets) != 32 || len(row.EventLog.Targets[0].ID) < 4096 {
				t.Fatal("large attribution or sequence changed")
			}
			seen++
		}
		if err := box.Acknowledge(context.Background(), batch[len(batch)-1].Seq); err != nil {
			t.Fatal(err)
		}
	}
}
