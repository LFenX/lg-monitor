package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type compressionLog struct {
	mu      sync.Mutex
	methods []string
}

type remoteStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s remoteStream) Context() context.Context { return s.ctx }
func (remoteStream) Send(*lgmonv1.MonitorMessage) error {
	panic("remote plaintext must be rejected first")
}
func (remoteStream) Recv() (*lgmonv1.CollectorMessage, error) {
	panic("remote plaintext must be rejected first")
}

func TestPlaintextRejectsRemotePeerBeforeReading(t *testing.T) {
	receiver := NewReceiver(nil)
	defer receiver.Close()
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.8"), Port: 1234}})
	if err := receiver.Connect(remoteStream{ctx: ctx}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("remote plaintext: %v", err)
	}
}

func TestCancelledSessionCannotDonateReceiptToReconnect(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- writer.Write(ctx, wireMessage(1)) }()
	old, err := receiver.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	if err := <-result; err == nil {
		t.Fatal("cancelled write succeeded")
	}
	if next, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"}); err == nil {
		next.Close()
		t.Fatal("reconnected while old record was still pending")
	}
	receiver.Acknowledge(old, nil)
	var next *Writer
	for {
		next, err = NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	defer next.Close()
	go func() { result <- next.Write(ctx, wireMessage(1)) }()
	message, err := receiver.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		t.Fatalf("reconnect returned without its own commit: %v", err)
	default:
	}
	receiver.Acknowledge(message, nil)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func (*compressionLog) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context   { return ctx }
func (*compressionLog) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (*compressionLog) HandleConn(context.Context, stats.ConnStats)                       {}
func (c *compressionLog) HandleRPC(_ context.Context, event stats.RPCStats) {
	if header, ok := event.(*stats.InHeader); ok {
		c.mu.Lock()
		c.methods = append(c.methods, header.Compression)
		c.mu.Unlock()
	}
}

func linkFixture(t *testing.T) (context.Context, *Receiver, *grpc.ClientConn, *compressionLog) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	listener := bufconn.Listen(1024 * 1024)
	receiver := NewReceiver(nil)
	compression := &compressionLog{}
	server := NewServer(receiver, grpc.StatsHandler(compression))
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); receiver.Close(); listener.Close() })
	connection, err := grpc.NewClient("passthrough:///memory", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStatsHandler(compression), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	return ctx, receiver, connection, compression
}

func wireMessage(seq uint64) pipeline.Message {
	return pipeline.Message{CollectorID: "fake-collector", Seq: seq, TS: 123456, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "fake-target", State: "down", Process: "missing", Port: "closed", Reason: "虚构原因"}}
}

func TestRecordRoundTripAndNoCommandLineField(t *testing.T) {
	messages := []pipeline.Message{
		wireMessage(1),
		{CollectorID: "fake-collector", Seq: 2, TS: 123456, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "fake-target", Probe: 3, TS: 123450, OK: true, LatencyMS: 10, StatusCode: 204}},
		{CollectorID: "fake-collector", Seq: 3, TS: 123456, Kind: pipeline.KindProcessSample, ProcessSample: &pipeline.ProcessSample{TargetID: "fake-target", PID: 42, CreatedAt: 123000, CPUPercent: 0.25, RSS: 128000}},
	}
	messages[0].TargetStatus.Processes = []pipeline.ProcessRef{{PID: 42, ParentPID: 2, Name: "fake.exe", Exe: `C:\fake.exe`, CreatedAt: 123000, CPUPercent: 0.25, RSS: 128000, Threads: 2}}
	messages[0].TargetStatus.Ports = []collector.Port{{LocalAddress: "127.0.0.1", Port: 12345, PID: 42}}
	for _, message := range messages {
		encoded, err := Encode(message)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(message.CollectorID, encoded)
		if err != nil || !reflect.DeepEqual(message, decoded) {
			t.Fatalf("round trip: %+v %+v %v", message, decoded, err)
		}
	}
	fields := (&lgmonv1.ProcessRef{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		if strings.Contains(string(fields.Get(i).Name()), "cmd") {
			t.Fatal("wire process reference exposes command line")
		}
	}
	if _, err := Decode("fake", &lgmonv1.Record{Seq: 1}); err == nil {
		t.Fatal("missing body accepted")
	}
	messages[0].TargetStatus.Ports[0].Port = 65536
	if _, err := Encode(messages[0]); err == nil {
		t.Fatal("invalid port accepted")
	}
}

