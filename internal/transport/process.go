package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/collector/match"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/protobuf/proto"
)

const ProcessQueryCapability = "process_query"
const MaxProcessResults = 5000

var ErrCollectorOffline = errors.New("采集器离线")
var ErrCollectorOutdated = errors.New("采集器版本过旧，请升级后再试")
var ErrProcessSnapshot = errors.New("采集器尚无可用快照，或返回了无效进程结果，请稍后重试")

type ProcessResponder interface {
	Answer(context.Context, *lgmonv1.ProcessQuery) *lgmonv1.ProcessList
}

type SnapshotResponder struct {
	Snapshots   collector.ProcessSnapshots
	Directories collector.WorkingDirectorySource
}

func (source SnapshotResponder) Answer(ctx context.Context, query *lgmonv1.ProcessQuery) *lgmonv1.ProcessList {
	result := &lgmonv1.ProcessList{RequestId: query.RequestId}
	if ctx.Err() != nil || source.Snapshots == nil || len(query.RequestId) == 0 || len(query.RequestId) > 64 {
		result.Error = "进程查询已取消或请求无效"
		return result
	}
	snapshot := source.Snapshots.Latest()
	if snapshot.CollectedAt <= 0 {
		result.Error = "采集器尚未完成常规进程采集，请稍后重试"
		return result
	}
	result.CollectedAt = snapshot.CollectedAt
	processes := snapshot.Processes
	includeWorkDir := false
	if len(query.MatchJson) > 0 {
		var spec config.Match
		decoder := json.NewDecoder(bytes.NewReader(query.MatchJson))
		decoder.DisallowUnknownFields()
		if len(query.MatchJson) > 1024*1024 || decoder.Decode(&spec) != nil || decoder.Decode(new(any)) != io.EOF || config.ValidateTargetMatch(spec) != nil {
			result.Error = "试匹配条件无效，请按目标 match 格式填写"
			return result
		}
		matcher, err := match.New([]config.Target{{ID: "process-query", Match: spec}})
		if err != nil {
			result.Error = "试匹配条件无效，请检查正则和端口"
			return result
		}
		processes, err = matcher.ResolveDirectories(ctx, snapshot, source.Directories)
		if err != nil {
			result.Error = "进程试匹配已取消"
			return result
		}
		includeWorkDir = spec.WorkDir != ""
		found := matcher.Match(processes, snapshot.Ports)[0]
		if snapshot.ServicesKnown {
			found = matcher.MatchWithServices(processes, snapshot.Ports, snapshot.Services)[0]
		}
		processes, result.Reason = found.Processes, found.Reason
	}
	ports := map[int32][]*lgmonv1.Port{}
	for _, port := range snapshot.Ports {
		ports[port.PID] = append(ports[port.PID], &lgmonv1.Port{Pid: port.PID, Port: port.Port, LocalAddress: port.LocalAddress})
	}
	services := map[int32][]string{}
	for _, service := range snapshot.Services {
		if service.PID > 0 {
			services[service.PID] = append(services[service.PID], service.Name)
		}
	}
	processes = slices.Clone(processes)
	slices.SortFunc(processes, func(a, b collector.Process) int {
		if a.PID < b.PID {
			return -1
		}
		if a.PID > b.PID {
			return 1
		}
		return 0
	})
	size := proto.Size(result)
	for _, process := range processes {
		if ctx.Err() != nil {
			result.Error, result.Processes = "进程查询已取消", nil
			return result
		}
		names := services[process.PID]
		slices.Sort(names)
		row := &lgmonv1.ProcessInfo{Process: &lgmonv1.ProcessRef{Pid: process.PID, ParentPid: process.ParentPID, Name: process.Name, Exe: process.Exe, CreatedAt: process.CreatedAt, CpuPercent: process.CPUPercent, Rss: process.RSS, Threads: process.Threads}, Ports: ports[process.PID], Services: slices.Compact(names)}
		if includeWorkDir {
			row.WorkDir = process.WorkDir
		}
		rowSize := proto.Size(row) + 5
		if len(result.Processes) == MaxProcessResults || size+rowSize > MaxMessageSize-1024 {
			result.Truncated = true
			break
		}
		result.Processes = append(result.Processes, row)
		size += rowSize
	}
	return result
}

