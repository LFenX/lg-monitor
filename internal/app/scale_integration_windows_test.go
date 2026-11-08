//go:build windows && integration && scale

package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/examples"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"github.com/shirou/gopsutil/v4/process"
	"google.golang.org/grpc"
)

type scaleGate struct {
	mu          sync.Mutex
	offline     bool
	connections []transport.RPCConnection
	sent        map[uint64]time.Time
	latencies   []float64
	unconfirmed uint64
	firstSent   []int64
}

func (g *scaleGate) Connect(ctx context.Context, address string, configuration *tls.Config) (transport.RPCConnection, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.offline {
		return nil, fmt.Errorf("owned scale collector is disconnected")
	}
	connection, err := (transport.NativeConnector{}).Connect(ctx, address, configuration)
	if err == nil {
		connection = &scaleRPCConnection{RPCConnection: connection, gate: g}
		g.connections = append(g.connections, connection)
	}
	return connection, err
}

func (g *scaleGate) disconnect(value bool) {
	g.mu.Lock()
	g.offline = value
	var closing []transport.RPCConnection
	if value {
		closing = g.connections
		g.connections = nil
	}
	g.mu.Unlock()
	for _, connection := range closing {
		_ = connection.Close()
	}
}

type scaleRPCConnection struct {
	transport.RPCConnection
	gate *scaleGate
}

func (c *scaleRPCConnection) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, options ...grpc.CallOption) (grpc.ClientStream, error) {
	stream, err := c.RPCConnection.NewStream(ctx, desc, method, options...)
	if err != nil {
		return nil, err
	}
	return &scaleRPCStream{ClientStream: stream, gate: c.gate}, nil
}

type scaleRPCStream struct {
	grpc.ClientStream
	gate *scaleGate
}

func (s *scaleRPCStream) SendMsg(value any) error {
	message, ok := value.(*lgmonv1.CollectorMessage)
	at := time.Now()
	if ok && message.GetBatch() != nil {
		counter := scaleCounter()
		s.gate.mu.Lock()
		if s.gate.sent == nil {
			s.gate.sent = map[uint64]time.Time{}
		}
		for _, record := range message.GetBatch().Records {
			s.gate.sent[record.Seq] = at
			for uint64(len(s.gate.firstSent)) <= record.Seq {
				s.gate.firstSent = append(s.gate.firstSent, 0)
			}
			if s.gate.firstSent[record.Seq] == 0 {
				s.gate.firstSent[record.Seq] = counter
			}
		}
		s.gate.mu.Unlock()
	}
	return s.ClientStream.SendMsg(value)
}
func (s *scaleRPCStream) RecvMsg(value any) error {
	if err := s.ClientStream.RecvMsg(value); err != nil {
		return err
	}
	message, ok := value.(*lgmonv1.MonitorMessage)
	if !ok {
		return nil
	}
	at := time.Now()
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	if welcome := message.GetWelcome(); welcome != nil {
		// An interrupted acknowledgement has no measured bound; resume still verifies its durable sequence.
		for seq := range s.gate.sent {
			if seq < welcome.ResumeFrom {
				delete(s.gate.sent, seq)
				s.gate.unconfirmed++
			}
		}
	}
	if ack := message.GetAck(); ack != nil {
		for seq, sent := range s.gate.sent {
			if seq <= ack.Seq {
				s.gate.latencies = append(s.gate.latencies, float64(at.Sub(sent))/float64(time.Millisecond))
				delete(s.gate.sent, seq)
			}
		}
	}
	return nil
}

type scaleWorker struct {
	box    *transport.Outbox
	work   examples.Workload
	gate   *scaleGate
	mu     sync.Mutex
	down   map[int]bool
	counts map[pipeline.Kind]int
}

func (w *scaleWorker) unavailable(index int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.down[index]
}

func (w *scaleWorker) write(ctx context.Context, messages []pipeline.Message) error {
	for _, message := range messages {
		if err := w.box.Write(ctx, message); err != nil {
			return err
		}
		w.mu.Lock()
		w.counts[message.Kind]++
		w.mu.Unlock()
	}
	return nil
}

