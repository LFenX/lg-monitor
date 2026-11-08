//go:build windows

package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var eventLogDLL = windows.NewLazySystemDLL("wevtapi.dll")
var eventSubscribe = eventLogDLL.NewProc("EvtSubscribe")
var eventNext = eventLogDLL.NewProc("EvtNext")
var eventRender = eventLogDLL.NewProc("EvtRender")
var eventClose = eventLogDLL.NewProc("EvtClose")
var eventBookmarkCreate = eventLogDLL.NewProc("EvtCreateBookmark")
var eventBookmarkUpdate = eventLogDLL.NewProc("EvtUpdateBookmark")
var eventPublisherOpen = eventLogDLL.NewProc("EvtOpenPublisherMetadata")
var eventMessageFormat = eventLogDLL.NewProc("EvtFormatMessage")
var eventQuery = eventLogDLL.NewProc("EvtQuery")

type nativeEventLogBackend struct{}

func (nativeEventLogBackend) Anchor(ctx context.Context, channel string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	wide, err := windows.UTF16PtrFromString(channel)
	if err != nil {
		return "", err
	}
	query, _, err := eventQuery.Call(0, uintptr(unsafe.Pointer(wide)), 0, 1|0x200)
	if query == 0 {
		return "", fmt.Errorf("读取事件日志末尾游标失败：%w", err)
	}
	defer eventClose.Call(query)
	var handle uintptr
	var returned uint32
	ok, _, err := eventNext.Call(query, 1, uintptr(unsafe.Pointer(&handle)), 0, 0, uintptr(unsafe.Pointer(&returned)))
	if ok == 0 && errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
		return EventLogEmptyBookmark, nil
	}
	if ok == 0 {
		return "", fmt.Errorf("获取事件日志末条记录失败：%w", err)
	}
	if handle == 0 || returned != 1 {
		return "", fmt.Errorf("事件日志末条记录句柄无效")
	}
	defer eventClose.Call(handle)
	book, _, err := eventBookmarkCreate.Call(0)
	if book == 0 {
		return "", fmt.Errorf("创建事件日志起点书签失败：%w", err)
	}
	defer eventClose.Call(book)
	if ok, _, err := eventBookmarkUpdate.Call(book, handle); ok == 0 {
		return "", fmt.Errorf("记录事件日志起点失败：%w", err)
	}
	return renderWindowsEvent(book, 2)
}

type eventLogResult struct {
	item EventLogItem
	err  error
}

type windowsEventLogReader struct {
	values chan eventLogResult
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
}

func (nativeEventLogBackend) Open(parent context.Context, channel, query, bookmark string, oldest bool) (EventLogReader, error) {
	ctx, cancel := context.WithCancel(parent)
	reader := &windowsEventLogReader{values: make(chan eventLogResult, 1), done: make(chan struct{}), cancel: cancel}
	ready := make(chan error, 1)
	go reader.run(ctx, channel, query, bookmark, oldest, ready)
	select {
	case <-parent.Done():
		reader.Close()
		return nil, parent.Err()
	case err := <-ready:
		if err != nil {
			reader.Close()
			return nil, err
		}
		return reader, nil
	}
}

func (r *windowsEventLogReader) Next(ctx context.Context) (EventLogItem, error) {
	select {
	case <-ctx.Done():
		return EventLogItem{}, ctx.Err()
	case value, ok := <-r.values:
		if !ok {
			return EventLogItem{}, io.EOF
		}
		return value.item, value.err
	}
}

func (r *windowsEventLogReader) Close() error {
	r.once.Do(r.cancel)
	<-r.done
	return nil
}

func eventLogError(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_NOT_FOUND), errors.Is(err, windows.ERROR_EVT_QUERY_RESULT_STALE), errors.Is(err, windows.ERROR_EVT_QUERY_RESULT_INVALID_POSITION):
		return errors.Join(ErrEventLogBookmarkStale, err)
	case errors.Is(err, windows.ERROR_EVT_FILTER_TOO_COMPLEX), errors.Is(err, windows.ERROR_EVT_INVALID_QUERY):
		return errors.Join(ErrEventLogQueryComplex, err)
	default:
		return err
	}
}

