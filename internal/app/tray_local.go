package app

import (
	"context"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type trayLocalReader interface {
	AlertHistory(context.Context, string, int64) ([]rules.Alert, error)
	NotificationPause(context.Context) (store.NotificationPause, error)
}
type trayLocalWriter interface {
	SetNotificationPause(context.Context, time.Duration, string, string) (store.NotificationPause, error)
	ResumeNotifications(context.Context) (store.NotificationPause, error)
	PreparePauseResumes(context.Context) error
}
type localTray struct {
	read    trayLocalReader
	write   trayLocalWriter
	events  LivePublisher
	url     string
	address func() string
}

func (b localTray) Status(ctx context.Context) (TrayStatus, error) {
	alerts, err := b.read.AlertHistory(ctx, rules.StateFiring, 0)
	if err != nil {
		return TrayStatus{}, err
	}
	pause, err := b.read.NotificationPause(ctx)
	if err != nil {
		return TrayStatus{}, err
	}
	state := TrayStatus{Firing: len(alerts), CanPause: true, PanelURL: b.url, Pause: pause}
	if b.address != nil {
		state.PanelURL = b.address()
	}
	for _, a := range alerts {
		if a.Severity == "critical" {
			state.Critical++
		}
	}
	return state, nil
}
func (b localTray) Pause(ctx context.Context, d time.Duration) error {
	p, err := b.write.SetNotificationPause(ctx, d, "", "local")
	if err != nil {
		return err
	}
	return b.events.Publish("pause", p)
}
func (b localTray) Resume(ctx context.Context) error {
	p, err := b.write.ResumeNotifications(ctx)
	if err != nil {
		return err
	}
	if err := b.write.PreparePauseResumes(ctx); err != nil {
		return err
	}
	return b.events.Publish("pause", p)
}

func RunDesktop(parent context.Context, opts LocalOptions, run func(context.Context, LocalOptions) error, tray func(context.Context, TrayBackend) error) error {
	if parent.Err() != nil {
		return nil
	}
	if opts.NoTray || opts.Role == "collector" {
		return run(parent, opts)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ready := make(chan TrayBackend, 1)
	opts.TrayReady = func(b TrayBackend) {
		select {
		case ready <- b:
		case <-ctx.Done():
		}
	}
	done := make(chan error, 1)
	go func() { done <- run(ctx, opts); cancel() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return <-done
	case backend := <-ready:
		err := tray(ctx, backend)
		cancel()
		runErr := <-done
		if runErr != nil {
			return runErr
		}
		if err != nil {
			return fmt.Errorf("运行桌面托盘失败：%w", err)
		}
		return runErr
	}
}
