package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func runMonitorNode(parent context.Context, opts LocalOptions, id, hostname string, manager *config.Manager, watcher config.ChangeSource, db *store.Store, vault secrets.Store) error {
	address := opts.Listen
	if address == "" {
		address = ":7443"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("监听采集连接失败：%w", err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	receiver := transport.NewReceiver(func(ctx context.Context, hello transport.Hello) error {
		at := opts.Now().UnixMilli()
		return db.RegisterCollector(ctx, store.CollectorRecord{ID: hello.CollectorID, Hostname: hello.Hostname, Version: hello.Version, FirstSeen: at, LastSeen: at})
	})
	defer receiver.Close()
	server := transport.NewServer(receiver)
	runtime, err := NewRuntime(RuntimeOptions{ID: id, Hostname: hostname, Version: opts.Version, Config: manager, Watcher: watcher, Store: db,
		Source: receiver, Secrets: vault, NewClient: opts.NewClient, Now: opts.Now, Logger: opts.Logger})
	if err != nil {
		return err
	}
	served, stopped := make(chan error, 1), make(chan struct{})
	go func() { served <- server.Serve(listener); cancel() }()
	go func() {
		defer close(stopped)
		<-ctx.Done()
		server.Stop()
		receiver.Close()
	}()
	opts.Logger.Warn("M1 transport accepts loopback collectors only until mTLS pairing is configured", "listen", listener.Addr().String())
	runErr := runtime.Run(ctx)
	cancel()
	<-stopped
	serveErr := <-served
	if errors.Is(serveErr, grpc.ErrServerStopped) {
		serveErr = nil
	}
	return errors.Join(runErr, serveErr)
}

func runCollectorNode(parent context.Context, opts LocalOptions) error {
	address := opts.Monitor
	if address == "" {
		address = "127.0.0.1:7443"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("监控器地址必须为主机:端口：%w", err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("尚未配置 mTLS，请先用回环地址验证；跨机配对将在 M1-02 启用")
	}
	address = net.JoinHostPort(host, port)
	directory := filepath.Join(opts.Home, "config")
	manager, err := config.NewManager(config.FSLoader{FS: os.DirFS(directory)}, opts.Logger)
	if err != nil {
		return err
	}
	id, err := (collector.FileIDStore{Home: opts.Home}).LoadOrCreate()
	if err != nil {
		return err
	}
	hostname, err := opts.Host.Hostname(parent)
	if err != nil {
		return fmt.Errorf("读取采集主机名失败：%w", err)
	}
	watcher, err := config.NewFileWatcher(directory)
	if err != nil {
		return err
	}
	defer watcher.Close()
	connection, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("创建本机采集通道失败：%w", err)
	}
	defer connection.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	writer, err := transport.NewWriter(ctx, connection, transport.Hello{CollectorID: id, Hostname: hostname, Version: opts.Version})
	if err != nil {
		if parent.Err() != nil {
			return nil
		}
		return err
	}
	defer writer.Close()
	prober := collector.NewProber(opts.Now)
	defer prober.Close()
	local, err := NewCollector(CollectorOptions{ID: id, Config: manager, Processes: collector.NewProcessSource(opts.Now, opts.Logger), Ports: collector.NewPortSource(opts.Now, opts.Logger), Probes: prober, Sink: writer, Now: opts.Now, Logger: opts.Logger})
	if err != nil {
		return err
	}
	watched := make(chan error, 1)
	go func() { watched <- manager.Watch(ctx, watcher); cancel() }()
	runErr := local.Run(ctx)
	cancel()
	watchErr := <-watched
	if parent.Err() != nil {
		return nil
	}
	return errors.Join(runErr, watchErr)
}
