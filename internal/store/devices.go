// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/collector"
)

// DeviceSnapshot is one computer's newest system sample and the newest one that carried process and container usage.
type DeviceSnapshot struct {
	Latest *collector.SystemMetrics
	Usage  *collector.SystemMetrics
}

// usageAge is how far back the usage sample may be; older lists describe a machine that has moved on.
const usageAge = 10 * 60 * 1000

func (v *View) DeviceSnapshot(ctx context.Context, collectorID string) (DeviceSnapshot, error) {
	var result DeviceSnapshot
	read := func(query string, args ...any) (*collector.SystemMetrics, error) {
		var detail string
		err := v.db.QueryRowContext(ctx, query, args...).Scan(&detail)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("读取设备指标失败：%w", err)
		}
		var value collector.SystemMetrics
		if err := json.Unmarshal([]byte(detail), &value); err != nil {
			return nil, fmt.Errorf("解码设备指标失败：%w", err)
		}
		return &value, nil
	}
	var err error
	if result.Latest, err = read(`SELECT detail FROM system_metrics WHERE collector_id=? ORDER BY ts DESC LIMIT 1`, collectorID); err != nil || result.Latest == nil {
		return result, err
	}
	result.Usage, err = read(`SELECT detail FROM system_metrics WHERE collector_id=? AND ts>=? AND (json_extract(detail,'$.top') IS NOT NULL OR json_extract(detail,'$.containers') IS NOT NULL OR json_extract(detail,'$.containerError') IS NOT NULL) ORDER BY ts DESC LIMIT 1`, collectorID, result.Latest.TS-usageAge)
	return result, err
}
