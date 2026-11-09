package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

const maxOTLPBatchPoints = 1000
const maxOTLPRequestBytes = 1024 * 1024

func otlpString(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

func encodeOTLP(ctx context.Context, snapshot store.ExportMetricSnapshot, monitorID, version string) ([][]byte, error) {
	if err := api.ValidateMetricSnapshot(snapshot); err != nil {
		return nil, err
	}
	type identity struct{ host, version string }
	identities := map[string]identity{}
	for _, family := range snapshot.Families {
		if family.Name == "lgmon_collector_info" {
			for _, point := range family.Points {
				identities[point.Labels["collector_id"]] = identity{point.Labels["hostname"], point.Labels["version"]}
			}
		}
	}
	type resource struct {
		scope    *metricspb.ScopeMetrics
		families map[string]*metricspb.Metric
	}
	var batches [][]byte
	request := &collectorpb.ExportMetricsServiceRequest{}
	resources := map[string]*resource{}
	count, estimated := 0, 0
	flush := func() error {
		if count == 0 {
			return nil
		}
		wire, err := proto.Marshal(request)
		if err != nil {
			return fmt.Errorf("编码 OTLP protobuf 失败：%w", err)
		}
		if len(wire) > maxOTLPRequestBytes {
			return fmt.Errorf("OTLP 单批指标超过 1 MiB 界限")
		}
		batches = append(batches, wire)
		request = &collectorpb.ExportMetricsServiceRequest{}
		resources = map[string]*resource{}
		count, estimated = 0, 0
		return nil
	}
	for _, family := range snapshot.Families {
		for _, point := range family.Points {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("OTLP 编码已取消：%w", err)
			}
			cid := point.Labels["collector_id"]
			identity := identities[cid]
			size := 512 + len(family.Name) + len(family.Help) + len(family.Unit) + len(monitorID) + len(version) + len(identity.host) + len(identity.version)
			for k, v := range point.Labels {
				size += len(k) + len(v) + 32
			}
			if size > maxOTLPRequestBytes/2 {
				return nil, fmt.Errorf("单个 OTLP 指标及身份超过批次界限")
			}
			if count >= maxOTLPBatchPoints || estimated+size > maxOTLPRequestBytes/2 {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			r := resources[cid]
			if r == nil {
				instance := cid
				if instance == "" {
					instance = monitorID
				}
				attributes := []*commonpb.KeyValue{otlpString("service.name", "lg-monitor"), otlpString("service.instance.id", instance), otlpString("lgmon.monitor.id", monitorID)}
				if identity.host != "" {
					attributes = append(attributes, otlpString("host.name", identity.host))
				}
				if identity.version != "" {
					attributes = append(attributes, otlpString("service.version", identity.version))
				}
				r = &resource{scope: &metricspb.ScopeMetrics{Scope: &commonpb.InstrumentationScope{Name: "lg-monitor/exports", Version: version}}, families: map[string]*metricspb.Metric{}}
				resources[cid] = r
				request.ResourceMetrics = append(request.ResourceMetrics, &metricspb.ResourceMetrics{Resource: &resourcepb.Resource{Attributes: attributes}, ScopeMetrics: []*metricspb.ScopeMetrics{r.scope}})
			}
			metric := r.families[family.Name]
			if metric == nil {
				metric = &metricspb.Metric{Name: family.Name, Description: family.Help, Unit: family.Unit, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{}}}
				r.families[family.Name] = metric
				r.scope.Metrics = append(r.scope.Metrics, metric)
			}
			data := &metricspb.NumberDataPoint{TimeUnixNano: uint64(point.TS) * 1000000, Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: point.Value}}
			keys := make([]string, 0, len(point.Labels))
			for key := range point.Labels {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				data.Attributes = append(data.Attributes, otlpString(key, point.Labels[key]))
			}
			metric.GetGauge().DataPoints = append(metric.GetGauge().DataPoints, data)
			count++
			estimated += size
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return batches, nil
}
