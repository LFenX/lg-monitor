package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const MaxMessageSize = 4 * 1024 * 1024
const MaxBatchRecords = 256
const MaxBatchBytes = 1024 * 1024

type Hello struct {
	CollectorID, Hostname, Version string
	LastAckedSeq                   uint64
	Capabilities                   []string
	CurrentTime, ClockOffsetMS     int64
	ClockKnown                     bool
	Cache                          *pipeline.CacheUsage
}

type receiptKey struct {
	collector string
	seq       uint64
}

type linkSession struct {
	connected             bool
	pending               int
	cancel                context.CancelFunc
	identity              Hello
	processRequests       chan *lgmonv1.ProcessQuery
	processResults        map[string]chan *lgmonv1.ProcessList
	serviceSwitchRequests chan *lgmonv1.ServiceSwitchQuery
	serviceSwitchResults  map[string]chan *lgmonv1.ServiceSwitchSummary
	done                  chan struct{}
}

type commitReceipt struct {
	done      chan error
	session   *linkSession
	keys      []receiptKey
	remaining int
}

type Receiver struct {
	lgmonv1.UnimplementedCollectorLinkServer
	queue        *pipeline.BatchChannel
	readMu       sync.Mutex
	readPending  []pipeline.Message
	register     func(context.Context, Hello) error
	authorize    func(context.Context, Hello) error
	resume       func(context.Context, string) (uint64, error)
	config       ConfigDelivery
	heartbeat    func(context.Context, Hello) error
	serviceTest  func(context.Context, *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error)
	mu           sync.Mutex
	active       map[string]*linkSession
	pending      map[receiptKey]*commitReceipt
	querySeq     uint64
	configStates map[string]CollectorConfigState
	now          func() time.Time
	clocks       map[string]*clockEstimate
	capabilities map[string][]string
	cacheStates  map[string]*pipeline.CacheUsage
}

func NewReceiver(register func(context.Context, Hello) error, authorize ...func(context.Context, Hello) error) *Receiver {
	receiver := &Receiver{queue: pipeline.NewBatchChannel(1), register: register, active: make(map[string]*linkSession), pending: make(map[receiptKey]*commitReceipt), now: time.Now, clocks: map[string]*clockEstimate{}, configStates: map[string]CollectorConfigState{}, capabilities: map[string][]string{}, cacheStates: map[string]*pipeline.CacheUsage{}}
	if len(authorize) > 0 {
		receiver.authorize = authorize[0]
	}
	return receiver
}

func (r *Receiver) Revoke(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if session := r.active[id]; session != nil {
		session.cancel()
	}
}

func NewServer(receiver *Receiver, options ...grpc.ServerOption) *grpc.Server {
	options = append([]grpc.ServerOption{grpc.MaxRecvMsgSize(MaxMessageSize), grpc.MaxSendMsgSize(MaxMessageSize)}, options...)
	server := grpc.NewServer(options...)
	lgmonv1.RegisterCollectorLinkServer(server, receiver)
	return server
}

func (r *Receiver) Read(ctx context.Context) (pipeline.Message, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return pipeline.Message{}, fmt.Errorf("读取采集通道已取消：%w", err)
		}
		if len(r.readPending) == 0 {
			messages, err := r.ReadBatch(ctx)
			if err != nil {
				return pipeline.Message{}, err
			}
			r.readPending = messages
		}
		message := r.readPending[0]
		r.readPending[0] = pipeline.Message{}
		r.readPending = r.readPending[1:]
		r.mu.Lock()
		_, pending := r.pending[receiptKey{message.CollectorID, message.Seq}]
		r.mu.Unlock()
		if pending {
			return message, nil
		}
	}
}
func (r *Receiver) ReadBatch(ctx context.Context) ([]pipeline.Message, error) {
	return r.queue.ReadBatch(ctx)
}
func (r *Receiver) Close() error {
	r.mu.Lock()
	for _, session := range r.active {
		session.cancel()
	}
	r.mu.Unlock()
	return r.queue.Close()
}

func (r *Receiver) Acknowledge(message pipeline.Message, err error) {
	r.mu.Lock()
	completion, exists := r.pending[receiptKey{message.CollectorID, message.Seq}]
	if exists {
		delete(r.pending, receiptKey{message.CollectorID, message.Seq})
		completion.remaining--
		if err != nil || completion.remaining == 0 {
			for _, key := range completion.keys {
				delete(r.pending, key)
			}
			completion.session.pending--
			if !completion.session.connected && completion.session.pending == 0 {
				delete(r.active, message.CollectorID)
			}
			completion.done <- err
		}
	}
	r.mu.Unlock()
}

