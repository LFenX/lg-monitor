package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"unicode/utf8"
)

// JobEventMaxBytes bounds one checked event; the monitor refuses anything larger.
const JobEventMaxBytes = 16 << 10

// JobEventRecord is one line of a batch job's JSON Lines file, or a notice about the file. The collector checks
// and redacts each line before it leaves the machine; the monitor checks it again.
type JobEventRecord struct {
	JobID string `json:"jobId"`
	Path  string `json:"path"`
	// Line is 1-based; 0 is a notice about the file itself.
	Line  uint64          `json:"line,omitempty"`
	Event json.RawMessage `json:"event,omitempty"`
	Error string          `json:"error,omitempty"`
}

var jobIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func (r JobEventRecord) Validate() error {
	switch {
	case !jobIDPattern.MatchString(r.JobID):
		return fmt.Errorf("任务事件的任务 ID 无效")
	case r.Path == "" || utf8.RuneCountInString(r.Path) > 260 || !utf8.ValidString(r.Path):
		return fmt.Errorf("任务事件文件路径无效")
	case len(r.Error) > 1024 || !utf8.ValidString(r.Error):
		return fmt.Errorf("任务事件说明过长或不是有效文本")
	case len(r.Event) > JobEventMaxBytes || len(r.Event) > 0 && !json.Valid(r.Event):
		return fmt.Errorf("任务事件必须是不超过 16 KiB 的 JSON")
	case len(r.Event) > 0 && (r.Error != "" || r.Line == 0):
		return fmt.Errorf("任务事件要有行号，且不能同时带错误说明")
	}
	return nil
}

// JobFileRead is what one read of a job's file found.
type JobFileRead struct {
	Size int64
	// Head is the start of the file, so a replaced file is noticed even when it grew past the old offset.
	Head []byte
	// Data runs from the requested offset, at most the requested limit.
	Data []byte
}

// JobFileSource reads a job's event file without ever writing to it.
type JobFileSource interface {
	ReadJobFile(ctx context.Context, path string, offset int64, limit int) (JobFileRead, error)
}

// JobFileHeadBytes is how much of a file's start is compared to notice a replaced file.
const JobFileHeadBytes = 256

// NativeJobFiles reads job files from the local disk, opened read-only so the job keeps appending.
type NativeJobFiles struct{}

func (NativeJobFiles) ReadJobFile(ctx context.Context, path string, offset int64, limit int) (JobFileRead, error) {
	if err := ctx.Err(); err != nil {
		return JobFileRead{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return JobFileRead{}, jobFileError(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return JobFileRead{}, jobFileError(err)
	}
	if info.IsDir() {
		return JobFileRead{}, fmt.Errorf("这是一个目录，不是事件文件")
	}
	read := JobFileRead{Size: info.Size()}
	head := make([]byte, min(info.Size(), JobFileHeadBytes))
	if _, err := io.ReadFull(file, head); err != nil {
		return JobFileRead{}, jobFileError(err)
	}
	read.Head = head
	if offset >= info.Size() || limit <= 0 {
		return read, nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return JobFileRead{}, jobFileError(err)
	}
	read.Data = make([]byte, min(int64(limit), info.Size()-offset))
	n, err := io.ReadFull(file, read.Data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return JobFileRead{}, jobFileError(err)
	}
	read.Data = read.Data[:n]
	return read, nil
}

func jobFileError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("文件不存在：%w", err)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("采集器没有读取这个文件的权限：%w", err)
	}
	return fmt.Errorf("读取文件失败：%w", err)
}
