package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeJobFilesReadWhileTheJobKeepsAppending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writer, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.WriteString("{\"a\":1}\n{\"b\":2}\n"); err != nil {
		t.Fatal(err)
	}
	files := NativeJobFiles{}
	read, err := files.ReadJobFile(context.Background(), path, 8, 1<<20)
	if err != nil || read.Size != 16 || string(read.Data) != "{\"b\":2}\n" || string(read.Head) != "{\"a\":1}\n{\"b\":2}\n" {
		t.Fatalf("read = %+v %v", read, err)
	}
	if _, err := writer.WriteString("{\"c\":3}\n"); err != nil {
		t.Fatal(err)
	}
	read, err = files.ReadJobFile(context.Background(), path, 16, 4)
	if err != nil || read.Size != 24 || string(read.Data) != "{\"c\"" {
		t.Fatalf("limited read = %+v %v", read, err)
	}
	if _, err := files.ReadJobFile(context.Background(), filepath.Join(t.TempDir(), "missing.jsonl"), 0, 10); err == nil || !strings.Contains(err.Error(), "文件不存在") {
		t.Fatalf("missing file error = %v", err)
	}
	if _, err := files.ReadJobFile(context.Background(), t.TempDir(), 0, 10); err == nil {
		t.Fatal("a directory was read as an event file")
	}
}

func TestJobEventRecordsCarryEitherAnEventOrAReason(t *testing.T) {
	valid := []JobEventRecord{
		{JobID: "aics-upload", Path: `D:\e.jsonl`, Line: 3, Event: []byte(`{"event_id":"e"}`)},
		{JobID: "aics-upload", Path: `D:\e.jsonl`, Line: 3, Error: "第 3 行：不是合法事件"},
		{JobID: "aics-upload", Path: `D:\e.jsonl`},
	}
	for _, record := range valid {
		if err := record.Validate(); err != nil {
			t.Fatalf("%+v: %v", record, err)
		}
	}
	invalid := []JobEventRecord{
		{JobID: "AICS", Path: `D:\e.jsonl`},
		{JobID: "aics-upload"},
		{JobID: "aics-upload", Path: `D:\e.jsonl`, Event: []byte(`{"event_id":"e"}`)},
		{JobID: "aics-upload", Path: `D:\e.jsonl`, Line: 1, Event: []byte(`{"event_id":"e"}`), Error: "x"},
		{JobID: "aics-upload", Path: `D:\e.jsonl`, Line: 1, Event: []byte(`not json`)},
		{JobID: "aics-upload", Path: `D:\e.jsonl`, Line: 1, Event: []byte(`"` + strings.Repeat("x", JobEventMaxBytes) + `"`)},
	}
	for _, record := range invalid {
		if err := record.Validate(); err == nil {
			t.Fatalf("accepted %+v", record)
		}
	}
}
