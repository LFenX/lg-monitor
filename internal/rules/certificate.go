package rules

import (
	"fmt"
	"math"
	"slices"
	"time"
)

type Certificate struct {
	NotAfter int64  `json:"notAfter"`
	Subject  string `json:"subject"`
}

func (c Certificate) DaysLeft(now time.Time) int64 {
	return int64(math.Ceil((float64(c.NotAfter) - float64(now.UnixMilli())) / float64((24 * time.Hour).Milliseconds())))
}

func (c Certificate) Message(now time.Time) string {
	date := time.UnixMilli(c.NotAfter).In(now.Location()).Format("2006-01-02 15:04:05")
	if c.NotAfter <= now.UnixMilli() {
		return fmt.Sprintf("证书已于（%s）过期", date)
	}
	return fmt.Sprintf("证书将在 %d 天后到期（%s）", c.DaysLeft(now), date)
}

func CertificateInstanceKey(collector, target string, probe int) string {
	return fmt.Sprintf("%s/%s/probe/%d", collector, target, probe)
}

type evaluation struct {
	identity   key
	met, known bool
	reason     string
}

func (e *Engine) conditions(spec ruleSpec, collector string, now time.Time) []evaluation {
	if spec.rule.Type != "cert_expiry" {
		met, known, reason := e.condition(spec, collector)
		return []evaluation{{key{spec.rule.ID, collector, spec.target.ID, ""}, met, known, reason}}
	}
	within, _ := spec.rule.CertWithin()
	result := make([]evaluation, 0, len(spec.target.Probes))
	for _, index := range spec.rule.CertificateProbes(spec.target) {
		view := e.probes[targetKey{collector, spec.target.ID}][index]
		known := view.certKnown && view.cert != nil
		next := evaluation{identity: key{spec.rule.ID, collector, spec.target.ID, CertificateInstanceKey(collector, spec.target.ID, index)}, known: known}
		if known {
			next.met, next.reason = view.cert.NotAfter <= now.Add(within).UnixMilli(), view.cert.Message(now)
		}
		result = append(result, next)
	}
	return result
}

func certificateInstanceActive(spec ruleSpec, identity key) bool {
	return slices.ContainsFunc(spec.rule.CertificateProbes(spec.target), func(index int) bool {
		return identity.instance == CertificateInstanceKey(identity.collector, identity.target, index)
	})
}

func (e *Engine) certificate(identity key) *Certificate {
	for index, view := range e.probes[targetKey{identity.collector, identity.target}] {
		if view.cert != nil && identity.instance == CertificateInstanceKey(identity.collector, identity.target, index) {
			copy := *view.cert
			return &copy
		}
	}
	return nil
}