func (r *windowsEventLogReader) run(ctx context.Context, channel, query, bookmark string, oldest bool, ready chan<- error) {
	// Keep all event handles on the worker that creates and consumes them.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(r.done)
	defer close(r.values)
	channelWide, err := windows.UTF16PtrFromString(channel)
	if err != nil {
		ready <- err
		return
	}
	queryWide, err := windows.UTF16PtrFromString(query)
	if err != nil {
		ready <- err
		return
	}
	var bookmarkWide *uint16
	if bookmark != "" {
		bookmarkWide, err = windows.UTF16PtrFromString(bookmark)
		if err != nil {
			ready <- err
			return
		}
	}
	book, _, err := eventBookmarkCreate.Call(uintptr(unsafe.Pointer(bookmarkWide)))
	if book == 0 {
		ready <- fmt.Errorf("创建事件日志书签失败：%w", eventLogError(err))
		return
	}
	defer eventClose.Call(book)
	signal, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		ready <- fmt.Errorf("创建事件日志通知句柄失败：%w", err)
		return
	}
	defer windows.CloseHandle(signal)
	flags := uintptr(1 | 0x10000)
	var resume uintptr
	if bookmark != "" {
		flags, resume = 3|0x10000, book
	} else if oldest {
		flags = 2 | 0x10000
	}
	subscription, _, err := eventSubscribe.Call(0, uintptr(signal), uintptr(unsafe.Pointer(channelWide)), uintptr(unsafe.Pointer(queryWide)), resume, 0, 0, flags)
	if subscription == 0 {
		ready <- fmt.Errorf("只读订阅 %s 事件日志失败：%w", channel, eventLogError(err))
		return
	}
	defer eventClose.Call(subscription)
	ready <- nil
	for ctx.Err() == nil {
		var handle uintptr
		var returned uint32
		ok, _, err := eventNext.Call(subscription, 1, uintptr(unsafe.Pointer(&handle)), 0, 0, uintptr(unsafe.Pointer(&returned)))
		if ok == 0 {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) || errors.Is(err, windows.ERROR_TIMEOUT) {
				if err := windows.ResetEvent(signal); err != nil {
					r.deliver(ctx, eventLogResult{err: fmt.Errorf("重置事件日志通知失败：%w", err)})
					return
				}
				// Poll again after the bounded wait, including a signal/reset race.
				if _, err := windows.WaitForSingleObject(signal, 250); err != nil {
					r.deliver(ctx, eventLogResult{err: fmt.Errorf("等待事件日志失败：%w", err)})
					return
				}
				continue
			}
			r.deliver(ctx, eventLogResult{err: fmt.Errorf("读取 %s 事件日志失败：%w", channel, eventLogError(err))})
			return
		}
		if returned != 1 || handle == 0 {
			r.deliver(ctx, eventLogResult{err: fmt.Errorf("事件日志返回的句柄数量无效")})
			return
		}
		item, readErr := readWindowsEventLog(channel, handle, book)
		eventClose.Call(handle)
		if !r.deliver(ctx, eventLogResult{item: item, err: readErr}) || readErr != nil {
			return
		}
	}
}

func (r *windowsEventLogReader) deliver(ctx context.Context, result eventLogResult) bool {
	select {
	case <-ctx.Done():
		return false
	case r.values <- result:
		return true
	}
}

func renderWindowsEvent(handle, flags uintptr) (string, error) {
	var used, properties uint32
	ok, _, err := eventRender.Call(0, handle, flags, 0, 0, uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
	if ok == 0 && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", err
	}
	if used == 0 || used > 1024*1024 || used%2 != 0 {
		return "", fmt.Errorf("事件日志渲染数据长度无效或超过 1 MiB")
	}
	buffer := make([]uint16, used/2)
	ok, _, err = eventRender.Call(0, handle, flags, uintptr(used), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
	if ok == 0 {
		return "", err
	}
	return windows.UTF16ToString(buffer), nil
}

func formatWindowsEvent(provider string, event uintptr) string {
	wide, err := windows.UTF16PtrFromString(provider)
	if err != nil {
		return ""
	}
	metadata, _, _ := eventPublisherOpen.Call(0, uintptr(unsafe.Pointer(wide)), 0, 0, 0)
	if metadata == 0 {
		return ""
	}
	defer eventClose.Call(metadata)
	var used uint32
	ok, _, err := eventMessageFormat.Call(metadata, event, 0, 0, 0, 1, 0, 0, uintptr(unsafe.Pointer(&used)))
	if ok == 0 && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || used == 0 || used > 512*1024 {
		return ""
	}
	buffer := make([]uint16, used)
	ok, _, _ = eventMessageFormat.Call(metadata, event, 0, 0, 0, 1, uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)))
	if ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer)
}

func readWindowsEventLog(channel string, event, bookmark uintptr) (EventLogItem, error) {
	xmlText, err := renderWindowsEvent(event, 1)
	if err != nil {
		return EventLogItem{}, fmt.Errorf("读取事件日志内容失败：%w", err)
	}
	row, err := parseEventLogXML(channel, xmlText, "")
	if err != nil {
		return EventLogItem{}, err
	}
	if formatted := formatWindowsEvent(row.Provider, event); formatted != "" {
		row.Message = eventLogText(formatted)
	}
	if ok, _, err := eventBookmarkUpdate.Call(bookmark, event); ok == 0 {
		return EventLogItem{}, fmt.Errorf("更新事件日志书签失败：%w", err)
	}
	saved, err := renderWindowsEvent(bookmark, 2)
	if err != nil {
		return EventLogItem{}, fmt.Errorf("序列化事件日志书签失败：%w", err)
	}
	return EventLogItem{Event: row, Bookmark: saved}, nil
}
