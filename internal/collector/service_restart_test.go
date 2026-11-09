package collector

import (
	"context"
	"errors"
	"testing"
)

type restartHandleFixture struct {
	state      string
	calls      []string
	startError error
}

func (s *restartHandleFixture) State() (string, error) {
	s.calls = append(s.calls, "query")
	return s.state, nil
}
func (s *restartHandleFixture) Stop() error {
	s.calls = append(s.calls, "stop")
	s.state = "stopped"
	return nil
}
func (s *restartHandleFixture) Start() error {
	s.calls = append(s.calls, "start")
	if s.startError == nil {
		s.state = "running"
	}
	return s.startError
}
func (s *restartHandleFixture) Close() error { s.calls = append(s.calls, "close"); return nil }

type restartManagerFixture struct {
	service *restartHandleFixture
	opened  string
}

func (m *restartManagerFixture) Open(_ context.Context, name string) (RestartServiceHandle, error) {
	m.opened = name
	return m.service, nil
}
func TestRestrictedServiceRestartUsesOnlyExactSCMHandleAndConfirmsRunning(t *testing.T) {
	for _, state := range []string{"running", "stopped", "pending"} {
		t.Run(state, func(t *testing.T) {
			h := &restartHandleFixture{state: state}
			m := &restartManagerFixture{service: h}
			err := (RestrictedServiceRestarter{Manager: m}).Restart(context.Background(), "FakeService")
			if m.opened != "FakeService" {
				t.Fatal("wrong SCM name")
			}
			if state == "pending" {
				if err == nil || len(h.calls) != 2 {
					t.Fatal(err, h.calls)
				}
				return
			}
			if err != nil || h.state != "running" || h.calls[len(h.calls)-1] != "close" {
				t.Fatal(err, h.calls)
			}
		})
	}
	h := &restartHandleFixture{state: "running", startError: errors.New("fake refused start")}
	err := (RestrictedServiceRestarter{Manager: &restartManagerFixture{service: h}}).Restart(context.Background(), "FakeService")
	if !errors.Is(err, ErrRestartUncertain) {
		t.Fatal("effect uncertainty lost", err)
	}
}
