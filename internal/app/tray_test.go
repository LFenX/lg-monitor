package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

type trayStatusReply struct {
	value TrayStatus
	err   error
}

type trayStatusCall struct {
	ctx   context.Context
	reply chan trayStatusReply
}

type trayPauseCall struct {
	duration time.Duration
	reply    chan error
}

type controlledTrayBackend struct {
	statuses chan trayStatusCall
	pauses   chan trayPauseCall
	active   atomic.Int32
	maximum  atomic.Int32
}

func (b *controlledTrayBackend) Status(ctx context.Context) (TrayStatus, error) {
	active := b.active.Add(1)
	defer b.active.Add(-1)
	for previous := b.maximum.Load(); active > previous && !b.maximum.CompareAndSwap(previous, active); previous = b.maximum.Load() {
	}
	call := trayStatusCall{ctx: ctx, reply: make(chan trayStatusReply, 1)}
	select {
	case b.statuses <- call:
	case <-ctx.Done():
		return TrayStatus{}, ctx.Err()
	}
	select {
	case reply := <-call.reply:
		return reply.value, reply.err
	case <-ctx.Done():
		return TrayStatus{}, ctx.Err()
	}
}

func (b *controlledTrayBackend) Pause(ctx context.Context, duration time.Duration) error {
	call := trayPauseCall{duration: duration, reply: make(chan error, 1)}
	select {
	case b.pauses <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *controlledTrayBackend) Resume(ctx context.Context) error { return b.Pause(ctx, 0) }

type fakeTrayDriver struct {
	events     chan TrayAction
	views      chan TrayView
	runError   error
	updateFail error
	exited     atomic.Bool
}

func (d *fakeTrayDriver) Run(ctx context.Context, ready func()) error {
	defer d.exited.Store(true)
	if d.runError != nil {
		return d.runError
	}
	ready()
	<-ctx.Done()
	return nil
}
func (d *fakeTrayDriver) Events() <-chan TrayAction { return d.events }
func (d *fakeTrayDriver) Update(view TrayView) error {
	if d.updateFail != nil {
		return d.updateFail
	}
	d.views <- view
	return nil
}

type trayOpenCall struct {
	address string
	reply   chan error
}

type controlledTrayOpener struct{ calls chan trayOpenCall }

func (o controlledTrayOpener) Open(ctx context.Context, address string) error {
	call := trayOpenCall{address: address, reply: make(chan error, 1)}
	select {
	case o.calls <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type trayFixture struct {
	backend *controlledTrayBackend
	driver  *fakeTrayDriver
	opener  controlledTrayOpener
	clock   *fakeClock
	log     bytes.Buffer
	done    chan struct{}
	err     error
}

func newTrayFixture(t *testing.T, change func(*trayFixture)) *trayFixture {
	t.Helper()
	f := &trayFixture{
		backend: &controlledTrayBackend{statuses: make(chan trayStatusCall, 8), pauses: make(chan trayPauseCall, 8)},
		driver:  &fakeTrayDriver{events: make(chan TrayAction, 8), views: make(chan TrayView, 32)},
		opener:  controlledTrayOpener{calls: make(chan trayOpenCall, 8)},
		clock:   &fakeClock{created: make(chan *fakeTicker, 2)},
		done:    make(chan struct{}),
	}
	if change != nil {
		change(f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		f.err = RunTray(ctx, TrayOptions{Backend: f.backend, Driver: f.driver, Opener: f.opener, Clock: f.clock, Logger: slog.New(slog.NewTextHandler(&f.log, nil))})
		close(f.done)
	}()
	t.Cleanup(func() { cancel(); receiveRuntime(t, f.done) })
	return f
}

func trayView(t *testing.T, f *trayFixture, matches func(TrayView) bool) TrayView {
	t.Helper()
	limit := time.NewTimer(10 * time.Second)
	defer limit.Stop()
	for {
		select {
		case view := <-f.driver.views:
			if matches(view) {
				return view
			}
		case <-limit.C:
			t.Fatal("tray did not display the expected state")
		}
	}
}

func TestTrayTracksAlertStateAndConnectionRecoveryWithoutLeakingErrors(t *testing.T) {
	f := newTrayFixture(t, nil)
	ticker := receiveRuntime(t, f.clock.created)
	if ticker.interval != 10*time.Second {
		t.Fatalf("poll interval: %s", ticker.interval)
	}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayStarting })
	call := receiveRuntime(t, f.backend.statuses)
	status := TrayStatus{PanelURL: "http://127.0.0.1:7444", CanPause: true}
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayHealthy && v.CanOpen && v.CanPause })
	f.clock.tick(ticker, 5*time.Second)
	call = receiveRuntime(t, f.backend.statuses)
	status.Firing = 3
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayWarning && v.Firing == 3 })
	f.clock.tick(ticker, 10*time.Second)
	call = receiveRuntime(t, f.backend.statuses)
	status.Critical = 1
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayFiring && v.Firing == 3 })
	f.clock.tick(ticker, 5*time.Second)
	call = receiveRuntime(t, f.backend.statuses)
	call.reply <- trayStatusReply{err: errors.New("fake-private-token")}
	trayView(t, f, func(v TrayView) bool {
		return v.State == TrayUnavailable && v.CanOpen && !v.CanPause && !strings.Contains(v.Message, "fake-private-token")
	})
	f.clock.tick(ticker, 5*time.Second)
	call = receiveRuntime(t, f.backend.statuses)
	status.Firing = 0
	status.Critical = 0
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayHealthy && v.CanPause })
	f.driver.events <- TrayExit
	receiveRuntime(t, f.done)
	if f.err != nil || !ticker.stopped.Load() || !f.driver.exited.Load() {
		t.Fatalf("tray cleanup: %v ticker=%v driver=%v", f.err, ticker.stopped.Load(), f.driver.exited.Load())
	}
	if strings.Contains(f.log.String(), "fake-private-token") {
		t.Fatal("backend error exposed credential data")
	}
}

