//go:build windows && integration && scale

package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
	"unsafe"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
	"golang.org/x/sys/windows"
)

var scaleClockDLL = windows.NewLazySystemDLL("kernel32.dll")
var scaleCounterProc = scaleClockDLL.NewProc("QueryPerformanceCounter")
var scaleFrequencyProc = scaleClockDLL.NewProc("QueryPerformanceFrequency")

func scaleClockValue(proc *windows.LazyProc) int64 {
	var value int64
	if ok, _, err := proc.Call(uintptr(unsafe.Pointer(&value))); ok == 0 {
		panic(fmt.Errorf("scale performance counter: %w", err))
	}
	return value
}

// QPC provides a common monotonic clock for both processes on this machine.
// https://learn.microsoft.com/en-us/windows/win32/sysinfo/acquiring-high-resolution-time-stamps
func scaleCounter() int64 { return scaleClockValue(scaleCounterProc) }

type scaleCommitStore struct {
	*store.Store
	output     io.Writer
	collectors map[string]uint64
}

func (s *scaleCommitStore) IngestBatch(ctx context.Context, messages []pipeline.Message) (store.IngressBatch, error) {
	batch, err := s.Store.IngestBatch(ctx, messages)
	at := scaleCounter()
	if err != nil || len(batch.Messages) == 0 {
		return batch, err
	}
	index, known := s.collectors[batch.Messages[0].CollectorID]
	first := batch.Messages[0].Seq
	for offset, message := range batch.Messages {
		if !known || message.Seq != first+uint64(offset) {
			return batch, fmt.Errorf("scale commit trace requires known collector and contiguous records")
		}
	}
	entry := [4]uint64{index, first, batch.Messages[len(batch.Messages)-1].Seq, uint64(at)}
	if err := binary.Write(s.output, binary.LittleEndian, entry); err != nil {
		return batch, fmt.Errorf("record scale commit timestamp: %w", err)
	}
	return batch, nil
}

func scaleCommitLatencies(input io.Reader, sent [][]int64, frequency int64) ([]float64, error) {
	if frequency <= 0 {
		return nil, fmt.Errorf("invalid performance counter frequency")
	}
	seen := make([][]bool, len(sent))
	for index := range sent {
		seen[index] = make([]bool, len(sent[index]))
	}
	var latencies []float64
	for {
		var entry [4]uint64
		if err := binary.Read(input, binary.LittleEndian, &entry); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read scale commit trace: %w", err)
		}
		index, first, through := entry[0], entry[1], entry[2]
		if index >= uint64(len(sent)) || first == 0 || first > through || through >= uint64(len(sent[index])) {
			return nil, fmt.Errorf("invalid scale commit trace range: %v", entry)
		}
		for seq := first; seq <= through; seq++ {
			start, end := sent[index][seq], int64(entry[3])
			if start <= 0 || end < start || seen[index][seq] {
				return nil, fmt.Errorf("missing send, negative interval or duplicate scale commit: collector=%d seq=%d", index, seq)
			}
			seen[index][seq] = true
			latencies = append(latencies, float64(end-start)/float64(frequency)*1000)
		}
	}
	for index := range sent {
		for seq := 1; seq < len(sent[index]); seq++ {
			if sent[index][seq] == 0 || !seen[index][seq] {
				return nil, fmt.Errorf("incomplete scale commit coverage: collector=%d seq=%d", index, seq)
			}
		}
	}
	return latencies, nil
}

func TestScaleCommitTimingPreservesFullCoverage(t *testing.T) {
	sent := [][]int64{{0, 10, 20}, {0, 30}}
	trace := func(entries ...[4]uint64) []byte {
		var buffer bytes.Buffer
		for _, entry := range entries {
			if err := binary.Write(&buffer, binary.LittleEndian, entry); err != nil {
				t.Fatal(err)
			}
		}
		return buffer.Bytes()
	}
	first, second := [4]uint64{0, 1, 2, 110}, [4]uint64{1, 1, 1, 130}
	valid := trace(first, second)
	values, err := scaleCommitLatencies(bytes.NewReader(valid), sent, 1000)
	if err != nil || len(values) != 3 || values[0] != 100 || values[1] != 90 || values[2] != 100 {
		t.Fatalf("commit timing: %v %v", values, err)
	}
	for name, invalid := range map[string][]byte{
		"missing": trace(first), "duplicate": trace(first, second, first), "truncated": valid[:len(valid)-1],
		"negative": trace([4]uint64{0, 1, 2, 5}, second), "unknown": trace(first, [4]uint64{2, 1, 1, 130}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scaleCommitLatencies(bytes.NewReader(invalid), sent, 1000); err == nil {
				t.Fatal("incomplete or invalid measurement accepted")
			}
		})
	}
}
