package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/transport"
	"github.com/spf13/cobra"
)

type monitorStatus struct {
	ID              string               `json:"id"`
	Enabled         bool                 `json:"enabled"`
	AllowOperations bool                 `json:"allowOperations"`
	Address         string               `json:"address,omitempty"`
	Fingerprint     string               `json:"fingerprint,omitempty"`
	IdentityError   string               `json:"identityError,omitempty"`
	Cache           *pipeline.CacheUsage `json:"cache"`
	CacheMessage    string               `json:"cacheMessage,omitempty"`
}

func newMonitorStatusCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{Use: "monitors", Short: "读取本机监控器身份和独立缓存；配置修改重启生效", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		home, _ := cmd.Flags().GetString("home")
		settings, err := config.LoadSettings(os.DirFS(filepath.Join(home, "config")))
		if err != nil {
			return err
		}
		vault, err := secrets.New(home, slog.Default())
		if err != nil {
			return err
		}
		id, idErr := os.ReadFile(filepath.Join(home, "data", "collector-id"))
		monitors := append([]config.CollectorMonitor{{}}, settings.CollectorMonitors...)
		result := struct {
			Monitors           []monitorStatus `json:"monitors"`
			MaxBytesPerMonitor int64           `json:"maxBytesPerMonitor"`
			TotalMaxBytes      int64           `json:"totalMaxBytes"`
			TotalPhysicalBytes int64           `json:"totalPhysicalBytes"`
		}{Monitors: []monitorStatus{}}
		result.MaxBytesPerMonitor, err = settings.Cache.MaxBytes()
		if err != nil {
			return err
		}
		for _, monitor := range monitors {
			name := monitor.ID
			if name == "" {
				name = "default"
			}
			item := monitorStatus{ID: name, Enabled: monitor.IsEnabled(), AllowOperations: monitor.ID == "" || monitor.AllowOperations}
			if item.Enabled {
				result.TotalMaxBytes += result.MaxBytesPerMonitor
			}
			identity, err := transport.LoadClient(cmd.Context(), transport.FileState{Home: home, Monitor: monitor.ID}, vault, time.Now)
			if err != nil {
				item.IdentityError = err.Error()
			} else {
				item.Address = identity.Monitor
				item.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(identity.CA.Raw))
				if idErr != nil {
					item.IdentityError = "无法读取根采集器身份：" + idErr.Error()
				} else if strings.TrimSpace(string(id)) != identity.ID {
					item.IdentityError = "配对证书与根采集器身份不符"
				}
			}
			directory, err := transport.MonitorDataDirectory(home, monitor.ID)
			if err != nil {
				return err
			}
			usage, limit, err := transport.ReadCacheUsage(cmd.Context(), filepath.Join(directory, "outbox.db"))
			if err == nil {
				item.Cache = &usage
				result.TotalPhysicalBytes += usage.Bytes
				item.CacheMessage = limit.Message
			} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, transport.ErrCacheStatsUnavailable) {
				item.CacheMessage = err.Error()
			}
			result.Monitors = append(result.Monitors, item)
		}
		if *asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		for _, item := range result.Monitors {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t启用=%t\t槽位操作授权=%t\t%s\t%s\t%s\n", item.ID, item.Enabled, item.AllowOperations, item.Address, item.IdentityError, item.CacheMessage); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "每槽上限=%d；启用槽位总预算=%d；全部已配置槽位实际缓存=%d 字节（重启生效）\n", result.MaxBytesPerMonitor, result.TotalMaxBytes, result.TotalPhysicalBytes)
		return err
	}}
}