func TestTrayMenuUsesPermissionsAndFixedOneHourWithoutOverlappingPauses(t *testing.T) {
	f := newTrayFixture(t, nil)
	ticker := receiveRuntime(t, f.clock.created)
	call := receiveRuntime(t, f.backend.statuses)
	status := TrayStatus{PanelURL: "http://127.0.0.1:7444"}
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayHealthy && !v.CanPause })
	f.driver.events <- TrayPauseHour
	f.driver.events <- TrayOpenPanel
	open := receiveRuntime(t, f.opener.calls)
	if open.address != status.PanelURL || len(f.backend.pauses) != 0 {
		t.Fatal("read-only tray changed notification controls")
	}
	open.reply <- errors.New("fake-private-token")
	trayView(t, f, func(v TrayView) bool { return strings.Contains(v.Message, "打开面板失败") })
	f.clock.tick(ticker, 5*time.Second)
	call = receiveRuntime(t, f.backend.statuses)
	status.CanPause = true
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.CanPause })
	f.driver.events <- TrayPauseHour
	pause := receiveRuntime(t, f.backend.pauses)
	if pause.duration != time.Hour {
		t.Fatalf("pause duration=%s", pause.duration)
	}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayHealthy && !v.CanPause })
	f.driver.events <- TrayPauseHour
	f.driver.events <- TrayOpenPanel
	open = receiveRuntime(t, f.opener.calls)
	if len(f.backend.pauses) != 0 {
		t.Fatal("a pending pause was submitted twice")
	}
	pause.reply <- nil
	trayView(t, f, func(v TrayView) bool { return v.CanPause && strings.Contains(v.Message, "已暂停通知 1 小时") })
	open.reply <- nil
	f.driver.events <- TrayPauseHour
	pause = receiveRuntime(t, f.backend.pauses)
	pause.reply <- errors.New("fake-private-token")
	trayView(t, f, func(v TrayView) bool { return v.CanPause && strings.Contains(v.Message, "暂停通知失败") })
	f.driver.events <- TrayExit
	receiveRuntime(t, f.done)
	if f.err != nil || strings.Contains(f.log.String(), "fake-private-token") {
		t.Fatalf("tray error handling: %v %s", f.err, f.log.String())
	}
}

