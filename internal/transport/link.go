package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sync"

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

type Hello struct {
	CollectorID, Hostname, Version string
	LastAckedSeq                   uint64
}

type receiptKey struct {
	collector string
	seq       uint64
}

type linkSession struct {
	connected bool
	pending   int
	cancel    context.CancelFunc
}

type commitReceipt struct {
	done    chan error
	session *linkSession
}

type Receiver struct {
	lgmonv1.UnimplementedCollectorLinkServer
	queue     *pipeline.Channel
	register  func(context.Context, Hello) error
	authorize func(context.Context, Hello) error
	resume    func(context.Context, string) (uint64, error)
	config    ConfigDelivery
	heartbeat func(context.Context, Hello) error
	mu        sync.Mutex
	active    map[string]*linkSession
	pending   map[receiptKey]commitReceipt
}

func NewReceiver(register func(context.Context, Hello) error, authorize ...func(context.Context, Hello) error) *Receiver {
	receiver := &Receiver{queue: pipeline.NewChannel(256), register: register, active: make(map[string]*linkSession), pending: make(map[receiptKey]commitReceipt)}
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

func (r *Receiver) Read(ctx context.Context) (pipeline.Message, error) { return r.queue.Read(ctx) }
func (r *Receiver) Close() error                                       { return r.queue.Close() }

func (r *Receiver) Acknowledge(message pipeline.Message, err error) {
	r.mu.Lock()
	completion, exists := r.pending[receiptKey{message.CollectorID, message.Seq}]
	if exists {
		delete(r.pending, receiptKey{message.CollectorID, message.Seq})
		completion.session.pending--
		if !completion.session.connected && completion.session.pending == 0 {
			delete(r.active, message.CollectorID)
		}
		completion.done <- err
	}
	r.mu.Unlock()
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
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(hello.CollectorId) || len(hello.Hostname) > 255 || len(hello.Version) > 128 || hello.LastAckedSeq > pipeline.MaxSequence {
		return status.Error(codes.InvalidArgument, "首条消息必须为有效的 Hello")
	}
	identity := Hello{CollectorID: hello.CollectorId, Hostname: hello.Hostname, Version: hello.Version, LastAckedSeq: hello.LastAckedSeq}
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
	session := &linkSession{connected: true, cancel: cancel}
	r.active[hello.CollectorId] = session
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		session.connected = false
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
	if r.register != nil {
		if err := r.register(ctx, identity); err != nil {
			return status.Error(codes.Unavailable, "保存采集器信息失败")
		}
	}
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
	applied := make(chan *lgmonv1.ConfigApplied, 1)
	if r.config.Source != nil {
		go func() {
			if err := r.pushConfig(ctx, identity, send, applied); err != nil {
				r.config.Logger.Warn("Configuration delivery interrupted", "collectorId", identity.CollectorID)
				cancel()
			}
		}()
	}
	type incoming struct {
		message *lgmonv1.CollectorMessage
		err     error
	}
	requests := make(chan incoming, 1)
	go func() {
		for {
			message, err := stream.Recv()
			select {
			case requests <- incoming{message, err}:
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
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "采集连接已取消或证书已吊销")
		case request := <-requests:
			envelope, err = request.message, request.err
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if heartbeat := envelope.GetHeartbeat(); heartbeat != nil && r.heartbeat != nil {
			if heartbeat.Ts <= 0 {
				return status.Error(codes.InvalidArgument, "心跳时间无效")
			}
			if err := r.heartbeat(ctx, identity); err != nil {
				return status.Error(codes.Unavailable, "采集器心跳未能保存，请重新连接")
			}
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
		for _, message := range messages {
			if message.Seq < expected {
				continue
			}
			key := receiptKey{message.CollectorID, message.Seq}
			completion := make(chan error, 1)
			r.mu.Lock()
			session.pending++
			r.pending[key] = commitReceipt{done: completion, session: session}
			r.mu.Unlock()
			if err := r.queue.Write(ctx, message); err != nil {
				r.Acknowledge(message, err)
				return status.Error(codes.Unavailable, "采集消息接收端不可用")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-completion:
				if err != nil {
					return status.Error(codes.Unavailable, "采集消息未能提交，未确认此序号")
				}
			}
			expected = message.Seq + 1
		}
		if err := send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_Ack{Ack: &lgmonv1.Ack{Seq: expected - 1}}}); err != nil {
			return err
		}
	}
}

type Writer struct {
	stream    grpc.BidiStreamingClient[lgmonv1.CollectorMessage, lgmonv1.MonitorMessage]
	id        string
	next      uint64
	resume    uint64
	cancel    context.CancelFunc
	mu        sync.Mutex
	sendMu    sync.Mutex
	applier   ConfigApplier
	closed    bool
	responses chan *lgmonv1.MonitorMessage
	done      chan struct{}
	readErr   error
}

func NewWriter(ctx context.Context, connection grpc.ClientConnInterface, hello Hello, applier ...ConfigApplier) (*Writer, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := lgmonv1.NewCollectorLinkClient(connection).Connect(streamCtx, grpc.UseCompressor(gzip.Name), grpc.MaxCallSendMsgSize(MaxMessageSize), grpc.MaxCallRecvMsgSize(MaxMessageSize))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("建立采集连接失败：%w", err)
	}
	if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Hello{Hello: &lgmonv1.Hello{CollectorId: hello.CollectorID, Hostname: hello.Hostname, Version: hello.Version, LastAckedSeq: hello.LastAckedSeq}}}); err != nil {
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
	if len(applier) != 0 {
		writer.applier = applier[0]
	}
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("采集连接已经关闭：%w", io.ErrClosedPipe)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发送采集记录已取消：%w", err)
	}
	if message.CollectorID != w.id || (message.Seq != w.next && !(message.DataGap != nil && message.DataGap.FromSeq == w.next && message.Seq >= w.next)) {
		return fmt.Errorf("采集身份不符或序号不连续")
	}
	record, err := Encode(message)
	if err != nil {
		return err
	}
	envelope := &lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Batch{Batch: &lgmonv1.Batch{Records: []*lgmonv1.Record{record}}}}
	if proto.Size(envelope) > MaxMessageSize {
		return fmt.Errorf("采集消息超过 4 MiB 上限")
	}
	stopCancellation := context.AfterFunc(ctx, w.cancel)
	defer stopCancellation()
	if err := w.send(envelope); err != nil {
		w.closed = true
		w.cancel()
		return fmt.Errorf("发送采集批次失败：%w", errors.Join(io.ErrClosedPipe, err))
	}
	var response *lgmonv1.MonitorMessage
	select {
	case response = <-w.responses:
	case <-w.done:
		err = w.readErr
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil || response.GetAck() == nil || response.GetAck().Seq != message.Seq {
		w.closed = true
		w.cancel()
		if err == nil {
			err = fmt.Errorf("确认序号与已发送批次不符")
		}
		return fmt.Errorf("读取采集提交确认失败：%w", errors.Join(io.ErrClosedPipe, err))
	}
	w.next = message.Seq + 1
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