func (r *Receiver) AcknowledgeBatch(messages []pipeline.Message, err error) {
	for _, message := range messages {
		r.Acknowledge(message, err)
	}
}

func (r *Receiver) Connect(stream grpc.BidiStreamingServer[lgmonv1.CollectorMessage, lgmonv1.MonitorMessage]) error {
	ctx := stream.Context()
	// Until pairing introduces mTLS, plaintext streams are restricted to the local machine.
	if remote, ok := peer.FromContext(ctx); ok && r.authorize == nil {
		if address, ok := remote.Addr.(*net.TCPAddr); ok && !address.IP.IsLoopback() {
			return status.Error(codes.PermissionDenied, "尚未配置 mTLS，只允许本机回环采集器")
		}
	}
	first, err := stream.Recv()
	receivedHelloAt := r.now().UnixMilli()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(hello.CollectorId) || len(hello.Hostname) > 255 || len(hello.Version) > 128 || hello.LastAckedSeq > pipeline.MaxSequence || hello.CurrentTime < 0 {
		return status.Error(codes.InvalidArgument, "首条消息必须为有效的 Hello")
	}
	identity := Hello{CollectorID: hello.CollectorId, Hostname: hello.Hostname, Version: hello.Version, LastAckedSeq: hello.LastAckedSeq, Capabilities: slices.Clone(hello.Capabilities)}
	if r.authorize != nil {
		if err := r.authorize(ctx, identity); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	if r.active[hello.CollectorId] != nil {
		r.mu.Unlock()
		return status.Error(codes.AlreadyExists, "同一采集器已经连接")
	}
	session := &linkSession{connected: true, cancel: cancel, identity: identity, processRequests: make(chan *lgmonv1.ProcessQuery, 8), processResults: map[string]chan *lgmonv1.ProcessList{}, done: make(chan struct{})}
	session.serviceSwitchRequests = make(chan *lgmonv1.ServiceSwitchQuery, 8)
	session.serviceSwitchResults = map[string]chan *lgmonv1.ServiceSwitchSummary{}
	r.active[hello.CollectorId] = session
	r.configStates[hello.CollectorId] = AwaitingCollectorConfig()
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		session.connected = false
		close(session.done)
		// Queued records retain their session until committed; a reconnect must not steal their receipts.
		if session.pending == 0 {
			delete(r.active, hello.CollectorId)
		}
		r.mu.Unlock()
	}()
	// A revoke between the first authorization and registration must still win.
	if r.authorize != nil {
		if err := r.authorize(ctx, identity); err != nil {
			return err
		}
	}
	identity.CurrentTime = hello.CurrentTime
	identity.ClockOffsetMS, identity.ClockKnown = r.observeClock(identity.CollectorID, hello.CurrentTime, receivedHelloAt, true)
	if r.register != nil {
		if err := r.register(ctx, identity); err != nil {
			return status.Error(codes.Unavailable, "保存采集器信息失败")
		}
	}
	r.mu.Lock()
	r.capabilities[identity.CollectorID] = slices.Clone(identity.Capabilities)
	delete(r.cacheStates, identity.CollectorID)
	r.mu.Unlock()
	if err := grpc.SetSendCompressor(ctx, gzip.Name); err != nil {
		return err
	}
	expected := hello.LastAckedSeq + 1
	if r.resume != nil {
		committed, err := r.resume(ctx, hello.CollectorId)
		if err != nil {
			return status.Error(codes.Unavailable, "读取连续确认水位失败")
		}
		expected = committed + 1
	}
	if err := stream.Send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_Welcome{Welcome: &lgmonv1.Welcome{Accepted: true, ResumeFrom: expected}}}); err != nil {
		return err
	}
	var sendMu sync.Mutex
	send := func(message *lgmonv1.MonitorMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(message)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case query := <-session.processRequests:
				if err := send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_ProcessQuery{ProcessQuery: query}}); err != nil {
					cancel()
					return
				}
			case query := <-session.serviceSwitchRequests:
				if err := send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_ServiceSwitchQuery{ServiceSwitchQuery: query}}); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	applied := make(chan *lgmonv1.ConfigApplied, 1)
	if r.config.Source != nil {
		go func() {
			if err := r.pushConfig(ctx, session, send, applied); err != nil {
				r.config.Logger.Warn("Configuration delivery interrupted", "collectorId", identity.CollectorID)
				cancel()
			}
		}()
	}
	type incoming struct {
		message    *lgmonv1.CollectorMessage
		err        error
		receivedAt int64
	}
	requests := make(chan incoming, 1)
	go func() {
		for {
			message, err := stream.Recv()
			receivedAt := r.now().UnixMilli()
			if err == nil && message.GetProcessList() != nil {
				r.processReply(session, message.GetProcessList())
				continue
			}
			if err == nil && message.GetServiceSwitchSummary() != nil {
				r.serviceSwitchReply(session, message.GetServiceSwitchSummary())
				continue
			}
			select {
			case requests <- incoming{message, err, receivedAt}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var envelope *lgmonv1.CollectorMessage
		var err error
		var receivedAt int64
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "采集连接已取消或证书已吊销")
		case request := <-requests:
			envelope, err = request.message, request.err
			receivedAt = request.receivedAt
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if heartbeat := envelope.GetHeartbeat(); heartbeat != nil {
			if heartbeat.Ts <= 0 {
				return status.Error(codes.InvalidArgument, "心跳时间无效")
			}
			current := identity
			if heartbeat.CacheMaxBytes != 0 || heartbeat.CacheBytes != 0 || heartbeat.CacheBytesPerHour != nil {
				cache, err := pipeline.NewCacheUsage(heartbeat.CacheBytes, heartbeat.CacheMaxBytes, heartbeat.CacheBytesPerHour)
				if err != nil {
					return status.Error(codes.InvalidArgument, "采集器缓存统计无效")
				}
				current.Cache = &cache
			}
			current.CurrentTime = heartbeat.Ts
			current.ClockOffsetMS, current.ClockKnown = r.observeClock(identity.CollectorID, heartbeat.Ts, receivedAt, false)
			if r.heartbeat != nil {
				if err := r.heartbeat(ctx, current); err != nil {
					return status.Error(codes.Unavailable, "采集器心跳未能保存，请重新连接")
				}
			}
			r.mu.Lock()
			if r.active[identity.CollectorID] == session {
				r.cacheStates[identity.CollectorID] = current.Cache.Clone()
			}
			r.mu.Unlock()
			continue
		}
		if result := envelope.GetConfigApplied(); result != nil && r.config.Source != nil {
			if len(result.Version) != 64 || len(result.Error) > 1024 {
				return status.Error(codes.InvalidArgument, "配置回执无效")
			}
			select {
			case applied <- result:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		batch := envelope.GetBatch()
		if batch == nil || len(batch.Records) == 0 || len(batch.Records) > 256 {
			return status.Error(codes.InvalidArgument, "Hello 后须发送包含 1～256 条记录的 Batch")
		}
		// Validate the complete batch before accepting any part of a malformed request.
		messages := make([]pipeline.Message, len(batch.Records))
		next, previous := expected, uint64(0)
		for i, record := range batch.Records {
			message, err := Decode(hello.CollectorId, record)
			if err == nil && message.Inventory != nil && !slices.Contains(identity.Capabilities, InventoryCapability) {
				return status.Error(codes.InvalidArgument, "清单消息需要采集器声明 inventory 能力")
			}
			if err != nil || (i > 0 && message.Seq <= previous) {
				return status.Error(codes.InvalidArgument, "记录无效或序号不连续")
			}
			previous = message.Seq
			if message.Seq >= next {
				if message.DataGap != nil {
					if message.DataGap.FromSeq > next {
						return status.Error(codes.InvalidArgument, "缺口无法覆盖所需连续序号")
					}
				} else if message.Seq != next {
					return status.Error(codes.InvalidArgument, "采集记录跳过了未确认序号")
				}
				next = message.Seq + 1
			}
			messages[i] = message
		}
		fresh := messages[:0]
		for _, message := range messages {
			if message.Seq >= expected {
				fresh = append(fresh, message)
			}
		}
		if len(fresh) > 0 {
			completion := &commitReceipt{done: make(chan error, 1), session: session, remaining: len(fresh)}
			r.mu.Lock()
			session.pending++
			for _, message := range fresh {
				key := receiptKey{message.CollectorID, message.Seq}
				completion.keys = append(completion.keys, key)
				r.pending[key] = completion
			}
			r.mu.Unlock()
			if err := r.queue.WriteBatch(ctx, fresh); err != nil {
				r.AcknowledgeBatch(fresh, err)
				return status.Error(codes.Unavailable, "采集消息接收端不可用")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-completion.done:
				if err != nil {
					return status.Error(codes.Unavailable, "采集消息未能提交，未确认此序号")
				}
			}
			expected = fresh[len(fresh)-1].Seq + 1
		}
		if err := send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_Ack{Ack: &lgmonv1.Ack{Seq: expected - 1}}}); err != nil {
			return err
		}
	}
}

