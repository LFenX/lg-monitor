//go:build windows

package collector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func inventoryDispatch(parent *ole.IDispatch, method bool, name string, args ...any) (*ole.IDispatch, func(), error) {
	var value *ole.VARIANT
	var err error
	if method {
		value, err = oleutil.CallMethod(parent, name, args...)
	} else {
		value, err = oleutil.GetProperty(parent, name, args...)
	}
	if err != nil {
		return nil, nil, err
	}
	dispatch := value.ToIDispatch()
	if dispatch == nil {
		value.Clear()
		return nil, nil, fmt.Errorf("计划任务接口没有返回对象")
	}
	return dispatch, func() { value.Clear() }, nil
}

func taskProperty(task *ole.IDispatch, name string) (any, error) {
	value, err := oleutil.GetProperty(task, name)
	if err != nil {
		return nil, err
	}
	defer value.Clear()
	if name == "LastRunTime" || name == "NextRunTime" {
		if value.VT != ole.VT_DATE {
			return nil, fmt.Errorf("计划任务日期类型无效")
		}
		if math.Float64frombits(uint64(value.Val)) <= 0 {
			return int64(0), nil
		}
		date, err := ole.GetVariantDate(uint64(value.Val))
		if err != nil {
			return nil, fmt.Errorf("转换计划任务日期失败：%w", err)
		}
		// Task Scheduler DATE values contain local wall time, not UTC fields.
		local := time.Date(date.Year(), date.Month(), date.Day(), date.Hour(), date.Minute(), date.Second(), 0, time.Local)
		return max(int64(0), local.UnixMilli()), nil
	}
	return value.Value(), nil
}

func taskInteger(value any) (int64, bool) {
	switch v := value.(type) {
	case int32:
		return int64(v), true
	case int:
		return int64(v), true
	case uint32:
		return int64(v), true
	case int64:
		return v, true
	}
	return 0, false
}

func readTaskEntry(task *ole.IDispatch) (InventoryEntry, error) {
	values := map[string]any{}
	for _, name := range []string{"Path", "Name", "Enabled", "State", "LastRunTime", "NextRunTime", "LastTaskResult", "NumberOfMissedRuns"} {
		value, err := taskProperty(task, name)
		if err != nil {
			return InventoryEntry{}, fmt.Errorf("读取计划任务属性失败：%w", err)
		}
		values[name] = value
	}
	path, pathOK := values["Path"].(string)
	name, nameOK := values["Name"].(string)
	enabled, enabledOK := values["Enabled"].(bool)
	state, stateOK := taskInteger(values["State"])
	last, lastOK := values["LastRunTime"].(int64)
	next, nextOK := values["NextRunTime"].(int64)
	result, resultOK := taskInteger(values["LastTaskResult"])
	missed, missedOK := taskInteger(values["NumberOfMissedRuns"])
	if !pathOK || !nameOK || !enabledOK || !stateOK || !lastOK || !nextOK || !resultOK || !missedOK || state < 0 || state > 4 || missed < 0 || missed > math.MaxUint32 || result < math.MinInt32 || result > math.MaxUint32 {
		return InventoryEntry{}, fmt.Errorf("计划任务返回的只读属性类型无效")
	}
	states := []string{"unknown", "disabled", "queued", "ready", "running"}
	return InventoryEntry{ID: strings.ToLower(path), Name: name, Enabled: enabled, State: states[state], LastRun: last, NextRun: next, LastResult: uint32(result), MissedRuns: uint32(missed)}, nil
}

func inventoryCollectionCount(collection *ole.IDispatch) (int, error) {
	value, err := taskProperty(collection, "Count")
	if err != nil {
		return 0, err
	}
	count, ok := taskInteger(value)
	if count > MaxInventoryEntries {
		return 0, errInventoryLimit
	}
	if !ok || count < 0 {
		return 0, fmt.Errorf("计划任务集合超过限额或数量无效")
	}
	return int(count), nil
}

func readScheduledTasks(ctx context.Context) ([]InventoryEntry, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var code *ole.OleError
		if !errors.As(err, &code) || code.Code() != 1 {
			return nil, fmt.Errorf("初始化计划任务 COM 失败：%w", err)
		}
	}
	defer ole.CoUninitialize()
	unknown, err := oleutil.CreateObject("Schedule.Service")
	if err != nil {
		return nil, fmt.Errorf("打开计划任务读取接口失败：%w", err)
	}
	defer unknown.Release()
	service, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, err
	}
	defer service.Release()
	connected, err := oleutil.CallMethod(service, "Connect")
	if err != nil {
		return nil, fmt.Errorf("连接本机计划任务失败：%w", err)
	}
	connected.Clear()
	root, release, err := inventoryDispatch(service, true, "GetFolder", `\`)
	if err != nil {
		return nil, err
	}
	defer release()
	entries := []InventoryEntry{}
	folders := 0
	var visit func(*ole.IDispatch, int) error
	visit = func(folder *ole.IDispatch, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		folders++
		if folders > 1024 || depth > 64 {
			return errInventoryLimit
		}
		tasks, done, err := inventoryDispatch(folder, true, "GetTasks", 1)
		if err != nil {
			return err
		}
		defer done()
		count, err := inventoryCollectionCount(tasks)
		if err != nil {
			return err
		}
		for i := 1; i <= count; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			task, closeTask, err := inventoryDispatch(tasks, false, "Item", i)
			if err != nil {
				return err
			}
			entry, err := readTaskEntry(task)
			closeTask()
			if err != nil {
				return err
			}
			entries = append(entries, entry)
			if len(entries) > MaxInventoryEntries {
				return errInventoryLimit
			}
		}
		children, closeChildren, err := inventoryDispatch(folder, true, "GetFolders", 0)
		if err != nil {
			return err
		}
		defer closeChildren()
		count, err = inventoryCollectionCount(children)
		if err != nil {
			return err
		}
		for i := 1; i <= count; i++ {
			child, closeChild, err := inventoryDispatch(children, false, "Item", i)
			if err != nil {
				return err
			}
			err = visit(child, depth+1)
			closeChild()
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root, 0); err != nil {
		return nil, fmt.Errorf("读取完整计划任务清单失败：%w", err)
	}
	return entries, nil
}
