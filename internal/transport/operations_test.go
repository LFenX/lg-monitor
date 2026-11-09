package transport

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

func TestServiceOperationControlDoesNotBlockDataACKOrConsumeSequences(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	source, journal, _, executor, command := localOperationsFixture(t)
	executor.entered, executor.release = make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-executor.release:
		default:
			close(executor.release)
		}
	}()
	writer, err := NewWriterWithOperations(ctx, connection, Hello{CollectorID: "fake-collector"}, nil, nil, nil, source, journal.options.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	policy, err := receiver.OperationPolicy(ctx, "fake-collector")
	if err != nil || !policy.Enabled || len(policy.Services) != 1 {
		t.Fatal(policy, err)
	}
	result := make(chan collector.OperationResult, 1)
	operationErr := make(chan error, 1)
	go func() {
		value, err := receiver.ExecuteOperation(ctx, "fake-collector", command)
		result <- value
		operationErr <- err
	}()
	select {
	case <-executor.entered:
	case <-time.After(time.Second):
		t.Fatal("control request did not reach executor")
	}
	dataDone := make(chan error, 1)
	go func() { dataDone <- writer.Write(ctx, wireMessage(1)) }()
	record, err := receiver.Read(ctx)
	if err != nil || record.Seq != 1 {
		t.Fatal("operation occupied data sequence", record, err)
	}
	receiver.Acknowledge(record, nil)
	select {
	case err := <-dataDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service execution blocked sample ACK")
	}
	if value, err := receiver.OperationResult(ctx, "fake-collector", command.RequestID); err != nil || value.State != "running" {
		t.Fatal(value, err)
	}
	close(executor.release)
	if err := <-operationErr; err != nil {
		t.Fatal(err)
	}
	if value := <-result; value.State != "succeeded" {
		t.Fatal(value)
	}
}

func TestServiceOperationWireRejectsExtraCommandsAndForeignSessionReply(t *testing.T) {
	source, _, _, executor, command := localOperationsFixture(t)
	data, _ := json.Marshal(command)
	data = append(data[:len(data)-1], []byte(`,"service":"OtherService"}`)...)
	reply := answerOperation(context.Background(), source, &lgmonv1.OperationQuery{RequestId: "1", Kind: "execute", CommandJson: data})
	if reply.ErrorCode == "" || executor.calls.Load() != 0 {
		t.Fatal("wire accepted arbitrary service field", reply, executor.calls.Load())
	}
	receiver := NewReceiver(nil)
	defer receiver.Close()
	owned := make(chan *lgmonv1.OperationReply, 1)
	foreign := &linkSession{operationResults: map[string]chan *lgmonv1.OperationReply{}}
	receiver.operationReply(foreign, &lgmonv1.OperationReply{RequestId: "same-request", ResultJson: []byte(`{}`)})
	select {
	case <-owned:
		t.Fatal("foreign reply reached another session")
	default:
	}
	owner := &linkSession{operationResults: map[string]chan *lgmonv1.OperationReply{"same-request": owned}}
	receiver.operationReply(owner, &lgmonv1.OperationReply{RequestId: "same-request", ResultJson: []byte(`{}`)})
	select {
	case <-owned:
	case <-time.After(time.Second):
		t.Fatal("own correlated reply was not delivered")
	}
}

func TestServiceOperationClockRefusalAndDisconnectDoNotReplayAcceptedWork(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	source, journal, _, executor, command := localOperationsFixture(t)
	executor.entered, executor.release = make(chan struct{}), make(chan struct{})
	writer, err := NewWriterWithOperations(ctx, connection, Hello{CollectorID: "fake-collector"}, nil, nil, nil, source, journal.options.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	receiver.mu.Lock()
	clock := receiver.clocks["fake-collector"]
	delete(receiver.clocks, "fake-collector")
	receiver.mu.Unlock()
	_, err = receiver.ExecuteOperation(ctx, "fake-collector", command)
	var dispatch *OperationDispatchError
	if !errors.As(err, &dispatch) || dispatch.Sent || executor.calls.Load() != 0 {
		t.Fatal("unknown clock allowed dispatch", err, executor.calls.Load())
	}
	receiver.mu.Lock()
	receiver.clocks["fake-collector"] = clock
	receiver.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := receiver.ExecuteOperation(ctx, "fake-collector", command); done <- err }()
	select {
	case <-executor.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("operation was not accepted")
	}
	writer.Close()
	select {
	case err := <-done:
		if !errors.As(err, &dispatch) || !dispatch.Sent {
			t.Fatal("disconnect after acceptance claimed no effect", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disconnected request did not return")
	}
	close(executor.release)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		value, err := source.Result(ctx, command.RequestID)
		if err == nil && value.State == "succeeded" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("accepted work did not persist after disconnect", value, err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	reconnected, err := NewWriterWithOperations(ctx, connection, Hello{CollectorID: "fake-collector"}, nil, nil, nil, source, journal.options.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.Close()
	result, err := receiver.OperationResult(ctx, "fake-collector", command.RequestID)
	if err != nil || result.State != "succeeded" || executor.calls.Load() != 1 {
		t.Fatal("reconnected read repeated or lost execution", result, err, executor.calls.Load())
	}
}

func TestServiceOperationOfflineAndLegacyCollectorsRejectWithoutDisconnect(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := receiver.OperationPolicy(ctx, "missing"); !errors.Is(err, ErrCollectorOffline) {
		t.Fatal(err)
	}
	if _, err := receiver.OperationPolicy(ctx, "fake-collector"); !errors.Is(err, ErrCollectorOutdated) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := receiver.ExecuteOperation(cancelled, "fake-collector", collector.OperationCommand{}); err == nil {
		t.Fatal("legacy collector accepted operation")
	}
}