func TestStreamWaitsForCommitAndUsesGzipBothWays(t *testing.T) {
	ctx, receiver, connection, compression := linkFixture(t)
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector", Hostname: "fake-host", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for seq := uint64(1); seq <= 3; seq++ {
		result := make(chan error, 1)
		go func() { result <- writer.Write(ctx, wireMessage(seq)) }()
		message, err := receiver.Read(ctx)
		if err != nil || message.Seq != seq || message.CollectorID != "fake-collector" {
			t.Fatalf("receive: %+v %v", message, err)
		}
		select {
		case err := <-result:
			t.Fatalf("write returned before commit: %v", err)
		default:
		}
		receiver.Acknowledge(message, nil)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	compression.mu.Lock()
	defer compression.mu.Unlock()
	if len(compression.methods) != 2 || compression.methods[0] != gzip.Name || compression.methods[1] != gzip.Name {
		t.Fatalf("inbound compression on client and server: %v", compression.methods)
	}
}

func TestStreamResumesPersistedWatermarkAcceptsGapAndDirectlyAcksDuplicates(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	receiver.resume = func(context.Context, string) (uint64, error) { return 3, nil }
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector", LastAckedSeq: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if writer.ResumeFrom() != 4 {
		t.Fatalf("did not use persisted monitor watermark: %d", writer.ResumeFrom())
	}
	gap := pipeline.Message{CollectorID: "fake-collector", Seq: 5, TS: 123456, Kind: pipeline.KindDataGap, DataGap: &pipeline.DataGap{FromSeq: 4, ToSeq: 5, Dropped: 2, Reason: "虚构缺口"}}
	for _, message := range []pipeline.Message{gap, wireMessage(6)} {
		finished := make(chan error, 1)
		go func() { finished <- writer.Write(ctx, message) }()
		received, err := receiver.Read(ctx)
		if err != nil || received.Seq != message.Seq {
			t.Fatalf("gap receive: %+v %v", received, err)
		}
		receiver.Acknowledge(received, nil)
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	first, _ := Encode(gap)
	second, _ := Encode(wireMessage(6))
	if err := writer.stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Batch{Batch: &lgmonv1.Batch{Records: []*lgmonv1.Record{first, second}}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-writer.responses:
		if response.GetAck() == nil || response.GetAck().Seq != 6 {
			t.Fatalf("duplicate batch acknowledgement: %+v", response)
		}
	case <-ctx.Done():
		t.Fatal("duplicates were sent through the consumer instead of directly acknowledged")
	}
}

func TestCommitFailureAndCancellationCloseWriter(t *testing.T) {
	for _, cancelWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed commit", true: "cancel write"}[cancelWrite], func(t *testing.T) {
			ctx, receiver, connection, _ := linkFixture(t)
			writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			writeCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- writer.Write(writeCtx, wireMessage(1)) }()
			message, err := receiver.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if cancelWrite {
				cancel()
			} else {
				receiver.Acknowledge(message, errors.New("fake persistence failure"))
			}
			if err := <-result; !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("terminal write error: %v", err)
			}
			if err := writer.Write(ctx, wireMessage(1)); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("closed write: %v", err)
			}
		})
	}
}

func TestProtocolRejectsFirstMessageSequenceDuplicateAndOversize(t *testing.T) {
	ctx, _, connection, _ := linkFixture(t)
	client := lgmonv1.NewCollectorLinkClient(connection)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Batch{Batch: &lgmonv1.Batch{}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing hello: %v", err)
	}
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"}); status.Code(errors.Unwrap(err)) != codes.AlreadyExists {
		t.Fatalf("duplicate collector: %v", err)
	}
	if err := writer.Write(ctx, wireMessage(2)); err == nil {
		t.Fatal("sequence gap accepted")
	}
	large := wireMessage(1)
	large.TargetStatus.Reason = strings.Repeat("x", MaxMessageSize)
	if err := writer.Write(ctx, large); err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Fatalf("client limit: %v", err)
	}
	raw, err := client.Connect(ctx, grpc.UseCompressor(gzip.Name), grpc.MaxCallSendMsgSize(2*MaxMessageSize))
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Hello{Hello: &lgmonv1.Hello{CollectorId: "other-collector"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Recv(); err != nil {
		t.Fatal(err)
	}
	record, _ := Encode(large)
	if err := raw.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Batch{Batch: &lgmonv1.Batch{Records: []*lgmonv1.Record{record}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Recv(); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("decompressed server limit: %v", err)
	}
}
