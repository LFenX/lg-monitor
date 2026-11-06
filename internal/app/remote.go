package app

import (
	"context"
	"encoding/json"
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
	state := transport.FileState{Home: opts.Home}
	authority, err := transport.NewAuthority(parent, transport.AuthorityOptions{Keys: vault, State: state, SANs: transport.NativeSANs{}, Now: opts.Now})
	if err != nil {
		return err
	}
	pairing, err := transport.OpenPairDB(parent, filepath.Join(opts.Home, "data", "pairing.db"))
	if err != nil {
		return err
	}
	defer pairing.Close()
	localAddress := listener.Addr().(*net.TCPAddr)
	localIP := localAddress.IP.String()
	if localAddress.IP.IsUnspecified() {
		localIP = "127.0.0.1"
	}
	endpoint := net.JoinHostPort(localIP, fmt.Sprint(localAddress.Port))
	endpointData, err := json.Marshal(struct {
		Address string `json:"address"`
	}{Address: endpoint})
	if err != nil {
		return fmt.Errorf("编码监控器本机地址失败：%w", err)
	}
	if err := state.Save(parent, "endpoint", endpointData, true); err != nil {
		return err
	}
	var identity *transport.ClientIdentity
	if opts.Role != "monitor" {
		identity, err = transport.SelfPair(parent, authority, pairing, state, id, hostname, endpoint)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	receiver, server := transport.NewSecureServer(authority, pairing, func(ctx context.Context, hello transport.Hello) error {
		at := opts.Now().UnixMilli()
		return db.RegisterCollector(ctx, store.CollectorRecord{ID: hello.CollectorID, Hostname: hello.Hostname, Version: hello.Version, FirstSeen: at, LastSeen: at})
	})
	defer receiver.Close()
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
	localDone := make(chan error, 1)
	if identity != nil {
		go func() {
			err := runEmbeddedCollector(ctx, opts, id, hostname, manager, identity)
			if err != nil {
				opts.Logger.Error("Local collector stopped; monitor continues accepting remote collectors")
			}
			localDone <- err
		}()
	} else {
		localDone <- nil
	}
	opts.Logger.Info("Collector listener uses mTLS; pairing requires a one-time code and CA fingerprint", "listen", listener.Addr().String())
	runErr := runtime.Run(ctx)
	cancel()
	<-stopped
	serveErr := <-served
	localErr := <-localDone
	if errors.Is(serveErr, grpc.ErrServerStopped) {
		serveErr = nil
	}
	if parent.Err() != nil {
		localErr = nil
	}
	return errors.Join(runErr, serveErr, localErr)
}

func runCollectorNode(parent context.Context, opts LocalOptions) error {
	vault, err := secrets.New(opts.Home, opts.Logger)
	if err != nil {
		return err
	}
	identity, err := transport.LoadClient(parent, transport.FileState{Home: opts.Home}, vault, opts.Now)
	if err != nil {
		return err
	}
	if opts.Monitor != "" {
		identity.Monitor = opts.Monitor
	}
	directory := filepath.Join(opts.Home, "config")
	manager, err := config.NewManager(config.FSLoader{FS: os.DirFS(directory)}, opts.Logger)
	if err != nil {
		return err
	}
	id, err := (collector.FileIDStore{Home: opts.Home}).LoadOrCreate()
	if err != nil {
		return err
	}
	if id != identity.ID {
		return fmt.Errorf("采集器身份文件与配对证书不符，请恢复原身份文件")
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
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	watched := make(chan error, 1)
	go func() { watched <- manager.Watch(ctx, watcher); cancel() }()
	runErr := runEmbeddedCollector(ctx, opts, id, hostname, manager, identity)
	cancel()
	watchErr := <-watched
	if parent.Err() != nil {
		return nil
	}
	return errors.Join(runErr, watchErr)
}

func runEmbeddedCollector(ctx context.Context, opts LocalOptions, id, hostname string, manager *config.Manager, identity *transport.ClientIdentity) error {
	connection, err := (transport.NativeConnector{}).Connect(ctx, identity.Monitor, identity.TLS())
	if err != nil {
		return err
	}
	defer connection.Close()
	writer, err := transport.NewWriter(ctx, connection, transport.Hello{CollectorID: id, Hostname: hostname, Version: opts.Version})
	if err != nil {
		if ctx.Err() != nil {
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
	err = local.Run(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