type Writer struct {
	stream            grpc.BidiStreamingClient[lgmonv1.CollectorMessage, lgmonv1.MonitorMessage]
	id                string
	next              uint64
	resume            uint64
	cancel            context.CancelFunc
	mu                sync.Mutex
	sendMu            sync.Mutex
	applier           ConfigApplier
	closed            bool
	responses         chan *lgmonv1.MonitorMessage
	done              chan struct{}
	readErr           error
	processes         ProcessResponder
	serviceSwitch     collector.ServiceSwitchSource
	serviceSwitchBusy chan struct{}
}

func NewWriter(ctx context.Context, connection grpc.ClientConnInterface, hello Hello, applier ...ConfigApplier) (*Writer, error) {
	var configuration ConfigApplier
	if len(applier) > 0 {
		configuration = applier[0]
	}
	return NewWriterWithProcesses(ctx, connection, hello, configuration, nil)
}

func NewWriterWithProcesses(ctx context.Context, connection grpc.ClientConnInterface, hello Hello, applier ConfigApplier, processes ProcessResponder, clocks ...func() time.Time) (*Writer, error) {
	return NewWriterWithSources(ctx, connection, hello, applier, processes, nil, clocks...)
}

func NewWriterWithSources(ctx context.Context, connection grpc.ClientConnInterface, hello Hello, applier ConfigApplier, processes ProcessResponder, serviceSwitch collector.ServiceSwitchSource, clocks ...func() time.Time) (*Writer, error) {
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	capabilities := slices.Clone(hello.Capabilities)
	if processes != nil && !slices.Contains(capabilities, ProcessQueryCapability) {
		capabilities = append(capabilities, ProcessQueryCapability)
	}
	if serviceSwitch != nil && !slices.Contains(capabilities, ServiceSwitchCapability) {
		capabilities = append(capabilities, ServiceSwitchCapability)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := lgmonv1.NewCollectorLinkClient(connection).Connect(streamCtx, grpc.UseCompressor(gzip.Name), grpc.MaxCallSendMsgSize(MaxMessageSize), grpc.MaxCallRecvMsgSize(MaxMessageSize))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("建立采集连接失败：%w", err)
	}
	if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Hello{Hello: &lgmonv1.Hello{CollectorId: hello.CollectorID, Hostname: hello.Hostname, Version: hello.Version, LastAckedSeq: hello.LastAckedSeq, Capabilities: capabilities, CurrentTime: now().UnixMilli()}}}); err != nil {
		cancel()
		return nil, fmt.Errorf("发送采集身份失败：%w", err)
	}
	response, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("读取采集连接确认失败：%w", err)
	}
	welcome := response.GetWelcome()
	if welcome == nil || !welcome.Accepted || welcome.ResumeFrom == 0 || welcome.ResumeFrom > pipeline.MaxSequence {
		cancel()
		return nil, fmt.Errorf("监控器返回了无效连接确认")
	}
	writer := &Writer{stream: stream, id: hello.CollectorID, next: welcome.ResumeFrom, resume: welcome.ResumeFrom, cancel: cancel, responses: make(chan *lgmonv1.MonitorMessage, 1), done: make(chan struct{})}
	writer.applier, writer.processes = applier, processes
	writer.serviceSwitch, writer.serviceSwitchBusy = serviceSwitch, make(chan struct{}, 1)
	go writer.receive(streamCtx)
	return writer, nil
}

