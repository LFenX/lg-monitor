package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
)

func (b *APIBackend) alertEventLogs(ctx context.Context, r api.Request) (any, error) {
	source, ok := b.options.Read.(interface {
		AlertEventLogs(context.Context, string, int, string) (store.EventLogPage, error)
	})
	if !ok {
		return nil, fmt.Errorf("告警事件读取依赖不可用")
	}
	limit := 200
	if value := r.Query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return nil, invalidAPI("limit 必须为 1～500 的整数")
		}
		limit = parsed
	}
	result, err := source.AlertEventLogs(ctx, r.Params["id"], limit, r.Query.Get("cursor"))
	if errors.Is(err, store.ErrEventLogPage) {
		return nil, invalidAPI(err.Error())
	}
	return result, err
}
