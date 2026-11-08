package config

import (
	"fmt"
	"regexp"
	"strconv"
)

const DefaultCacheMaxBytes int64 = 500_000_000
const MinimumCacheMaxBytes int64 = 64_000_000
const MaximumCacheMaxBytes int64 = 10_000_000_000

type CacheSettings struct {
	MaxSize string `yaml:"maxSize,omitempty" json:"maxSize,omitempty" jsonschema:"pattern=^[0-9]+(B|KB|MB|GB)$" jsonschema_description:"采集器本机离线缓存物理上限，默认 500MB，范围 64MB～10GB；单位为十进制，不随目标下发。"`
}

var cacheSizePattern = regexp.MustCompile(`^([0-9]+)(B|KB|MB|GB)$`)

func (c CacheSettings) MaxBytes() (int64, error) {
	if c.MaxSize == "" {
		return DefaultCacheMaxBytes, nil
	}
	matches := cacheSizePattern.FindStringSubmatch(c.MaxSize)
	if len(matches) != 3 {
		return 0, fmt.Errorf("cache.maxSize 请使用 500MB 等整数大小，单位为 B、KB、MB 或 GB（十进制）")
	}
	value, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cache.maxSize 数值无效：%w", err)
	}
	unit := map[string]uint64{"B": 1, "KB": 1000, "MB": 1000000, "GB": 1000000000}[matches[2]]
	if value > uint64(MaximumCacheMaxBytes)/unit || value*unit < uint64(MinimumCacheMaxBytes) {
		return 0, fmt.Errorf("cache.maxSize 必须在 64MB～10GB 之间（十进制）")
	}
	return int64(value * unit), nil
}