func (w *Writer) receive(ctx context.Context) {
	defer close(w.done)
	defer w.cancel()
	for {
		response, err := w.stream.Recv()
		if err != nil {
			w.readErr = err
			return
		}
		if query := response.GetProcessQuery(); query != nil {
			result := &lgmonv1.ProcessList{RequestId: query.RequestId, Error: "采集器未配置进程查询"}
			if w.processes != nil {
				result = w.processes.Answer(ctx, query)
			}
			if result == nil {
				result = &lgmonv1.ProcessList{RequestId: query.RequestId, Error: "采集器进程快照不可用"}
			}
			if err := w.send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_ProcessList{ProcessList: result}}); err != nil {
				w.readErr = err
				return
			}
			continue
		}
		if query := response.GetServiceSwitchQuery(); query != nil {
			select {
			case w.serviceSwitchBusy <- struct{}{}:
				go func() {
					defer func() { <-w.serviceSwitchBusy }()
					queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					result := answerServiceSwitch(queryCtx, w.serviceSwitch, query)
					if w.send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_ServiceSwitchSummary{ServiceSwitchSummary: result}}) != nil {
						w.cancel()
					}
				}()
			default:
				if err := w.send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_ServiceSwitchSummary{ServiceSwitchSummary: &lgmonv1.ServiceSwitchSummary{RequestId: query.RequestId, ErrorCode: "source_invalid", Error: "上一次服务开关读取尚未结束，请稍后重试"}}}); err != nil {
					w.readErr = err
					return
				}
			}
			continue
		}
		if push := response.GetConfig(); push != nil {
			result := &lgmonv1.ConfigApplied{Version: push.Version}
			if len(push.Version) != 64 {
				w.readErr = fmt.Errorf("配置版本无效")
				return
			}
			if w.applier == nil {
				result.Error = "采集器未配置目标缓存"
			} else if err := w.applier.Apply(ctx, push.Version, push.TargetsJson); err != nil {
				// Local validation or file errors can contain paths; keep the wire response bounded and private.
				result.Error = "配置校验或保存失败，已保留旧配置"
			} else {
				result.Ok = true
			}
			if err := w.send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_ConfigApplied{ConfigApplied: result}}); err != nil {
				w.readErr = err
				return
			}
			continue
		}
		select {
		case w.responses <- response:
		case <-ctx.Done():
			w.readErr = ctx.Err()
			return
		}
	}
}

