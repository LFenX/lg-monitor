package transport

import (
	"fmt"
	"math"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

func Encode(message pipeline.Message) (*lgmonv1.Record, error) {
	if err := message.Validate(); err != nil {
		return nil, err
	}
	record := &lgmonv1.Record{Seq: message.Seq, Ts: message.TS}
	switch message.Kind {
	case pipeline.KindTargetStatus:
		status := message.TargetStatus
		body := &lgmonv1.TargetStatus{TargetId: status.TargetID, State: status.State, Process: status.Process, Port: status.Port, Reason: status.Reason, Full: status.Full}
		for _, p := range status.Processes {
			body.Processes = append(body.Processes, &lgmonv1.ProcessRef{Pid: p.PID, ParentPid: p.ParentPID, Name: p.Name, Exe: p.Exe, CreatedAt: p.CreatedAt, CpuPercent: p.CPUPercent, Rss: p.RSS, Threads: p.Threads})
		}
		for _, p := range status.Ports {
			if p.Port < 1 || p.Port > 65535 {
				return nil, fmt.Errorf("上报监听端口必须在 1～65535 之间")
			}
			body.Ports = append(body.Ports, &lgmonv1.Port{LocalAddress: p.LocalAddress, Port: uint32(p.Port), Pid: p.PID})
		}
		record.Body = &lgmonv1.Record_TargetStatus{TargetStatus: body}
	case pipeline.KindProbeResult:
		p := message.ProbeResult
		if p.Probe > math.MaxInt32 || p.StatusCode < 0 || p.StatusCode > 599 {
			return nil, fmt.Errorf("上报探测编号或 HTTP 状态码无效")
		}
		record.Body = &lgmonv1.Record_ProbeResult{ProbeResult: &lgmonv1.ProbeResult{TargetId: p.TargetID, Probe: int32(p.Probe), Ts: p.TS, Ok: p.OK, LatencyMs: p.LatencyMS, StatusCode: int32(p.StatusCode), Error: p.Error}}
	case pipeline.KindProcessSample:
		p := message.ProcessSample
		record.Body = &lgmonv1.Record_ProcessSample{ProcessSample: &lgmonv1.ProcessSample{TargetId: p.TargetID, Pid: p.PID, CreatedAt: p.CreatedAt, CpuPercent: p.CPUPercent, Rss: p.RSS}}
	}
	return record, nil
}

func Decode(collectorID string, record *lgmonv1.Record) (pipeline.Message, error) {
	if record == nil {
		return pipeline.Message{}, fmt.Errorf("采集记录不能为空")
	}
	message := pipeline.Message{CollectorID: collectorID, Seq: record.Seq, TS: record.Ts}
	switch body := record.Body.(type) {
	case *lgmonv1.Record_TargetStatus:
		if body.TargetStatus == nil {
			return message, fmt.Errorf("目标状态不能为空")
		}
		s := body.TargetStatus
		status := &pipeline.TargetStatus{TargetID: s.TargetId, State: s.State, Process: s.Process, Port: s.Port, Reason: s.Reason, Full: s.Full}
		for _, p := range s.Processes {
			if p == nil {
				return message, fmt.Errorf("进程引用不能为空")
			}
			status.Processes = append(status.Processes, pipeline.ProcessRef{PID: p.Pid, ParentPID: p.ParentPid, Name: p.Name, Exe: p.Exe, CreatedAt: p.CreatedAt, CPUPercent: p.CpuPercent, RSS: p.Rss, Threads: p.Threads})
		}
		for _, p := range s.Ports {
			if p == nil || p.Port < 1 || p.Port > 65535 {
				return message, fmt.Errorf("监听端口无效")
			}
			status.Ports = append(status.Ports, collector.Port{LocalAddress: p.LocalAddress, Port: p.Port, PID: p.Pid})
		}
		message.Kind, message.TargetStatus = pipeline.KindTargetStatus, status
	case *lgmonv1.Record_ProbeResult:
		p := body.ProbeResult
		if p == nil || p.StatusCode < 0 || p.StatusCode > 599 {
			return message, fmt.Errorf("探测结果或 HTTP 状态码无效")
		}
		message.Kind, message.ProbeResult = pipeline.KindProbeResult, &collector.ProbeResult{TargetID: p.TargetId, Probe: int(p.Probe), TS: p.Ts, OK: p.Ok, LatencyMS: p.LatencyMs, StatusCode: int(p.StatusCode), Error: p.Error}
	case *lgmonv1.Record_ProcessSample:
		p := body.ProcessSample
		if p == nil {
			return message, fmt.Errorf("进程采样不能为空")
		}
		message.Kind, message.ProcessSample = pipeline.KindProcessSample, &pipeline.ProcessSample{TargetID: p.TargetId, PID: p.Pid, CreatedAt: p.CreatedAt, CPUPercent: p.CpuPercent, RSS: p.Rss}
	default:
		return message, fmt.Errorf("未知的采集记录类型")
	}
	return message, message.Validate()
}
