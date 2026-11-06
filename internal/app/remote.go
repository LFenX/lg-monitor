package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
	}, db.LastCommitted)
	receiver.SetConfigDelivery(transport.ConfigDelivery{Source: manager, Logger: opts.Logger})
	receiver.SetHeartbeat(func(ctx context.Context, hello transport.Hello) error { return db.Heartbeat(ctx, hello.CollectorID) })
	defer receiver.Close()
	runtime, err := NewRuntime(RuntimeOptions{ID: id, Hostname: hostname, Version: opts.Version, Config: manager, Watcher: watcher, Store: db,
		Source: receiver, Secrets: vault, NewClient: opts.NewClient, Now: opts.Now, Logger: opts.Logger,
		Collectors: pairedPresence{pairs: pairing, db: db, link: receiver, now: opts.Now, startedAt: opts.Now().UnixMilli()}})
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
			err := runEmbeddedCollector(ctx, opts, id, hostname, identity)
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
	runErr := runEmbeddedCollector(parent, opts, id, hostname, identity)
	if parent.Err() != nil {
		return nil
	}
	return runErr
}

func runEmbeddedCollector(ctx context.Context, opts LocalOptions, id, hostname string, identity *transport.ClientIdentity) (runErr error) {
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(opts.Home, "data", "outbox.db"), CollectorID: id, Now: opts.Now})
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, box.Close()) }()
	manager, err := transport.NewCachedTargets(ctx, transport.FileTargetCache{Home: opts.Home}, opts.Logger)
	if err != nil {
		return err
	}
	delivery, err := transport.NewDelivery(transport.DeliveryOptions{Outbox: box, Identity: identity, Hostname: hostname, Version: opts.Version, Logger: opts.Logger, Config: manager})
	if err != nil {
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	prober := collector.NewProber(opts.Now)
	defer prober.Close()
	local, err := NewCollector(CollectorOptions{ID: id, Config: manager, Processes: collector.NewProcessSource(opts.Now, opts.Logger), Ports: collector.NewPortSource(opts.Now, opts.Logger), Probes: prober, Sink: box, Now: opts.Now, Logger: opts.Logger})
	if err != nil {
		return err
	}
	delivered := make(chan error, 1)
	go func() {
		err := delivery.Run(workCtx)
		delivered <- err
		if err != nil {
			cancel()
		}
	}()
	err = local.Run(workCtx)
	cancel()
	deliveryErr := <-delivered
	if ctx.Err() != nil {
		return nil
	}
	return errors.Join(err, deliveryErr)
}
