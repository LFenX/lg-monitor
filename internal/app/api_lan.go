package app

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

// LANBoardList and LANBoardView let the member port show boards without the rest of the API.
func (b *APIBackend) LANBoardList(ctx context.Context) ([]api.BoardInfo, error) {
	return b.boardList(ctx, b.options.Config.Snapshot())
}

func (b *APIBackend) LANBoardView(ctx context.Context, id string) (api.BoardView, error) {
	return b.boardView(ctx, b.options.Config.Snapshot(), id)
}

func (b *APIBackend) lanOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	if b.options.Teams == nil {
		return nil, &api.Fault{Status: 503, Code: "lan_unavailable", Message: "内网共享需要以 monitor 或 both 运行"}
	}
	if op == api.GetLAN {
		return b.options.Teams.LANState(ctx)
	}
	var input api.LANConfigInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	settings := b.options.Config.Snapshot().Settings.LAN
	if input.Visible != nil {
		settings.Visible = *input.Visible
	}
	if input.Name != nil {
		settings.Name = strings.TrimSpace(*input.Name)
		if utf8.RuneCountInString(settings.Name) > 40 {
			return nil, invalidAPI("名字最多 40 个字")
		}
	}
	if settings.Visible {
		if err := b.options.Teams.EnsureLANTeam(ctx); err != nil {
			return nil, err
		}
	}
	if _, err := b.options.Editor.Update(ctx, "settings.yaml", func(old []byte) ([]byte, error) {
		return config.SetLAN(old, settings)
	}); err != nil {
		return nil, err
	}
	b.options.Teams.notify()
	// The member port restarts on the new address; give it a moment so the reply says how it went.
	for attempt := 0; attempt < 20; attempt++ {
		state, err := b.options.Teams.LANState(ctx)
		if err != nil || !settings.Visible || state.Listening && state.Problem == "" {
			return state, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return b.options.Teams.LANState(ctx)
}
