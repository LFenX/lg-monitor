package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type fakeServiceSession struct {
	names              []string
	rows               map[string]Service
	errors             map[string]error
	enumerated, closed int
}

func (s *fakeServiceSession) Names(context.Context) ([]string, error) {
	s.enumerated++
	return s.names, nil
}
func (s *fakeServiceSession) Read(_ context.Context, name string) (Service, error) {
	return s.rows[strings.ToLower(name)], s.errors[strings.ToLower(name)]
}
func (s *fakeServiceSession) Close() error { s.closed++; return nil }

type fakeServiceBackend struct{ session *fakeServiceSession }

func (b fakeServiceBackend) Open(context.Context, bool) (serviceSession, error) {
	return b.session, nil
}

func TestServiceSourceKeepsUnreadableAndMissingFactsAndLookupAvoidsEnumeration(t *testing.T) {
	session := &fakeServiceSession{names: []string{"blocked", "running", "gone"}, rows: map[string]Service{"running": {Name: "running", State: ServiceRunning, StartType: "automatic", PID: 42}}, errors: map[string]error{"blocked": errors.New("fake denied"), "gone": errServiceMissing}}
	source := newServiceSource(fakeServiceBackend{session}, func() time.Time { return time.UnixMilli(1) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rows, err := source.Snapshot(context.Background())
	if err != nil || len(rows) != 3 || rows[0].State != ServiceUnknown || rows[1].State != ServiceMissing || rows[2].PID != 42 {
		t.Fatalf("service facts dropped or changed: %+v %v", rows, err)
	}
	selected, err := source.Lookup(context.Background(), []string{"RUNNING", "running"})
	if err != nil || len(selected) != 1 || session.enumerated != 1 || session.closed != 2 {
		t.Fatalf("target lookup enumerated all services: %+v %v", selected, err)
	}
}
