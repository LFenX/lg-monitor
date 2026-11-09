//go:build windows

package collector

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type etwWnode struct {
	BufferSize, ProviderID uint32
	HistoricalContext      uint64
	Timestamp              int64
	GUID                   windows.GUID
	ClientContext, Flags   uint32
}

type etwTraceProperties struct {
	Wnode                                                       etwWnode
	BufferSize, MinimumBuffers, MaximumBuffers, MaximumFileSize uint32
	LogFileMode, FlushTimer, EnableFlags                        uint32
	AgeLimit                                                    int32
	NumberOfBuffers, FreeBuffers, EventsLost, BuffersWritten    uint32
	LogBuffersLost, RealTimeBuffersLost                         uint32
	LoggerThread                                                uintptr
	LogFileNameOffset, LoggerNameOffset                         uint32
}

type etwLogHeader struct {
	Prefix                             [72]byte
	TimeZone                           [172]byte
	Alignment                          uint32
	BootTime, PerfFrequency, StartTime int64
	ReservedFlags, BuffersLost         uint32
}

type etwLogFile struct {
	LogFileName, LoggerName        *uint16
	CurrentTime                    int64
	BuffersRead, Mode              uint32
	CurrentEvent                   [88]byte
	Header                         etwLogHeader
	BufferCallback                 uintptr
	BufferSize, Filled, EventsLost uint32
	EventCallback                  uintptr
	IsKernelTrace                  uint32
	Context                        uintptr
}

type etwDescriptor struct {
	ID                              uint16
	Version, Channel, Level, Opcode uint8
	Task                            uint16
	Keyword                         uint64
}

type etwEventHeader struct {
	Size, HeaderType, Flags, EventProperty uint16
	ThreadID, ProcessID                    uint32
	Timestamp                              int64
	Provider                               windows.GUID
	Descriptor                             etwDescriptor
	KernelTime, UserTime                   uint32
	Activity                               windows.GUID
}

type etwRecord struct {
	Header                            etwEventHeader
	BufferContext                     uint32
	ExtendedDataCount, UserDataLength uint16
	ExtendedData, UserData, Context   uintptr
}

type etwPropertyDescriptor struct {
	Name                 uint64
	ArrayIndex, Reserved uint32
}

var (
	etwControl         = windows.NewLazySystemDLL("advapi32.dll")
	etwTDH             = windows.NewLazySystemDLL("tdh.dll")
	etwProcessProvider = windows.GUID{Data1: 0x3d6fa8d0, Data2: 0xfe05, Data3: 0x11d0, Data4: [8]byte{0x9d, 0xda, 0, 0xc0, 0x4f, 0xd7, 0xba, 0x7c}}
	etwActive          sync.Map
	etwNextID          atomic.Uint64
	etwCallback        = windows.NewCallback(etwReceiveRecord)
	etwPrivilegeMu     sync.Mutex
)

type etwProcessConsumer struct {
	ctx      context.Context
	now      func() time.Time
	notify   func(ProcessEvent)
	filter   ProcessEventFilter
	names    map[string]bool
	pids     map[int32]bool
	failures chan error
}

func etwProperty(record *etwRecord, property string, limit uint32) ([]byte, error) {
	name, err := windows.UTF16FromString(property)
	if err != nil {
		return nil, err
	}
	defer runtime.KeepAlive(name)
	descriptor := etwPropertyDescriptor{Name: uint64(uintptr(unsafe.Pointer(&name[0]))), ArrayIndex: math.MaxUint32}
	var size uint32
	status, _, _ := etwTDH.NewProc("TdhGetPropertySize").Call(uintptr(unsafe.Pointer(record)), 0, 0, 1, uintptr(unsafe.Pointer(&descriptor)), uintptr(unsafe.Pointer(&size)))
	if status != 0 {
		return nil, fmt.Errorf("读取 ETW %s 大小失败：%w", property, windows.Errno(status))
	}
	if size == 0 || size > limit {
		return nil, fmt.Errorf("ETW %s 大小无效：%d", property, size)
	}
	data := make([]byte, size)
	status, _, _ = etwTDH.NewProc("TdhGetProperty").Call(uintptr(unsafe.Pointer(record)), 0, 0, 1, uintptr(unsafe.Pointer(&descriptor)), uintptr(size), uintptr(unsafe.Pointer(&data[0])))
	if status != 0 {
		return nil, fmt.Errorf("读取 ETW %s 失败：%w", property, windows.Errno(status))
	}
	return data, nil
}

func eventProcessName(name string) string {
	name = name[strings.LastIndexAny(name, `\/`)+1:]
	return strings.TrimSuffix(strings.ToLower(name), ".exe")
}

func etwReceiveRecord(record *etwRecord) uintptr {
	if record == nil {
		return 0
	}
	value, exists := etwActive.Load(record.Context)
	if !exists {
		return 0
	}
	consumer := value.(*etwProcessConsumer)
	if consumer.ctx.Err() != nil || record.Header.Provider != etwProcessProvider {
		return 0
	}
	kind := ProcessEventKind("")
	switch record.Header.Descriptor.Opcode {
	case 1:
		kind = ProcessStarted
	case 2:
		kind = ProcessExited
	default:
		return 0
	}
	data, err := etwProperty(record, "ProcessId", 4)
	if err != nil || len(data) != 4 {
		if err == nil {
			err = fmt.Errorf("ETW 进程 PID 大小无效")
		}
		select {
		case consumer.failures <- err:
		default:
		}
		return 0
	}
	pid := binary.LittleEndian.Uint32(data)
	if pid == 0 || pid > math.MaxInt32 {
		return 0
	}
	image, err := etwProperty(record, "ImageFileName", 4096)
	if err != nil {
		select {
		case consumer.failures <- err:
		default:
		}
		return 0
	}
	for i, b := range image {
		if b == 0 {
			image = image[:i]
			break
		}
	}
	name := string(image)
	wanted := consumer.names[eventProcessName(name)]
	if kind == ProcessStarted {
		wanted = wanted || consumer.filter.AllStarts
	} else {
		wanted = wanted || consumer.pids[int32(pid)]
	}
	if wanted {
		consumer.notify(ProcessEvent{Kind: kind, PID: int32(pid), Name: name, TS: consumer.now().UnixMilli()})
	}
	return 0
}