func TestTrayExitCancelsBlockedRefreshAndStopsItsClock(t *testing.T) {
	f := newTrayFixture(t, nil)
	ticker := receiveRuntime(t, f.clock.created)
	call := receiveRuntime(t, f.backend.statuses)
	for range 10 {
		f.clock.tick(ticker, time.Second)
	}
	f.driver.events <- TrayExit
	receiveRuntime(t, f.done)
	if f.err != nil || call.ctx.Err() == nil || !ticker.stopped.Load() || f.backend.active.Load() != 0 || f.backend.maximum.Load() != 1 || len(f.backend.statuses) != 0 {
		t.Fatalf("slow refresh leaked: err=%v ctx=%v active=%d maximum=%d pending=%d", f.err, call.ctx.Err(), f.backend.active.Load(), f.backend.maximum.Load(), len(f.backend.statuses))
	}
}

func TestTrayFailureBeforeReadyDoesNotStartPolling(t *testing.T) {
	f := newTrayFixture(t, func(f *trayFixture) { f.driver.runError = errors.New("fake UI unavailable") })
	receiveRuntime(t, f.done)
	if f.err == nil || !strings.Contains(f.err.Error(), "运行托盘失败") || len(f.backend.statuses) != 0 || len(f.clock.created) != 0 {
		t.Fatalf("failed tray started background work: %v", f.err)
	}
}

func TestTrayUpdateFailureClosesDriverAndClock(t *testing.T) {
	f := newTrayFixture(t, func(f *trayFixture) { f.driver.updateFail = errors.New("fake UI failure") })
	ticker := receiveRuntime(t, f.clock.created)
	receiveRuntime(t, f.done)
	if f.err == nil || !strings.Contains(f.err.Error(), "更新托盘状态失败") || !ticker.stopped.Load() || !f.driver.exited.Load() || len(f.backend.statuses) != 0 {
		t.Fatalf("tray update failure cleanup: %v", f.err)
	}
}

func TestTrayRefusesUnsafeBrowserDestinationsAndPauses(t *testing.T) {
	for _, address := range []string{"file:///C:/fake.exe", "javascript:fake()", "http://fake:token@127.0.0.1", "http://127.0.0.1/?token=fake", "http://127.0.0.1/#fake", "http://127.0.0.1/\x00"} {
		t.Run(address, func(t *testing.T) {
			f := newTrayFixture(t, nil)
			call := receiveRuntime(t, f.backend.statuses)
			call.reply <- trayStatusReply{value: TrayStatus{PanelURL: address, CanPause: true}}
			trayView(t, f, func(v TrayView) bool { return v.State == TrayUnavailable && !v.CanOpen && !v.CanPause })
			f.driver.events <- TrayOpenPanel
			f.driver.events <- TrayPauseHour
			f.driver.events <- TrayExit
			receiveRuntime(t, f.done)
			if f.err != nil || len(f.opener.calls) != 0 || len(f.backend.pauses) != 0 {
				t.Fatalf("unsafe destination reached system adapter: %v", f.err)
			}
		})
	}
}

func TestTrayPausedStateOffersResume(t *testing.T) {
	f := newTrayFixture(t, nil)
	receiveRuntime(t, f.clock.created)
	call := receiveRuntime(t, f.backend.statuses)
	until := int64(4000000)
	status := TrayStatus{CanPause: true, PanelURL: "http://127.0.0.1:7444", Pause: store.NotificationPause{Paused: true, Until: &until}}
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayPaused && v.Until == until && v.CanPause })
	f.driver.events <- TrayPauseHour
	resume := receiveRuntime(t, f.backend.pauses)
	if resume.duration != 0 {
		t.Fatal("paused menu started another pause")
	}
	resume.reply <- nil
	call = receiveRuntime(t, f.backend.statuses)
	status.Pause = store.NotificationPause{}
	call.reply <- trayStatusReply{value: status}
	trayView(t, f, func(v TrayView) bool { return v.State == TrayHealthy && strings.Contains(v.Message, "已恢复通知") })
	f.driver.events <- TrayExit
	receiveRuntime(t, f.done)
}
