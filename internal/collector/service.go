package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

const (
	ServiceRunning = "running"
	ServiceStopped = "stopped"
	ServiceUnknown = "unknown"
	ServiceMissing = "missing"
	ServiceDeleted = "deleted"
)

var errServiceMissing = errors.New("Windows 服务不存在")

type Service struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
	StartType   string `json:"startType"`
	PID         int32  `json:"pid"`
	Error       string `json:"error,omitempty"`
}

func (s Service) Validate() error {
	if strings.TrimSpace(s.Name) == "" || s.PID < 0 {
		return fmt.Errorf("服务名称或进程 ID 无效")
	}
	switch s.State {
	case ServiceRunning, ServiceStopped, ServiceUnknown, ServiceMissing, ServiceDeleted, "start_pending", "stop_pending", "paused", "pause_pending", "continue_pending":
	default:
		return fmt.Errorf("服务状态无效")
	}
	switch s.StartType {
	case "automatic", "automatic_delayed", "manual", "disabled", "boot", "system", "unknown":
	default:
		return fmt.Errorf("服务启动类型无效")
	}
	return nil
}

type ServiceSource interface {
	Snapshot(context.Context) ([]Service, error)
	Lookup(context.Context, []string) ([]Service, error)
}

type serviceSession interface {
	Names(context.Context) ([]string, error)
	Read(context.Context, string) (Service, error)
	Close() error
}
type serviceBackend interface {
	Open(context.Context, bool) (serviceSession, error)
}
type nativeServices struct{}
type serviceSource struct {
	instrumentation
	backend serviceBackend
}

func NewServiceSource(now func() time.Time, logger *slog.Logger) ServiceSource {
	return newServiceSource(nativeServices{}, now, logger)
}
func newServiceSource(backend serviceBackend, now func() time.Time, logger *slog.Logger) *serviceSource {
	return &serviceSource{instrumentation: newInstrumentation(now, logger), backend: backend}
}

func (s *serviceSource) Snapshot(ctx context.Context) ([]Service, error) {
	return s.read(ctx, nil, true)
}
func (s *serviceSource) Lookup(ctx context.Context, names []string) ([]Service, error) {
	return s.read(ctx, slices.Clone(names), false)
}

func (s *serviceSource) read(ctx context.Context, names []string, enumerate bool) ([]Service, error) {
	started := s.now()
	defer s.finish("Windows services", started)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("服务采集已取消：%w", err)
	}
	session, err := s.backend.Open(ctx, enumerate)
	if err != nil {
		return nil, fmt.Errorf("只读连接服务管理器失败：%w", err)
	}
	defer session.Close()
	if enumerate {
		names, err = session.Names(ctx)
		if err != nil {
			return nil, fmt.Errorf("枚举 Windows 服务失败：%w", err)
		}
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	rows := make([]Service, 0, len(names))
	seen := make(map[string]bool)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("服务采集已取消：%w", err)
		}
		key := strings.ToLower(name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		row, err := session.Read(ctx, name)
		if err != nil {
			row = Service{Name: name, DisplayName: name, State: ServiceUnknown, StartType: "unknown", Error: "服务状态读取失败，请检查访问权限"}
			if errors.Is(err, errServiceMissing) {
				row.State, row.Error = ServiceMissing, "服务不存在"
			}
		}
		if err := row.Validate(); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
