package app

import (
	"context"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type pairedPresence struct {
	pairs     transport.PairRepository
	db        *store.Store
	link      *transport.Receiver
	now       func() time.Time
	startedAt int64
}

func (s pairedPresence) ClockOffset(id string) int64 { return s.link.ClockOffset(id) }

func (s pairedPresence) Collectors(ctx context.Context) ([]rules.CollectorPresence, error) {
	paired, err := s.pairs.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]rules.CollectorPresence, 0, len(paired))
	for _, identity := range paired {
		if identity.Revoked {
			continue
		}
		metadata, found, err := s.db.Collector(ctx, identity.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			metadata = store.CollectorRecord{ID: identity.ID, Hostname: identity.Hostname, FirstSeen: identity.IssuedAt, LastSeen: identity.IssuedAt}
			if err := s.db.EnsureCollector(ctx, metadata); err != nil {
				return nil, err
			}
			metadata, _, err = s.db.Collector(ctx, identity.ID)
			if err != nil {
				return nil, err
			}
		}
		lastSeen := max(identity.IssuedAt, metadata.LastSeen)
		hostname := metadata.Hostname
		if hostname == "" {
			hostname = identity.Hostname
		}
		// Older versions used sample timestamps here; a future sample must not hide an absent collector forever.
		if lastSeen > s.now().UnixMilli() {
			lastSeen = s.startedAt
		}
		items = append(items, rules.CollectorPresence{ID: identity.ID, Hostname: hostname, LastSeen: lastSeen, Connected: s.link.Connected(identity.ID)})
	}
	return items, nil
}