func (r *Receiver) QueryProcesses(parent context.Context, id string, spec *config.Match) (collector.ProcessList, error) {
	var empty collector.ProcessList
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	var data []byte
	if spec != nil {
		if err := config.ValidateTargetMatch(*spec); err != nil {
			return empty, err
		}
		var err error
		data, err = json.Marshal(spec)
		if err != nil {
			return empty, fmt.Errorf("编码试匹配条件失败：%w", err)
		}
	}
	r.mu.Lock()
	session := r.active[id]
	if session == nil || !session.connected {
		r.mu.Unlock()
		return empty, ErrCollectorOffline
	}
	if !slices.Contains(session.identity.Capabilities, ProcessQueryCapability) ||
		(spec != nil && (spec.ExeDir != "" || spec.WorkDir != "") && !slices.Contains(session.identity.Capabilities, MatchDirectoryCapability)) {
		r.mu.Unlock()
		return empty, ErrCollectorOutdated
	}
	r.querySeq++
	requestID := strconv.FormatUint(r.querySeq, 10)
	response := make(chan *lgmonv1.ProcessList, 1)
	session.processResults[requestID] = response
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(session.processResults, requestID)
		r.mu.Unlock()
	}()
	query := &lgmonv1.ProcessQuery{RequestId: requestID, MatchJson: data}
	select {
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-session.done:
		return empty, ErrCollectorOffline
	case session.processRequests <- query:
	}
	select {
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-session.done:
		return empty, ErrCollectorOffline
	case result := <-response:
		decoded, err := decodeProcessList(session.identity, result)
		if spec == nil || spec.WorkDir == "" {
			for i := range decoded.Processes {
				decoded.Processes[i].WorkDir = ""
			}
		}
		return decoded, err
	}
}

func (r *Receiver) processReply(session *linkSession, result *lgmonv1.ProcessList) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pending := session.processResults[result.RequestId]; pending != nil {
		select {
		case pending <- result:
		default:
		}
	}
}

func decodeProcessList(identity Hello, source *lgmonv1.ProcessList) (collector.ProcessList, error) {
	result := collector.ProcessList{Collector: collector.ProcessCollector{ID: identity.CollectorID, Hostname: identity.Hostname}, Processes: []collector.ProcessInfo{}}
	if source.Error != "" || source.CollectedAt <= 0 || len(source.Processes) > MaxProcessResults || len(source.Reason) > 4096 {
		return result, ErrProcessSnapshot
	}
	result.CollectedAt, result.Truncated = source.CollectedAt, source.Truncated
	if source.Reason != "" {
		reason := source.Reason
		result.Reason = &reason
	}
	for _, item := range source.Processes {
		process := item.GetProcess()
		if process == nil || process.Pid <= 0 || process.ParentPid < 0 || process.CreatedAt <= 0 || process.Threads < 0 ||
			math.IsNaN(process.CpuPercent) || math.IsInf(process.CpuPercent, 0) || process.CpuPercent < 0 || process.CpuPercent > 100 {
			return collector.ProcessList{}, ErrProcessSnapshot
		}
		row := collector.ProcessInfo{PID: process.Pid, ParentPID: process.ParentPid, Name: process.Name, Exe: process.Exe, CreatedAt: process.CreatedAt, CPUPercent: process.CpuPercent, RSS: process.Rss, Threads: process.Threads, Ports: []collector.ProcessPort{}, Services: append([]string{}, item.Services...)}
		row.WorkDir = item.WorkDir
		for _, port := range item.Ports {
			if port == nil || port.Port == 0 || port.Port > 65535 || port.Pid != process.Pid {
				return collector.ProcessList{}, ErrProcessSnapshot
			}
			row.Ports = append(row.Ports, collector.ProcessPort{Address: port.LocalAddress, Port: port.Port})
		}
		result.Processes = append(result.Processes, row)
	}
	return result, nil
}
