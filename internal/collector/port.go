package collector

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

type connectionBackend interface {
	Connections(context.Context, string) ([]gnet.ConnectionStat, error)
}

type nativeConnections struct{}

func (nativeConnections) Connections(ctx context.Context, kind string) ([]gnet.ConnectionStat, error) {
	return gnet.ConnectionsWithContext(ctx, kind)
}

type portSource struct {
	instrumentation
	backend connectionBackend
}

func NewPortSource(now func() time.Time, logger *slog.Logger) PortSource {
	return newPortSource(nativeConnections{}, now, logger)
}

func newPortSource(backend connectionBackend, now func() time.Time, logger *slog.Logger) *portSource {
	return &portSource{instrumentation: newInstrumentation(now, logger), backend: backend}
}

func (s *portSource) ListeningTCP(ctx context.Context) ([]Port, error) {
	started := s.now()
	defer s.finish("port", started)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("端口采集已取消：%w", err)
	}
	connections, err := s.backend.Connections(ctx, "tcp")
	if err != nil {
		return nil, fmt.Errorf("读取 TCP 连接失败，请检查当前账户的网络表读取权限：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("端口采集已取消：%w", err)
	}
	result := make([]Port, 0, len(connections))
	for _, connection := range connections {
		if connection.Status == "LISTEN" {
			result = append(result, Port{LocalAddress: connection.Laddr.IP, Port: connection.Laddr.Port, PID: connection.Pid})
		}
	}
	slices.SortFunc(result, func(a, b Port) int {
		if order := cmp.Compare(a.LocalAddress, b.LocalAddress); order != 0 {
			return order
		}
		if order := cmp.Compare(a.Port, b.Port); order != 0 {
			return order
		}
		return cmp.Compare(a.PID, b.PID)
	})
	return result, nil
}
