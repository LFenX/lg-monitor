package pipeline

import (
	"fmt"
	"math"
)

const CacheWALReserve int64 = 8 * 1024 * 1024

type CacheUsage struct {
	Bytes          int64    `json:"bytes" jsonschema:"minimum=0"`
	MaxBytes       int64    `json:"maxBytes" jsonschema:"minimum=1"`
	BytesPerHour   *float64 `json:"bytesPerHour" jsonschema:"nullable,minimum=0"`
	ProjectedHours *float64 `json:"projectedHours" jsonschema:"nullable,minimum=0"`
}

func NewCacheUsage(bytes, maxBytes int64, rate *float64) (CacheUsage, error) {
	value := CacheUsage{Bytes: bytes, MaxBytes: maxBytes}
	if bytes < 0 || maxBytes <= CacheWALReserve {
		return value, fmt.Errorf("缓存占用或生效上限无效")
	}
	if rate != nil {
		if *rate <= 0 || math.IsNaN(*rate) || math.IsInf(*rate, 0) {
			return value, fmt.Errorf("缓存每小时写入字节数必须为有限正数")
		}
		copy := *rate
		hours := float64(maxBytes-CacheWALReserve) / copy
		if math.IsInf(hours, 0) {
			return value, fmt.Errorf("缓存预计时长超出可表示范围")
		}
		value.BytesPerHour, value.ProjectedHours = &copy, &hours
	}
	return value, nil
}

func (c CacheUsage) Warning() string {
	if c.ProjectedHours == nil || *c.ProjectedHours >= 24 {
		return ""
	}
	return fmt.Sprintf("按当前数据量，断网时只能缓存约 %.1f 小时。可以减少目标，或者调大 cache.maxSize。", *c.ProjectedHours)
}

func (c *CacheUsage) Clone() *CacheUsage {
	if c == nil {
		return nil
	}
	copy := *c
	if c.BytesPerHour != nil {
		value := *c.BytesPerHour
		copy.BytesPerHour = &value
	}
	if c.ProjectedHours != nil {
		value := *c.ProjectedHours
		copy.ProjectedHours = &value
	}
	return &copy
}
