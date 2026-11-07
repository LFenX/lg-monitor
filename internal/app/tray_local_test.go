package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

func TestDesktopTrayLifecycleOnlyOwnsForegroundProcess(t *testing.T) {
	for _, tc := range []struct {
		name, role   string
		hidden, fail bool
	}{
		{"monitor", "monitor", false, false}, {"both", "both", false, false}, {"headless", "monitor", true, false}, {"collector", "collector", false, false}, {"startup failure", "monitor", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trayCalled, stopped := false, false
			err := RunDesktop(context.Background(), LocalOptions{Role: tc.role, NoTray: tc.hidden}, func(ctx context.Context, opts LocalOptions) error {
				if tc.fail {
					return errors.New("fake startup error")
				}
				if tc.hidden || tc.role == "collector" {
					if opts.TrayReady != nil {
						t.Error("headless callback registered")
					}
					return nil
				}
				opts.TrayReady(nil)
				<-ctx.Done()
				stopped = true
				return nil
			}, func(context.Context, TrayBackend) error { trayCalled = true; return nil })
			if tc.fail {
				if err == nil || trayCalled {
					t.Fatal("startup failure started tray")
				}
				return
			}
			want := !tc.hidden && tc.role != "collector"
			if err != nil || trayCalled != want || stopped != want {
				t.Fatalf("lifecycle: tray=%t stopped=%t err=%v", trayCalled, stopped, err)
			}
		})
	}
}

type localTrayEvents struct{ states []store.NotificationPause }

func (e *localTrayEvents) Publish(_ string, v any) error {
	e.states = append(e.states, v.(store.NotificationPause))
	return nil
}
func TestInProcessTrayUsesLocalPersistenceAndPublishesPause(t *testing.T) {
	f := newAPIFixture(t)
	events := &localTrayEvents{}
	b := localTray{read: f.view, write: f.db, events: events, url: "http://127.0.0.1:7444"}
	ctx := context.Background()
	if err := b.Pause(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	state, err := b.Status(ctx)
	if err != nil || !state.CanPause || !state.Pause.Paused || *state.Pause.By != "local" {
		t.Fatalf("local: %+v %v", state, err)
	}
	if err := b.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events.states) != 2 || !events.states[0].Paused || events.states[1].Paused {
		t.Fatal("local tray missed SSE")
	}
}
