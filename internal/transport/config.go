package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/protobuf/proto"
)

type ConfigSource interface {
	Subscribe(context.Context) <-chan *config.Config
}

type ConfigApplier interface {
	Apply(context.Context, string, []byte) error
}

type TargetCacheStore interface {
	Load(context.Context) (string, []byte, error)
	Save(context.Context, string, []byte) error
}

type CachedTargets struct {
	mu      sync.Mutex
	store   TargetCacheStore
	manager *config.Manager
	version string
	logger  *slog.Logger
}

type initialTargets struct{ cfg *config.Config }

func (l initialTargets) Load() (*config.Config, error) { return l.cfg, nil }

func NewCachedTargets(ctx context.Context, storage TargetCacheStore, logger *slog.Logger) (*CachedTargets, error) {
	if storage == nil {
		return nil, fmt.Errorf("采集配置需要本地缓存")
	}
	if logger == nil {
		logger = slog.Default()
	}
	version, data, err := storage.Load(ctx)
	cfg := &config.Config{}
	if errors.Is(err, os.ErrNotExist) {
		version = ""
	} else if err != nil {
		return nil, fmt.Errorf("读取采集配置缓存失败：%w", err)
	} else {
		cfg, err = config.ParseTargetConfig(version, data)
		if err != nil {
			return nil, fmt.Errorf("采集配置缓存已损坏，请恢复有效缓存；在线重新获取前请移走损坏的缓存文件：%w", err)
		}
	}
	manager, err := config.NewManager(initialTargets{cfg}, logger)
	if err != nil {
		return nil, err
	}
	return &CachedTargets{store: storage, manager: manager, version: version, logger: logger}, nil
}

func (c *CachedTargets) Subscribe(ctx context.Context) <-chan *config.Config {
	return c.manager.Subscribe(ctx)
}

func (c *CachedTargets) Snapshot() *config.Config { return c.manager.Snapshot() }

func (c *CachedTargets) Apply(ctx context.Context, version string, data []byte) (applyErr error) {
	defer func() {
		if applyErr != nil {
			c.logger.Error("Collector configuration rejected; keeping the previous snapshot", "error", applyErr)
		}
	}()
	if len(data) > MaxMessageSize-1024 {
		return fmt.Errorf("采集配置超过消息上限")
	}
	cfg, err := config.ParseTargetConfig(version, data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return fmt.Errorf("应用采集配置已取消：%w", ctx.Err())
	}
	if version == c.version {
		return nil
	}
	if err := c.store.Save(ctx, version, data); err != nil {
		return fmt.Errorf("保存采集配置失败，已保留旧配置：%w", err)
	}
	if err := c.manager.Apply(cfg); err != nil {
		return err
	}
	c.version = version
	return nil
}

type FileTargetCache struct{ Home, Monitor string }

type targetCacheDocument struct {
	Version string          `json:"version"`
	Targets json.RawMessage `json:"targets"`
}

func (f FileTargetCache) path() (string, error) {
	directory, err := MonitorDataDirectory(f.Home, f.Monitor)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "collector-config", "targets.json"), nil
}

