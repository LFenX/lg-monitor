package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"

	"github.com/LFenX/lg-monitor/internal/collector"
)

type unavailableInventory struct{}

func (unavailableInventory) Snapshot(context.Context) ([]collector.InventorySnapshot, error) {
	return nil, fmt.Errorf("本机清单密钥不可用，保留上次清单")
}

type inventoryKeyStore interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}

func inventorySource(ctx context.Context, vault inventoryKeyStore) (collector.InventorySource, error) {
	key, err := vault.Get(ctx, "lgmon-inventory-hmac")
	if errors.Is(err, fs.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, fmt.Errorf("创建本机启动项指纹密钥失败：%w", err)
		}
		if err = vault.Set(ctx, "lgmon-inventory-hmac", key); err != nil {
			clear(key)
			return nil, fmt.Errorf("加密保存本机启动项指纹密钥失败：%w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("读取本机启动项指纹密钥失败：%w", err)
	}
	defer clear(key)
	return collector.NewInventorySource(key)
}
