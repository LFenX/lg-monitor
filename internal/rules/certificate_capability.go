package rules

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type CollectorCapabilities interface {
	Capabilities(string) ([]string, bool)
}

type RuleErrorPruner interface {
	PruneRuleErrors(context.Context, []string) error
}

func (e *Engine) pruneRuleErrors(ctx context.Context) error {
	if !e.rulePrunePending {
		return nil
	}
	pruner, ok := e.repository.(RuleErrorPruner)
	if !ok {
		e.rulePrunePending = false
		return nil
	}
	ids := make([]string, 0, len(e.specs))
	for _, spec := range e.specs {
		ids = append(ids, spec.rule.ID)
	}
	err := pruner.PruneRuleErrors(ctx, ids)
	if err == nil {
		e.rulePrunePending = false
	}
	return err
}

func (e *Engine) checkCertificateCapabilities(ctx context.Context, now time.Time) error {
	source, supported := e.collectorSource.(CollectorCapabilities)
	recorder, persistent := e.repository.(RuleErrorRecorder)
	if !supported || !persistent || e.replay {
		return nil
	}
	var failures []error
	active := map[string]bool{}
	for _, spec := range e.specs {
		if spec.rule.Type != "cert_expiry" {
			continue
		}
		active[spec.rule.ID] = true
		missing := []string{}
		unknown := false
		for id, item := range e.presence {
			if !spec.target.AppliesTo(id, item.Hostname) {
				continue
			}
			capabilities, known := source.Capabilities(id)
			if !known {
				unknown = true
			} else if !slices.Contains(capabilities, "http_checks") {
				missing = append(missing, id)
			}
		}
		// A never-connected collector has not advertised its version; retain the previous diagnosis.
		if len(missing) == 0 && unknown {
			continue
		}
		slices.Sort(missing)
		message := ""
		if len(missing) != 0 {
			message = fmt.Sprintf("采集器 %s 版本过旧，不上报证书到期时间，请升级", strings.Join(missing, "、"))
		}
		previous, exists := e.capabilityErrors[spec.rule.ID]
		if exists && previous.Active == (message != "") && (!previous.Active || previous.Error == message) {
			continue
		}
		status := RuleError{RuleID: spec.rule.ID, Error: message, At: now.UnixMilli(), Active: message != ""}
		if err := recorder.RecordRuleEvaluation(ctx, status); err != nil {
			failures = append(failures, err)
			continue
		}
		if message == "" && exists {
			status.Error, status.At = previous.Error, previous.At
		}
		e.capabilityErrors[spec.rule.ID] = status
	}
	for id := range e.capabilityErrors {
		if !active[id] {
			delete(e.capabilityErrors, id)
		}
	}
	return errors.Join(failures...)
}
