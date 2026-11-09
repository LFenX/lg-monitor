package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

var ErrOperationMissing = errors.New("操作记录不存在")
var ErrOperationConflict = errors.New("相同请求 ID 的内容不一致")
var ErrRestartUncertain = errors.New("服务控制结果未知")

type OperationPolicy struct {
	Enabled  bool                  `json:"enabled"`
	Version  string                `json:"version"`
	Services []config.ServiceGrant `json:"services"`
	Reason   string                `json:"reason"`
}
type OperationCommand struct {
	RequestID string `json:"requestId"`
	GrantID   string `json:"grantId"`
	Version   string `json:"version"`
	ExpiresAt int64  `json:"expiresAt"`
}
type OperationResult struct {
	RequestID  string `json:"requestId"`
	GrantID    string `json:"grantId"`
	Version    string `json:"version"`
	Service    string `json:"service"`
	State      string `json:"state" jsonschema:"enum=running,enum=succeeded,enum=failed,enum=denied,enum=unknown"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	Message    string `json:"message"`
}
type OperationPolicySource interface {
	ReadOperationPolicy(context.Context) (config.RemoteOperations, error)
}
type ServiceRestartExecutor interface {
	Restart(context.Context, string) error
}
type OperationJournal interface {
	Claim(context.Context, OperationCommand, string, int64) (OperationResult, bool, error)
	Result(context.Context, string) (OperationResult, error)
	Finish(context.Context, OperationResult) error
	Close() error
}
type ServiceOperationsOptions struct {
	Policy   OperationPolicySource
	Executor ServiceRestartExecutor
	Journal  OperationJournal
	Now      func() time.Time
	Lifetime context.Context
}
type ServiceOperations struct {
	options       ServiceOperationsOptions
	mu            sync.Mutex
	closed        bool
	activeRequest string
	active        sync.WaitGroup
	busy          chan struct{}
}

var operationRequestPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var operationVersionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidOperationRequestID(id string) bool    { return operationRequestPattern.MatchString(id) }
func ValidOperationVersion(version string) bool { return operationVersionPattern.MatchString(version) }
func (c OperationCommand) Hash() string {
	data, _ := json.Marshal(struct{ RequestID, GrantID, Version string }{c.RequestID, c.GrantID, c.Version})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func NewServiceOperations(options ServiceOperationsOptions) (*ServiceOperations, error) {
	if options.Policy == nil || options.Executor == nil || options.Journal == nil || options.Now == nil || options.Lifetime == nil {
		return nil, fmt.Errorf("服务操作需要本机策略、执行器、持久账本、时钟和生命周期")
	}
	return &ServiceOperations{options: options, busy: make(chan struct{}, 1)}, nil
}

func (s *ServiceOperations) Policy(ctx context.Context) (OperationPolicy, error) {
	policy, err := s.options.Policy.ReadOperationPolicy(ctx)
	if err != nil || policy.Validate() != nil {
		return OperationPolicy{Services: []config.ServiceGrant{}, Reason: "本机操作策略不可用，请由采集器管理员检查配置、服务身份及受保护路径"}, nil
	}
	result := OperationPolicy{Enabled: policy.Enabled, Version: policy.Version(), Services: []config.ServiceGrant{}, Reason: "远程服务操作未启用"}
	if policy.Enabled {
		result.Services = slices.Clone(policy.Services)
		result.Reason = "仅允许列出的精确服务重启，每次需要管理员确认"
	}
	return result, nil
}

func (s *ServiceOperations) Result(ctx context.Context, id string) (OperationResult, error) {
	if !ValidOperationRequestID(id) {
		return OperationResult{}, ErrOperationMissing
	}
	result, err := s.options.Journal.Result(ctx, id)
	if err == nil && result.State == "running" {
		s.mu.Lock()
		active := s.activeRequest == id
		s.mu.Unlock()
		if !active {
			result.State, result.Message = "unknown", "采集器没有确认该操作的终态，请人工核对；不得自动重试"
		}
	}
	return result, err
}

func (s *ServiceOperations) Execute(ctx context.Context, command OperationCommand) OperationResult {
	result := OperationResult{RequestID: command.RequestID, GrantID: command.GrantID, Version: command.Version, State: "denied", Message: "操作请求、授权或有效期无效"}
	if !ValidOperationRequestID(command.RequestID) || !ValidOperationVersion(command.Version) || command.GrantID == "" || ctx.Err() != nil {
		return result
	}
	if previous, err := s.Result(ctx, command.RequestID); err == nil {
		// Claim compares the immutable command even when policy has since been revoked.
		stored, _, claimErr := s.options.Journal.Claim(ctx, command, previous.Service, s.options.Now().UnixMilli())
		if claimErr == nil {
			if previous.State == "unknown" && stored.State == "running" {
				stored.State, stored.Message = previous.State, previous.Message
			}
			return stored
		}
		result.Message = "相同请求 ID 的内容不一致或账本不可用"
		return result
	} else if !errors.Is(err, ErrOperationMissing) {
		result.Message = "采集器操作账本不可用，没有执行服务操作"
		return result
	}
	now := s.options.Now().UnixMilli()
	if command.ExpiresAt <= now || command.ExpiresAt > now+60000 {
		return result
	}
	select {
	case s.busy <- struct{}{}:
		defer func() { <-s.busy }()
	default:
		result.Message = "采集器有一项服务操作尚未结束"
		return result
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		result.Message = "采集器正在退出"
		return result
	}
	s.active.Add(1)
	s.activeRequest = command.RequestID
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.activeRequest = ""; s.mu.Unlock(); s.active.Done() }()
	policy, err := s.options.Policy.ReadOperationPolicy(ctx)
	if err != nil || policy.Validate() != nil || !policy.Enabled || policy.Version() != command.Version {
		return result
	}
	index := slices.IndexFunc(policy.Services, func(grant config.ServiceGrant) bool { return grant.ID == command.GrantID })
	if index < 0 {
		return result
	}
	result.Service = policy.Services[index].Service
	stored, fresh, err := s.options.Journal.Claim(ctx, command, result.Service, now)
	if err != nil {
		result.Message = "保存采集器操作意图失败，没有执行服务操作"
		return result
	}
	if !fresh {
		return stored
	}
	result = stored
	current, err := s.options.Policy.ReadOperationPolicy(ctx)
	if err != nil || !current.Enabled || current.Version() != command.Version || ctx.Err() != nil || s.options.Now().UnixMilli() >= command.ExpiresAt {
		result.State, result.Message = "denied", "本机授权、请求有效期或连接状态已变化，没有执行服务操作"
	} else {
		work, cancel := context.WithTimeout(s.options.Lifetime, 30*time.Second)
		err = s.options.Executor.Restart(work, result.Service)
		cancel()
		result.State, result.Message = "succeeded", "已核对服务处于运行状态"
		if err != nil {
			result.State, result.Message = "failed", "服务控制未完成，请由采集器管理员核对服务状态"
			if errors.Is(err, ErrRestartUncertain) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				result.State, result.Message = "unknown", "服务控制结果未知，请人工核对后再决定下一次操作"
			}
		}
	}
	result.FinishedAt = s.options.Now().UnixMilli()
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(s.options.Lifetime), 2*time.Second)
	defer cancel()
	if err := s.options.Journal.Finish(finishCtx, result); err != nil {
		result.State, result.Message = "unknown", "服务效果后的采集器审计未确认，请人工核对；不得自动重试"
	}
	return result
}

func (s *ServiceOperations) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.active.Wait()
	return s.options.Journal.Close()
}