func (f FileTargetCache) Load(ctx context.Context) (string, []byte, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, fmt.Errorf("读取采集配置已取消：%w", err)
	}
	path, err := f.path()
	if err != nil {
		return "", nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("打开采集配置缓存失败：%w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxMessageSize+1))
	if err != nil {
		return "", nil, fmt.Errorf("读取采集配置缓存失败：%w", err)
	}
	if len(data) > MaxMessageSize {
		return "", nil, fmt.Errorf("采集配置缓存超过消息上限")
	}
	var saved targetCacheDocument
	if err := json.Unmarshal(data, &saved); err != nil {
		return "", nil, fmt.Errorf("解析采集配置缓存失败：%w", err)
	}
	return saved.Version, saved.Targets, nil
}

func (f FileTargetCache) Save(ctx context.Context, version string, targets []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存采集配置已取消：%w", err)
	}
	data, err := json.Marshal(targetCacheDocument{Version: version, Targets: targets})
	if err != nil {
		return fmt.Errorf("编码采集配置缓存失败：%w", err)
	}
	if len(data) > MaxMessageSize {
		return fmt.Errorf("采集配置缓存超过消息上限")
	}
	path, err := f.path()
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return fmt.Errorf("创建采集数据目录失败：%w", err)
	}
	created := os.Mkdir(directory, 0o700)
	if created != nil && !errors.Is(created, os.ErrExist) {
		return fmt.Errorf("创建采集配置缓存目录失败：%w", created)
	}
	if err := secureStateDirectory(directory, created == nil); err != nil {
		return fmt.Errorf("保护采集配置缓存目录失败：%w", err)
	}
	file, err := os.CreateTemp(directory, ".targets-*")
	if err != nil {
		return fmt.Errorf("创建采集配置临时文件失败：%w", err)
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("写入采集配置缓存失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("刷新采集配置缓存失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭采集配置缓存失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发布采集配置缓存已取消：%w", err)
	}
	if err := publishState(file.Name(), path, true); err != nil {
		return fmt.Errorf("发布采集配置缓存失败：%w", err)
	}
	return nil
}

type ConfigDelivery struct {
	Source ConfigSource
	Clock  collector.ProbeClock
	Logger *slog.Logger
}

func (r *Receiver) SetConfigDelivery(delivery ConfigDelivery) {
	if delivery.Clock == nil {
		delivery.Clock = collector.NewProbeClock()
	}
	if delivery.Logger == nil {
		delivery.Logger = slog.Default()
	}
	r.config = delivery
}

func (r *Receiver) pushConfig(ctx context.Context, session *linkSession, send func(*lgmonv1.MonitorMessage) error, applied <-chan *lgmonv1.ConfigApplied) error {
	identity := session.identity
	updates := r.config.Source.Subscribe(ctx)
	ticker := r.config.Clock.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var latest *lgmonv1.ConfigPush
	acknowledged := false
	unsupported := false
	push := func() error {
		if latest == nil || acknowledged || unsupported {
			return nil
		}
		return send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_Config{Config: latest}})
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case snapshot, ok := <-updates:
			if !ok {
				return nil
			}
			version, data, err := config.TargetConfig(snapshot, identity.CollectorID, identity.Hostname)
			if err != nil {
				return err
			}
			if latest != nil && latest.Version == version {
				continue
			}
			latest = &lgmonv1.ConfigPush{Version: version, TargetsJson: data}
			unsupported = false
			if message := missingConfigCapability(snapshot, identity); message != "" {
				unsupported = true
				r.setConfigState(session, CollectorConfigState{State: "outdated", Version: version, Message: message})
				continue
			}
			if proto.Size(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_Config{Config: latest}}) > MaxMessageSize || len(data) > MaxMessageSize-1024 {
				return fmt.Errorf("下发采集配置超过 4 MiB 上限，请减少分配给同一采集器的目标")
			}
			acknowledged = false
			r.setConfigState(session, CollectorConfigState{State: "pending", Version: version, Message: "等待采集器确认配置"})
			if err := push(); err != nil {
				return err
			}
		case result := <-applied:
			if latest == nil || unsupported || result.Version != latest.Version {
				continue
			}
			acknowledged = result.Ok
			if !result.Ok {
				r.setConfigState(session, CollectorConfigState{State: "rejected", Version: latest.Version, Message: result.Error})
				r.config.Logger.Warn("Collector rejected configuration; retrying", "collectorId", identity.CollectorID, "version", latest.Version)
			} else {
				r.setConfigState(session, CollectorConfigState{State: "applied", Version: latest.Version, Message: "采集器已应用配置"})
				r.config.Logger.Info("Collector applied configuration", "collectorId", identity.CollectorID, "version", latest.Version)
			}
		case <-ticker.Ticks():
			if err := push(); err != nil {
				return err
			}
		}
	}
}
