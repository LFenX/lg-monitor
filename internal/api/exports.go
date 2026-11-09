package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/store"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/protobuf/proto"
)

const PrometheusContentType = "text/plain; version=0.0.4; charset=utf-8"
const MaxPrometheusBytes = 16 * 1024 * 1024

var metricNamePattern = regexp.MustCompile(`^lgmon_[a-zA-Z_][a-zA-Z0-9_]*$`)
var exportLabelNames = map[string]bool{"collector_id": true, "hostname": true, "version": true, "volume": true, "interface": true, "target_id": true, "probe": true, "metric": true, "service": true, "severity": true}

func ValidateMetricSnapshot(snapshot store.ExportMetricSnapshot) error {
	seen := map[string]bool{}
	families := map[string]bool{}
	count := 0
	for _, family := range snapshot.Families {
		if !metricNamePattern.MatchString(family.Name) || families[family.Name] || len(family.Help) > 1024 || !utf8.ValidString(family.Help) || len(family.Unit) > 64 || !utf8.ValidString(family.Unit) {
			return fmt.Errorf("指标族名称、说明或单位无效")
		}
		families[family.Name] = true
		for _, point := range family.Points {
			count++
			if count > store.MaxExportPoints || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) || point.TS <= 0 || uint64(point.TS) > ^uint64(0)/1000000 {
				return fmt.Errorf("指标数量、数值或时间超过导出协议界限")
			}
			if len(point.Labels) > len(exportLabelNames) {
				return fmt.Errorf("指标标签超过导出界限")
			}
			for name, value := range point.Labels {
				if !exportLabelNames[name] || len(value) > 4096 || !utf8.ValidString(value) {
					return fmt.Errorf("指标标签名称或值无效")
				}
			}
			encoded, err := json.Marshal(point.Labels)
			if err != nil {
				return fmt.Errorf("编码指标标签失败：%w", err)
			}
			key := family.Name + string(encoded)
			if seen[key] {
				return fmt.Errorf("指标名称和标签集重复")
			}
			seen[key] = true
		}
	}
	return nil
}

type boundedMetricBuffer struct{ bytes.Buffer }

func (b *boundedMetricBuffer) WriteByte(value byte) error {
	if b.Len() >= MaxPrometheusBytes {
		return fmt.Errorf("Prometheus 文本超过 16 MiB 导出界限")
	}
	return b.Buffer.WriteByte(value)
}

func (b *boundedMetricBuffer) Write(data []byte) (int, error) {
	if len(data) > MaxPrometheusBytes-b.Len() {
		return 0, fmt.Errorf("Prometheus 文本超过 16 MiB 导出界限")
	}
	return b.Buffer.Write(data)
}
func (b *boundedMetricBuffer) WriteString(data string) (int, error) {
	if len(data) > MaxPrometheusBytes-b.Len() {
		return 0, fmt.Errorf("Prometheus 文本超过 16 MiB 导出界限")
	}
	return b.Buffer.WriteString(data)
}

func EncodePrometheus(snapshot store.ExportMetricSnapshot) ([]byte, error) {
	if err := ValidateMetricSnapshot(snapshot); err != nil {
		return nil, err
	}
	var buffer boundedMetricBuffer
	for _, family := range snapshot.Families {
		if len(family.Points) == 0 {
			continue
		}
		wire := &dto.MetricFamily{Name: proto.String(family.Name), Help: proto.String(family.Help), Type: dto.MetricType_GAUGE.Enum()}
		for _, point := range family.Points {
			metric := &dto.Metric{Gauge: &dto.Gauge{Value: proto.Float64(point.Value)}}
			keys := make([]string, 0, len(point.Labels))
			for key := range point.Labels {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				metric.Label = append(metric.Label, &dto.LabelPair{Name: proto.String(key), Value: proto.String(point.Labels[key])})
			}
			wire.Metric = append(wire.Metric, metric)
		}
		if _, err := expfmt.MetricFamilyToText(&buffer, wire); err != nil {
			return nil, fmt.Errorf("编码 Prometheus 指标失败：%w", err)
		}
	}
	return buffer.Bytes(), nil
}