func (w *scaleWorker) change(ctx context.Context, target int, down bool) (int64, error) {
	w.mu.Lock()
	w.down[target] = down
	w.mu.Unlock()
	at := time.Now()
	return at.UnixMilli(), w.write(ctx, []pipeline.Message{w.work.Status(target, at, down)})
}

type scaleFault struct {
	Collector string
	Target    string
	DownMS    int64
	UpMS      int64
}

type scaleLoadReport struct {
	Formal                   bool
	DurationSeconds          float64
	Collectors               int
	TargetsPerCollector      int
	Monitor                  scaleMonitorReport
	APIP95MS                 map[string]float64
	MonitorAverageCPUPercent float64
	MonitorPeakRSSBytes      uint64
	BacklogByMinute          []scaleBacklogMinute
	BacklogMeasurement       string
	Faults                   []scaleFault
	RuleMaxMS                int64
	SubmitMaxMS              int64
	CommitLatencyMeasurement string
	MeasuredRecords          uint64
	AcknowledgedRecords      uint64
	AcknowledgementP99MS     float64
	CounterFrequency         int64
	UnconfirmedAttempts      uint64
}

type scaleBacklogMinute struct {
	Minute              int
	Samples             int
	MeanTotal           float64
	MaxTotal            uint64
	PerCollectorMinimum []uint64
}

func scaleCheckBacklog(samples []scaleBacklogMinute, minutes, collectors int) error {
	for minute := 1; minute < minutes; minute++ {
		// Startup and the intentional outage/replay are not steady-state load.
		if minute >= 8 && minute <= 12 {
			continue
		}
		var sample *scaleBacklogMinute
		for index := range samples {
			if samples[index].Minute == minute {
				sample = &samples[index]
				break
			}
		}
		if sample == nil || sample.Samples < 100 || len(sample.PerCollectorMinimum) != collectors {
			return fmt.Errorf("insufficient steady backlog coverage at minute %d: %+v", minute, sample)
		}
		for collector, minimum := range sample.PerCollectorMinimum {
			if minimum != 0 {
				return fmt.Errorf("collector %d backlog never clears at steady minute %d: minimum=%d", collector, minute, minimum)
			}
		}
	}
	return nil
}

func TestScaleBacklogMeasurementRejectsUndrainedSteadyMinute(t *testing.T) {
	var samples []scaleBacklogMinute
	for minute := 0; minute <= 30; minute++ {
		samples = append(samples, scaleBacklogMinute{Minute: minute, Samples: 120, MeanTotal: 8, MaxTotal: 80, PerCollectorMinimum: []uint64{0, 0}})
	}
	// Collectors may clear at different instants; the combined queue need not be zero.
	if err := scaleCheckBacklog(samples, 30, 2); err != nil {
		t.Fatal(err)
	}
	samples[29].PerCollectorMinimum[1] = 1
	if err := scaleCheckBacklog(samples, 30, 2); err == nil {
		t.Fatal("shutdown's empty sample concealed a steady collector backlog")
	}
	samples[29].PerCollectorMinimum[1] = 0
	samples[9].PerCollectorMinimum[1] = 1000
	if err := scaleCheckBacklog(samples, 30, 2); err != nil {
		t.Fatal(err)
	}
	samples[1].Samples = 1
	if err := scaleCheckBacklog(samples, 30, 2); err == nil {
		t.Fatal("sparse measurements accepted as complete backlog coverage")
	}
}

