package collector

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const MaxInventoryEntries = 2048
const MaxInventoryBytes = 768 * 1024

var errInventoryLimit = errors.New("清单超出限额")

type InventoryEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Enabled     bool   `json:"enabled"`
	State       string `json:"state"`
	LastRun     int64  `json:"lastRun"`
	NextRun     int64  `json:"nextRun"`
	LastResult  uint32 `json:"lastResult"`
	MissedRuns  uint32 `json:"missedRuns"`
}

type InventorySnapshot struct {
	Kind      string           `json:"kind"`
	Scope     string           `json:"scope"`
	Epoch     string           `json:"epoch,omitempty"`
	Entries   []InventoryEntry `json:"entries"`
	ErrorCode string           `json:"errorCode,omitempty"`
}

func (s InventorySnapshot) Validate() error {
	text := func(value string, max int) bool {
		return len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
	}
	if (s.Kind != "startup" && s.Kind != "tasks") || s.Scope == "" || !text(s.Scope, 512) || !text(s.Epoch, 64) {
		return fmt.Errorf("启动项或计划任务快照的类别、来源或指纹版本无效")
	}
	if s.ErrorCode != "" {
		if !slices.Contains([]string{"read_failed", "limit_exceeded", "source_unavailable", "unsupported"}, s.ErrorCode) || len(s.Entries) != 0 {
			return fmt.Errorf("清单错误快照不能带条目，且必须使用已知错误代码")
		}
		return nil
	}
	if len(s.Entries) > MaxInventoryEntries || (s.Kind == "startup" && len(s.Epoch) != 16) {
		return fmt.Errorf("清单条目过多或启动项指纹版本无效")
	}
	seen := map[string]bool{}
	for _, e := range s.Entries {
		id := strings.ToLower(e.ID)
		if id == "" || seen[id] || !text(e.ID, 4096) || !text(e.Name, 1024) || e.Name == "" || !text(e.State, 32) || e.LastRun < 0 || e.NextRun < 0 {
			return fmt.Errorf("清单条目的身份、名称、时间或重复身份无效")
		}
		seen[id] = true
		if s.Kind == "startup" {
			if value, err := hex.DecodeString(e.Fingerprint); err != nil || len(value) != sha256.Size || e.State != "registered" || e.Enabled || e.LastRun != 0 || e.NextRun != 0 || e.LastResult != 0 || e.MissedRuns != 0 {
				return fmt.Errorf("启动项仅允许登记状态和本机指纹")
			}
		} else if e.Fingerprint != "" || !slices.Contains([]string{"unknown", "disabled", "queued", "ready", "running"}, e.State) {
			return fmt.Errorf("计划任务状态无效或包含非白名单指纹")
		}
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > MaxInventoryBytes {
		return fmt.Errorf("清单快照超过 768 KiB 或无法编码")
	}
	return nil
}

type InventorySource interface {
	Snapshot(context.Context) ([]InventorySnapshot, error)
}

type inventoryScope struct{ kind, id string }
type inventoryBackend interface {
	Scopes(context.Context) ([]inventoryScope, error)
	Read(context.Context, inventoryScope) ([]InventoryEntry, error)
}
type inventorySource struct {
	backend inventoryBackend
	epoch   string
	seen    map[inventoryScope]bool
}

func NewInventorySource(key []byte) (InventorySource, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("启动项指纹需要 32 字节的本机密钥")
	}
	epoch := sha256.Sum256(key)
	return &inventorySource{backend: newInventoryBackend(slices.Clone(key)), epoch: hex.EncodeToString(epoch[:8]), seen: map[inventoryScope]bool{}}, nil
}

func inventoryFingerprint(key, value []byte) string {
	digest := hmac.New(sha256.New, key)
	digest.Write(value)
	return hex.EncodeToString(digest.Sum(nil))
}

func (s *inventorySource) Snapshot(ctx context.Context) ([]InventorySnapshot, error) {
	scopes, err := s.backend.Scopes(ctx)
	if err != nil {
		return nil, fmt.Errorf("枚举清单来源失败：%w", err)
	}
	current := map[inventoryScope]bool{}
	for _, scope := range scopes {
		if current[scope] {
			return nil, fmt.Errorf("清单来源重复")
		}
		current[scope] = true
	}
	for old := range s.seen {
		if !current[old] {
			scopes = append(scopes, old)
		}
	}
	slices.SortFunc(scopes, func(a, b inventoryScope) int { return strings.Compare(a.kind+":"+a.id, b.kind+":"+b.id) })
	result := make([]InventorySnapshot, 0, len(scopes))
	for _, scope := range scopes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshot := InventorySnapshot{Kind: scope.kind, Scope: scope.id, Entries: []InventoryEntry{}}
		if scope.kind == "startup" {
			snapshot.Epoch = s.epoch
		}
		if !current[scope] {
			snapshot.ErrorCode = "source_unavailable"
		} else {
			s.seen[scope] = true
			entries, err := s.backend.Read(ctx, scope)
			if err != nil {
				snapshot.ErrorCode = "read_failed"
				if errors.Is(err, errInventoryLimit) {
					snapshot.ErrorCode = "limit_exceeded"
				}
			} else {
				snapshot.Entries = entries
				slices.SortFunc(snapshot.Entries, func(a, b InventoryEntry) int { return strings.Compare(strings.ToLower(a.ID), strings.ToLower(b.ID)) })
				if err := snapshot.Validate(); err != nil {
					snapshot.Entries, snapshot.ErrorCode = []InventoryEntry{}, "limit_exceeded"
				}
			}
		}
		result = append(result, snapshot)
	}
	return result, nil
}