func (w *Writer) send(message *lgmonv1.CollectorMessage) error {
	w.sendMu.Lock()
	defer w.sendMu.Unlock()
	return w.stream.Send(message)
}

func (w *Writer) ResumeFrom() uint64    { return w.resume }
func (w *Writer) Done() <-chan struct{} { return w.done }
func (w *Writer) Err() error {
	select {
	case <-w.done:
		return w.readErr
	default:
		return nil
	}
}

func (w *Writer) Write(ctx context.Context, message pipeline.Message) error {
	return w.WriteBatch(ctx, []pipeline.Message{message})
}

func (w *Writer) WriteBatch(ctx context.Context, messages []pipeline.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("采集连接已经关闭：%w", io.ErrClosedPipe)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发送采集记录已取消：%w", err)
	}
	if len(messages) == 0 || len(messages) > MaxBatchRecords {
		return fmt.Errorf("采集批次须包含 1～256 条记录")
	}
	next := w.next
	batch := &lgmonv1.Batch{}
	for _, message := range messages {
		if message.CollectorID != w.id || (message.Seq != next && !(message.DataGap != nil && message.DataGap.FromSeq == next && message.Seq >= next)) {
			return fmt.Errorf("采集身份不符或序号不连续")
		}
		record, err := Encode(message)
		if err != nil {
			return err
		}
		batch.Records = append(batch.Records, record)
		next = message.Seq + 1
	}
	envelope := &lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Batch{Batch: batch}}
	if proto.Size(envelope) > MaxMessageSize {
		return fmt.Errorf("采集消息超过 4 MiB 上限")
	}
	if len(messages) > 1 && proto.Size(envelope) > MaxBatchBytes {
		return fmt.Errorf("采集批次超过 1 MiB 上限")
	}
	stopCancellation := context.AfterFunc(ctx, w.cancel)
	defer stopCancellation()
	if err := w.send(envelope); err != nil {
		w.closed = true
		w.cancel()
		return fmt.Errorf("发送采集批次失败：%w", errors.Join(io.ErrClosedPipe, err))
	}
	var response *lgmonv1.MonitorMessage
	var err error
	select {
	case response = <-w.responses:
	case <-w.done:
		err = w.readErr
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil || response.GetAck() == nil || response.GetAck().Seq != next-1 {
		w.closed = true
		w.cancel()
		if err == nil {
			err = fmt.Errorf("确认序号与已发送批次不符")
		}
		return fmt.Errorf("读取采集提交确认失败：%w", errors.Join(io.ErrClosedPipe, err))
	}
	w.next = next
	return nil
}

func (w *Writer) Close() error {
	w.cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.sendMu.Lock()
	err := w.stream.CloseSend()
	w.sendMu.Unlock()
	<-w.done
	return err
}
