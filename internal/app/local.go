package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
)

type HostSource interface {
	Hostname(context.Context) (string, error)
}

type nativeHost struct{}

func (nativeHost) Hostname(context.Context) (string, error) { return os.Hostname() }

type LocalOptions struct {
	Home, Version         string
	Role, Listen, Monitor string
	NewClient             ClientFactory
	Now                   func() time.Time
	Logger                *slog.Logger
	Host                  HostSource
	Notice                func(string) error
	Ready                 func()
	ServiceIdentity       string
}

func RunLocal(ctx context.Context, opts LocalOptions) (runErr error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Host == nil {
		opts.Host = nativeHost{}
	}
	if ctx.Err() != nil {
		return nil
	}
	if opts.Role == "collector" {
		return runCollectorNode(ctx, opts)
	}
	if opts.Role != "" && opts.Role != "both" && opts.Role != "monitor" {
		return fmt.Errorf("运行角色必须是 both、collector 或 monitor")
	}
	directory := filepath.Join(opts.Home, "config")
	manager, err := config.NewManager(config.FSLoader{FS: os.DirFS(directory)}, opts.Logger)
	if err != nil {
		return err
	}
	if len(manager.Snapshot().Notifiers) == 0 && opts.Notice != nil {
		if err := opts.Notice("尚未配置飞书通知渠道，请运行 lgmon feishu setup 完成飞书接入"); err != nil {
			return fmt.Errorf("输出飞书接入提示失败：%w", err)
		}
	}
	id, err := (collector.FileIDStore{Home: opts.Home}).LoadOrCreate()
	if err != nil {
		return err
	}
	hostname, err := opts.Host.Hostname(ctx)
	if err != nil {
		return fmt.Errorf("读取本机名称失败：%w", err)
	}
	vault, err := secrets.New(opts.Home, opts.Logger)
	if err != nil {
		return err
	}
	watcher, err := config.NewFileWatcher(directory)
	if err != nil {
		return err
	}
	defer watcher.Close()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(opts.Home, "data", "lgmon.db"), Now: opts.Now, Logger: opts.Logger})
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil && runErr == nil {
			runErr = err
		}
	}()
	if opts.NewClient == nil {
		opts.NewClient = func(app config.FeishuApp, secret []byte) (feishu.Client, error) {
			return feishu.NewClient(app, secret, feishu.Options{Now: opts.Now, Logger: opts.Logger})
		}
	}
	return runMonitorNode(ctx, opts, id, hostname, manager, watcher, db, vault)
}
