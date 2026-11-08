package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func (b *APIBackend) ruleErrors(ctx context.Context) ([]api.RuleError, error) {
	source, ok := b.options.Read.(interface {
		RecentRuleErrors(context.Context) ([]rules.RuleError, error)
	})
	if !ok {
		return nil, fmt.Errorf("当前存储不支持读取规则错误")
	}
	rows, err := source.RecentRuleErrors(ctx)
	if err != nil {
		return nil, err
	}
	configured := map[string]config.Rule{}
	for _, rule := range b.options.Config.Snapshot().Rules {
		configured[rule.ID] = rule
	}
	result := []api.RuleError{}
	for _, row := range rows {
		rule, present := configured[row.RuleID]
		if !present {
			continue
		}
		result = append(result, api.RuleError{RuleID: row.RuleID, RuleName: rule.Name, RuleType: rule.Type, Error: row.Error, At: row.At, Consecutive: row.Consecutive, Active: row.Active})
	}
	slices.SortFunc(result, func(a, b api.RuleError) int {
		if a.Active != b.Active {
			if a.Active {
				return -1
			}
			return 1
		}
		if a.At != b.At {
			if a.At > b.At {
				return -1
			}
			return 1
		}
		return strings.Compare(a.RuleID, b.RuleID)
	})
	return result, nil
}

func watchRuleErrorEvents(ctx context.Context, backend *APIBackend, source RuntimeConfig, changes <-chan struct{}, publisher LivePublisher, logger *slog.Logger) {
	var previous []byte
	publish := func() {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		rows, err := backend.ruleErrors(readCtx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("Rule errors could not be read for live update")
			}
			return
		}
		data, err := json.Marshal(rows)
		if err == nil && !bytes.Equal(previous, data) {
			if err := publisher.Publish("ruleErrors", rows); err == nil {
				previous = data
			}
		}
	}
	publish()
	updates := source.Subscribe(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-changes:
				publish()
			case _, open := <-updates:
				if !open {
					return
				}
				publish()
			}
		}
	}()
}
