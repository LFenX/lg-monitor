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
}

type commitReceipt struct {
	done    chan error
	session *linkSession
}

type Receiver struct {
	lgmonv1.UnimplementedCollectorLinkServer
	queue    *pipeline.Channel
	register func(context.Context, Hello) error
	mu       sync.Mutex
	active   map[string]*linkSession
	pending  map[receiptKey]commitReceipt
}

func NewReceiver(register func(context.Context, Hello) error) *Receiver {
	return &Receiver{queue: pipeline.NewChannel(256), register: register, active: make(map[string]*linkSession), pending: make(map[receiptKey]commitReceipt)}
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
	if remote, ok := peer.FromContext(ctx); ok {
		if address, ok := remote.Addr.(*net.TCPAddr); ok && !address.IP.IsLoopback() {
			return status.Error(codes.PermissionDenied, "尚未配置 mTLS，只允许本机回环采集器")
		}
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(hello.CollectorId) || len(hello.Hostname) > 255 || len(hello.Version) > 128 || hello.LastAckedSeq == ^uint64(0) {
		return status.Error(codes.InvalidArgument, "首条消息必须为有效的 Hello")
	}
	r.mu.Lock()
	if r.active[hello.CollectorId] != nil {
		r.mu.Unlock()
		return status.Error(codes.AlreadyExists, "同一采集器已经连接")
	}
	session := &linkSession{connected: true}
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
	if r.register != nil {
		if err := r.register(ctx, Hello{CollectorID: hello.CollectorId, Hostname: hello.Hostname, Version: hello.Version, LastAckedSeq: hello.LastAckedSeq}); err != nil {
			return status.Error(codes.Unavailable, "保存采集器信息失败")
		}
	}
	if err := grpc.SetSendCompressor(ctx, gzip.Name); err != nil {
		return err
	}
	expected := hello.LastAckedSeq + 1
	if err := stream.Send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_Welcome{Welcome: &lgmonv1.Welcome{Accepted: true, ResumeFrom: expected}}}); err != nil {
		return err
	}
	for {
		envelope, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		batch := envelope.GetBatch()
		if batch == nil || len(batch.Records) == 0 || len(batch.Records) > 256 {
			return status.Error(codes.InvalidArgument, "Hello 后须发送包含 1～256 条记录的 Batch")
		}
		// Validate the complete batch before accepting any part of a malformed request.
		messages := make([]pipeline.Message, len(batch.Records))
		for i, record := range batch.Records {
			message, err := Decode(hello.CollectorId, record)
			if err != nil || message.Seq != expected+uint64(i) || message.Seq == ^uint64(0) {
				return status.Error(codes.InvalidArgument, "记录无效或序号不连续")
			}
			messages[i] = message
		}
		for _, message := range messages {
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
			expected++
		}
		if err := stream.Send(&lgmonv1.MonitorMessage{Body: &lgmonv1.MonitorMessage_Ack{Ack: &lgmonv1.Ack{Seq: expected - 1}}}); err != nil {
			return err
		}
	}
}

type Writer struct {
	stream grpc.BidiStreamingClient[lgmonv1.CollectorMessage, lgmonv1.MonitorMessage]
	id     string
	next   uint64
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
}

func NewWriter(ctx context.Context, connection grpc.ClientConnInterface, hello Hello) (*Writer, error) {
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
	if welcome == nil || !welcome.Accepted || welcome.ResumeFrom != hello.LastAckedSeq+1 {
		cancel()
		return nil, fmt.Errorf("监控器返回了无效连接确认")
	}
	return &Writer{stream: stream, id: hello.CollectorID, next: welcome.ResumeFrom, cancel: cancel}, nil
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
	if message.CollectorID != w.id || message.Seq != w.next {
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
	if err := w.stream.Send(envelope); err != nil {
		w.closed = true
		w.cancel()
		return fmt.Errorf("发送采集批次失败：%w", errors.Join(io.ErrClosedPipe, err))
	}
	response, err := w.stream.Recv()
	if err != nil || response.GetAck() == nil || response.GetAck().Seq != message.Seq {
		w.closed = true
		w.cancel()
		if err == nil {
			err = fmt.Errorf("确认序号与已发送批次不符")
		}
		return fmt.Errorf("读取采集提交确认失败：%w", errors.Join(io.ErrClosedPipe, err))
	}
	w.next++
	return nil
}

func (w *Writer) Close() error {
	w.cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return w.stream.CloseSend()
}
