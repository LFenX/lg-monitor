//go:build !windows

package collector

import (
	"context"
	"fmt"
)

type unsupportedInventory struct{}

func newInventoryBackend([]byte) inventoryBackend { return unsupportedInventory{} }
func (unsupportedInventory) Scopes(context.Context) ([]inventoryScope, error) {
	return nil, fmt.Errorf("当前系统不支持 Windows 启动项和计划任务采集")
}
func (unsupportedInventory) Read(context.Context, inventoryScope) ([]InventoryEntry, error) {
	return nil, fmt.Errorf("当前系统不支持 Windows 清单")
}