func TestScaleTwentyCollectorsFiftyTargets(t *testing.T) {
	duration := scaleDuration
	formal := true
	if diagnostic := os.Getenv("LGMON_SCALE_SMOKE"); diagnostic != "" {
		duration = scaleRequire(time.ParseDuration(diagnostic))
		if duration < 90*time.Second || duration >= scaleDuration {
			t.Fatal("smoke duration must be at least 90s and below 30m")
		}
		formal = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration+5*time.Minute)
	defer cancel()
	root := t.TempDir()
	home := filepath.Join(root, "monitor")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command(os.Args[0], "-test.run=^TestScaleMonitorChild$", "-test.timeout=38m")
	if profile := os.Getenv("LGMON_SCALE_CHILD_CPU_PROFILE"); profile != "" {
		command.Args = append(command.Args, "-test.cpuprofile="+profile)
	}
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Env = append(os.Environ(), "LGMON_SCALE_CHILD_HOME="+home)
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		_ = os.WriteFile(filepath.Join(home, "stop"), []byte("stop"), 0o600)
		select {
		case err := <-exited:
			if err != nil {
				t.Errorf("monitor helper: %v %s", err, output.String())
			}
		case <-time.After(20 * time.Second):
			_ = command.Process.Kill()
			<-exited
			t.Errorf("owned monitor helper required forced cleanup: %s", output.String())
		}
	})
	wait := func(label string, limit time.Duration, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			select {
			case err := <-exited:
				stopped = true
				t.Fatalf("%s: monitor exited: %v %s", label, err, output.String())
			default:
			}
			if condition() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("%s exceeded %s", label, limit)
	}
	var ready, httpReady map[string]string
	wait("monitor ready", 30*time.Second, func() bool {
		data, err := os.ReadFile(filepath.Join(home, "ready.json"))
		return err == nil && json.Unmarshal(data, &ready) == nil
	})
	wait("API ready", 10*time.Second, func() bool {
		data, err := os.ReadFile(filepath.Join(home, "http.json"))
		return err == nil && json.Unmarshal(data, &httpReady) == nil
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	vault := scaleRequire(secrets.New(home, logger))
	admin := scaleRequire(transport.ConnectAdmin(ctx, transport.FileState{Home: home}, vault, time.Now, nil))
	defer admin.Close()
	pairing := lgmonv1.NewPairingClient(admin)
	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	defer stopDelivery()
	var workers []*scaleWorker
	var deliveryWG sync.WaitGroup
	failures := make(chan error, 100)
	reportError := func(err error) {
		if err != nil {
			select {
			case failures <- err:
			default:
			}
		}
	}
	for index := 0; index < scaleCollectors; index++ {
		code := scaleRequire(pairing.NewCode(ctx, &lgmonv1.NewCodeRequest{TtlMillis: 60000}))
		collectorHome := filepath.Join(root, fmt.Sprintf("collector-%02d", index))
		keys := scaleRequire(secrets.New(collectorHome, logger))
		state := transport.FileState{Home: collectorHome}
		id := fmt.Sprintf("fake-scale-%02d", index)
		_, err := transport.Join(ctx, transport.JoinOptions{Address: ready["address"], CollectorID: id, Hostname: id,
			Code: code.Code, Fingerprint: code.Fingerprint, Keys: keys, State: state, Now: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		identity := scaleRequire(transport.LoadClient(ctx, state, keys, time.Now))
		box := scaleRequire(transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(collectorHome, "data", "outbox.db"), CollectorID: id, Now: time.Now}))
		t.Cleanup(func() { box.Close() })
		gate := &scaleGate{}
		cached := scaleRequire(transport.NewCachedTargets(ctx, transport.FileTargetCache{Home: collectorHome}, logger))
		delivery := scaleRequire(transport.NewDelivery(transport.DeliveryOptions{Outbox: box, Identity: identity, Hostname: id, Version: "fake-scale", Connector: gate, Config: cached, Logger: logger}))
		deliveryWG.Add(1)
		go func() { defer deliveryWG.Done(); reportError(delivery.Run(deliveryCtx)) }()
		workers = append(workers, &scaleWorker{box: box, gate: gate, down: map[int]bool{}, counts: map[pipeline.Kind]int{}, work: examples.Workload{Collector: id}})
	}
	start := time.Now().Add(2 * time.Second)
	for _, worker := range workers {
		worker.work.StartedAt = start
	}
	var generators sync.WaitGroup
	for index, worker := range workers {
		generators.Add(1)
		go func() {
			defer generators.Done()
			phase := time.Duration(index) * time.Second / scaleCollectors
			for elapsed := time.Duration(0); elapsed < duration; elapsed += time.Second {
				timer := time.NewTimer(time.Until(start.Add(elapsed + phase)))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				reportError(worker.write(ctx, worker.work.Messages(elapsed, time.Now(), worker.unavailable)))
			}
		}()
	}
	report := scaleLoadReport{Formal: formal, Collectors: scaleCollectors, TargetsPerCollector: examples.WorkloadTargets, APIP95MS: map[string]float64{}}
	backlogResult := make(chan []scaleBacklogMinute, 1)
	go func() {
		var samples []scaleBacklogMinute
		defer func() { backlogResult <- samples }()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				if at.Before(start) {
					continue
				}
				if !at.Before(start.Add(duration)) {
					return
				}
				minimum := make([]uint64, len(workers))
				var total uint64
				for index, worker := range workers {
					state, err := worker.box.State(ctx)
					if err != nil {
						reportError(err)
						return
					}
					minimum[index] = state.Allocated - state.Acked
					total += minimum[index]
				}
				// Never count an empty queue observed after generators stop.
				if !time.Now().Before(start.Add(duration)) {
					return
				}
				minute := int(at.Sub(start) / time.Minute)
				if len(samples) == 0 || samples[len(samples)-1].Minute != minute {
					samples = append(samples, scaleBacklogMinute{Minute: minute, PerCollectorMinimum: minimum})
				}
				sample := &samples[len(samples)-1]
				sample.Samples++
				sample.MeanTotal += (float64(total) - sample.MeanTotal) / float64(sample.Samples)
				sample.MaxTotal = max(sample.MaxTotal, total)
				for index := range minimum {
					sample.PerCollectorMinimum[index] = min(sample.PerCollectorMinimum[index], minimum[index])
				}
			}
		}
	}()
	var faultWG sync.WaitGroup
	faultWG.Add(1)
	go func() {
		defer faultWG.Done()
		interval, recovery := 5*time.Minute, 30*time.Second
		if !formal {
			interval, recovery = duration/3, 2*time.Second
		}
		random := rand.New(rand.NewPCG(22, 50))
		for elapsed := interval; elapsed+recovery < duration; elapsed += interval {
			timer := time.NewTimer(time.Until(start.Add(elapsed)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			index, target := random.IntN(scaleCollectors-1), random.IntN(examples.WorkloadTargets)
			worker := workers[index]
			down, err := worker.change(ctx, target, true)
			reportError(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(recovery):
			}
			up, err := worker.change(ctx, target, false)
			reportError(err)
			report.Faults = append(report.Faults, scaleFault{Collector: worker.work.Collector, Target: examples.WorkloadTargetID(target), DownMS: down, UpMS: up})
		}
	}()
	var offlineWG sync.WaitGroup
	offlineWG.Add(1)
	go func() {
		defer offlineWG.Done()
		begin, outage := 8*time.Minute, 2*time.Minute
		if !formal {
			begin, outage = 3*time.Second, 45*time.Second
		}
		timer := time.NewTimer(time.Until(start.Add(begin)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		worker := workers[scaleCollectors-1]
		worker.gate.disconnect(true)
		defer worker.gate.disconnect(false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(outage / 4):
		}
		_, err := worker.change(ctx, 0, true)
		reportError(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(outage / 4):
		}
		_, err = worker.change(ctx, 0, false)
		reportError(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(outage / 2):
		}
		worker.gate.disconnect(false)
	}()
	monitorProcess := scaleRequire(process.NewProcess(int32(command.Process.Pid)))
	initialCPU := scaleRequire(monitorProcess.Times())
	measurementStart := time.Now()
	var maxRSS uint64
	var apiTimes = map[string][]float64{}
	httpClient := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer httpClient.CloseIdleConnections()
	ticker := time.NewTicker(2 * time.Second)
	lastMinute := -1
	for time.Now().Before(start.Add(duration)) {
		select {
		case err := <-failures:
			t.Fatal(err)
		case err := <-exited:
			stopped = true
			t.Fatalf("monitor exited: %v %s", err, output.String())
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
			if memory, err := monitorProcess.MemoryInfo(); err == nil {
				maxRSS = max(maxRSS, memory.RSS)
			} else {
				t.Fatal(err)
			}
			for _, path := range []string{"/targets", "/alerts", "/collectors"} {
				req := scaleRequire(http.NewRequestWithContext(ctx, "GET", "http://"+httpReady["address"]+"/api/v1"+path, nil))
				req.Header.Set("Authorization", "Bearer fake-scale-read")
				at := time.Now()
				response := scaleRequire(httpClient.Do(req))
				_, err := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatalf("API %s: %d %v", path, response.StatusCode, err)
				}
				apiTimes[path] = append(apiTimes[path], float64(time.Since(at))/float64(time.Millisecond))
			}
			minute := int(time.Since(start) / time.Minute)
			if minute != lastMinute {
				var pending uint64
				for _, worker := range workers {
					state := scaleRequire(worker.box.State(ctx))
					pending += state.Allocated - state.Acked
				}
				lastMinute = minute
				t.Logf("elapsed=%s pending=%d monitor_rss=%d", time.Since(start).Round(time.Second), pending, maxRSS)
			}
		}
	}
	ticker.Stop()
	finalCPU := scaleRequire(monitorProcess.Times())
	report.MonitorAverageCPUPercent = (finalCPU.Total() - initialCPU.Total()) / time.Since(measurementStart).Seconds() / float64(runtime.NumCPU()) * 100
	report.MonitorPeakRSSBytes = maxRSS
	report.BacklogByMinute = <-backlogResult
	report.BacklogMeasurement = "independent 500ms samples; every collector clears within each full steady minute; startup, outage/replay and post-load drain excluded"
	if formal {
		if err := scaleCheckBacklog(report.BacklogByMinute, int(duration/time.Minute), len(workers)); err != nil {
			t.Error(err)
		}
	}
	generators.Wait()
	faultWG.Wait()
	offlineWG.Wait()
	report.DurationSeconds = time.Since(start).Seconds()
	wait("all durable acknowledgements", 2*time.Minute, func() bool {
		for _, worker := range workers {
			state := scaleRequire(worker.box.State(ctx))
			if state.GapFrom != 0 || state.GapTo != 0 {
				t.Fatalf("cache gap: %+v", state)
			}
			if state.Acked != state.Allocated {
				return false
			}
		}
		return true
	})
	stopDelivery()
	deliveryWG.Wait()
	if err := os.WriteFile(filepath.Join(home, "stop"), []byte("stop"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		stopped = true
		if err != nil {
			t.Fatalf("monitor helper: %v %s", err, output.String())
		}
	case <-time.After(time.Minute):
		t.Fatal("monitor helper did not drain")
	}
	data := scaleRequire(os.ReadFile(filepath.Join(home, "monitor-report.json")))
	if err := json.Unmarshal(data, &report.Monitor); err != nil {
		t.Fatal(err)
	}
	var latencies []float64
	var totalRecords uint64
	for _, worker := range workers {
		worker.gate.mu.Lock()
		latencies = append(latencies, worker.gate.latencies...)
		report.UnconfirmedAttempts += worker.gate.unconfirmed
		worker.gate.mu.Unlock()
		totalRecords += scaleRequire(worker.box.State(ctx)).Allocated
	}
	sort.Float64s(latencies)
	report.AcknowledgedRecords = uint64(len(latencies))
	report.CommitLatencyMeasurement = "first wire send to successful raw-data IngestBatch return; shared Windows performance counter; all records including reconnect attempts"
	// Treat unobserved acknowledgements as infinite latency when choosing the full-population percentile.
	percentile := int(math.Ceil(float64(totalRecords)*.99)) - 1
	if percentile >= 0 && percentile < len(latencies) {
		report.AcknowledgementP99MS = latencies[percentile]
	} else {
		t.Errorf("insufficient acknowledgement coverage for a full-population p99")
	}
	if report.AcknowledgedRecords+report.UnconfirmedAttempts != totalRecords {
		t.Errorf("acknowledgement coverage: measured=%d interrupted=%d records=%d", report.AcknowledgedRecords, report.UnconfirmedAttempts, totalRecords)
	}
	var sent [][]int64
	for _, worker := range workers {
		sent = append(sent, worker.gate.firstSent)
	}
	report.CounterFrequency = scaleClockValue(scaleFrequencyProc)
	commits := scaleRequire(os.Open(filepath.Join(home, "commits.bin")))
	commitLatencies, commitErr := scaleCommitLatencies(commits, sent, report.CounterFrequency)
	commits.Close()
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	report.MeasuredRecords = uint64(len(commitLatencies))
	if report.MeasuredRecords != totalRecords {
		t.Fatalf("commit coverage: measured=%d records=%d", report.MeasuredRecords, totalRecords)
	}
	sort.Float64s(commitLatencies)
	report.Monitor.CommitP99MS = commitLatencies[percentile]
	for _, worker := range workers {
		for kind, expected := range worker.counts {
			if got := report.Monitor.Records[worker.work.Collector][kind]; got != expected {
				t.Errorf("%s %s records=%d expected=%d", worker.work.Collector, kind, got, expected)
			}
		}
		state := scaleRequire(worker.box.State(ctx))
		if report.Monitor.LastSeq[worker.work.Collector] != state.Allocated {
			t.Errorf("%s incomplete last sequence", worker.work.Collector)
		}
	}
	for kind, table := range map[pipeline.Kind]string{
		pipeline.KindTargetStatus: "target_status", pipeline.KindProcessSample: "process_samples",
		pipeline.KindProbeResult: "probe_results", pipeline.KindSystemMetrics: "system_metrics",
	} {
		var expected int64
		for _, worker := range workers {
			expected += int64(worker.counts[kind])
		}
		if report.Monitor.DatabaseCounts[table] != expected {
			t.Errorf("database %s records=%d expected=%d", table, report.Monitor.DatabaseCounts[table], expected)
		}
	}
	if report.Monitor.DatabaseCounts["collector_events"] != 0 {
		t.Errorf("unexpected data loss events: %d", report.Monitor.DatabaseCounts["collector_events"])
	}
	if report.Monitor.DatabaseCounts["active_alerts"] != 0 {
		t.Errorf("faults did not recover: active=%d", report.Monitor.DatabaseCounts["active_alerts"])
	}
	for path, values := range apiTimes {
		sort.Float64s(values)
		report.APIP95MS[path] = values[int(math.Ceil(float64(len(values))*0.95))-1]
		if report.APIP95MS[path] >= 500 {
			t.Errorf("%s p95=%.1fms", path, report.APIP95MS[path])
		}
	}
	for _, fault := range report.Faults {
		found := 0
		for _, transition := range report.Monitor.Transitions {
			if transition.Alert.CollectorID == fault.Collector && transition.Alert.TargetID == fault.Target && transition.Alert.StartedAt >= fault.DownMS && transition.Alert.StartedAt < fault.UpMS {
				found++
				report.RuleMaxMS = max(report.RuleMaxMS, transition.SeenMS-fault.DownMS)
				report.SubmitMaxMS = max(report.SubmitMaxMS, transition.SentMS-fault.DownMS)
				if transition.SentMS == 0 {
					t.Errorf("fault notification missing: %+v", fault)
				}
			}
		}
		if found != 1 {
			t.Errorf("fault firing count=%d want=1: %+v", found, fault)
		}
	}
	for _, transition := range report.Monitor.Transitions {
		if transition.Alert.CollectorID == workers[scaleCollectors-1].work.Collector && transition.Alert.TargetID == examples.WorkloadTargetID(0) {
			t.Errorf("replayed ended fault incorrectly fired: %+v", transition)
		}
	}
	if report.Monitor.SequenceErrors != 0 || (formal && report.Monitor.CommitP99MS >= 2000) || report.RuleMaxMS > 5000 || report.SubmitMaxMS > 15000 {
		t.Errorf("scale thresholds failed: sequence=%d commit_p99=%.1fms rule_max=%dms submit_max=%dms", report.Monitor.SequenceErrors, report.Monitor.CommitP99MS, report.RuleMaxMS, report.SubmitMaxMS)
	}
	encoded := scaleRequire(json.MarshalIndent(report, "", "  "))
	t.Logf("scale report: %s", encoded)
	if destination := os.Getenv("LGMON_SCALE_REPORT"); destination != "" {
		if err := os.WriteFile(destination, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
