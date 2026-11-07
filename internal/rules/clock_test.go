package rules

import (
	"context"
	"testing"
	"time"
)

type clockPresence struct {
	offset int64
	now    func() time.Time
}

func (source clockPresence) ClockOffset(string) int64 { return source.offset }
func (source clockPresence) Collectors(context.Context) ([]CollectorPresence, error) {
	return []CollectorPresence{{ID: "host-test", Hostname: "fake-host", LastSeen: source.now().UnixMilli(), Connected: true}}, nil
}

func skewFixture(t *testing.T, offset time.Duration, delay string) *fixture {
	t.Helper()
	f := newFixture(t, delay, "process_absent", 0)
	opts := f.options()
	opts.Collectors = clockPresence{offset: offset.Milliseconds(), now: func() time.Time { return f.now }}
	var err error
	f.engine, err = New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func skewStatus(t *testing.T, f *fixture, offset, ago time.Duration, down bool) {
	t.Helper()
	backfillStatus(t, f, "host-test", f.now.Add(-offset-ago), down)
}

func TestClockSkewLiveIncidentsStillHonorForAndRecover(t *testing.T) {
	for _, offset := range []time.Duration{40 * time.Second, 5 * time.Minute, -5 * time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			f := skewFixture(t, offset, "5s")
			skewStatus(t, f, offset, 0, true)
			noNotification(t, f.notifications)
			f.evaluate(t, 4*time.Second)
			noNotification(t, f.notifications)
			f.evaluate(t, time.Second)
			first := takeAlert(t, f.notifications, StateFiring)
			skewStatus(t, f, offset, 0, false)
			if recovered := takeAlert(t, f.notifications, StateResolved); recovered.ID != first.ID || len(f.repository.events) != 2 {
				t.Fatal("clock skew changed live incident lifecycle")
			}
		})
	}
}

func TestClockSkewPreservesAllBackfillScenarios(t *testing.T) {
	for _, offset := range []time.Duration{40 * time.Second, 5 * time.Minute, -5 * time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			t.Run("completed", func(t *testing.T) {
				f := skewFixture(t, offset, "0s")
				skewStatus(t, f, offset, 5*time.Minute, true)
				f.evaluate(t, time.Minute)
				skewStatus(t, f, offset, 2*time.Minute, false)
				f.evaluate(t, time.Minute)
				skewStatus(t, f, offset, time.Second, false)
				noNotification(t, f.notifications)
				if len(f.repository.events) != 0 {
					t.Fatal("ended backfill incident notified")
				}
			})
			t.Run("ongoing", func(t *testing.T) {
				f := skewFixture(t, offset, "2m")
				start := f.now.Add(-offset - 10*time.Minute)
				skewStatus(t, f, offset, 10*time.Minute, true)
				skewStatus(t, f, offset, 5*time.Minute, true)
				noNotification(t, f.notifications)
				skewStatus(t, f, offset, time.Second, true)
				if alert := takeAlert(t, f.notifications, StateFiring); alert.StartedAt != start.UnixMilli() {
					t.Fatalf("raw backfill start changed: %+v", alert)
				}
			})
			t.Run("existing", func(t *testing.T) {
				f := skewFixture(t, offset, "0s")
				skewStatus(t, f, offset, 0, true)
				first := takeAlert(t, f.notifications, StateFiring)
				f.now = f.now.Add(10 * time.Minute)
				skewStatus(t, f, offset, 8*time.Minute, false)
				skewStatus(t, f, offset, 6*time.Minute, true)
				f.evaluate(t, time.Second)
				skewStatus(t, f, offset, time.Second, true)
				noNotification(t, f.notifications)
				skewStatus(t, f, offset, 0, false)
				if resolved := takeAlert(t, f.notifications, StateResolved); resolved.ID != first.ID || len(f.repository.events) != 2 {
					t.Fatal("historical intermediate recovery cycled incident")
				}
			})
			t.Run("thirty-second-boundary", func(t *testing.T) {
				f := skewFixture(t, offset, "5s")
				f.now = f.now.Add(123456 * time.Nanosecond)
				skewStatus(t, f, offset, 30*time.Second, true)
				f.evaluate(t, 5*time.Second)
				takeAlert(t, f.notifications, StateFiring)
			})
		})
	}
}

func TestClockSkewBackfillPendingUsesCorrectedElapsedTime(t *testing.T) {
	for _, offset := range []time.Duration{40 * time.Second, 5 * time.Minute, -5 * time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			f := skewFixture(t, offset, "2m")
			start := f.now.Add(-offset - 95*time.Second)
			skewStatus(t, f, offset, 95*time.Second, true)
			skewStatus(t, f, offset, time.Second, true)
			noNotification(t, f.notifications)
			f.evaluate(t, 24*time.Second)
			noNotification(t, f.notifications)
			f.evaluate(t, time.Second)
			if alert := takeAlert(t, f.notifications, StateFiring); alert.StartedAt != start.UnixMilli() {
				t.Fatal("for timer lost source start")
			}
		})
	}
}
