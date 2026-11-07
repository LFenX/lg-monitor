package config

import (
	"fmt"
	"math"
	"time"
)

type QueryParams struct {
	Range     *Duration `json:"range,omitempty"`
	From      *int64    `json:"from,omitempty"`
	To        *int64    `json:"to,omitempty"`
	Collector *string   `json:"collector,omitempty"`
}
type QueryValues struct {
	From      int64   `json:"from"`
	To        int64   `json:"to"`
	Collector *string `json:"collector" jsonschema:"nullable"`
}

func ResolveQueryParams(input QueryParams, defaultRange Duration, now func() time.Time) (QueryValues, error) {
	value := QueryValues{To: now().UnixMilli()}
	if input.To != nil {
		value.To = *input.To
	}
	if input.Range != nil && input.From != nil {
		return value, fmt.Errorf("range 和 from 不能同时提交")
	}
	if input.Collector != nil {
		collector := *input.Collector
		value.Collector = &collector
	}
	if input.From != nil {
		value.From = *input.From
	} else {
		span := defaultRange
		if span == "" {
			span = "1h"
		}
		if input.Range != nil {
			span = *input.Range
		}
		duration, err := span.Parse()
		if err != nil || duration.Milliseconds() <= 0 {
			return value, fmt.Errorf("时间范围须为大于零的整毫秒时长，例如 1h、7d")
		}
		milliseconds := duration.Milliseconds()
		if value.To < math.MinInt64+milliseconds {
			return value, fmt.Errorf("时间范围超出可表示范围")
		}
		value.From = value.To - milliseconds
	}
	if value.From >= value.To {
		return value, fmt.Errorf("from 必须早于 to")
	}
	return value, nil
}
func (v QueryValues) Bindings() map[string]any {
	var collector any
	if v.Collector != nil {
		collector = *v.Collector
	}
	return map[string]any{"from": v.From, "to": v.To, "collector": collector}
}
