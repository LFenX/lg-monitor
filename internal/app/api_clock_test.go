package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
)

func TestCollectorAPIExposesClockOffsetAndRecovery(t *testing.T) {
	f := newAPIFixture(t)
	for _, offset := range []int64{40000, -300000, 0} {
		if err := f.db.SetClockOffset(context.Background(), "fake-host", offset); err != nil {
			t.Fatal(err)
		}
		response := f.request(t, "GET", "/api/v1/collectors", "read", "", 200)
		var collectors []api.Collector
		if err := json.Unmarshal(response.Body.Bytes(), &collectors); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range collectors {
			if item.ID == "fake-host" {
				found = true
				if item.ClockOffsetMS != offset {
					t.Fatalf("clock offset=%d want=%d", item.ClockOffsetMS, offset)
				}
			}
		}
		if !found {
			t.Fatal("collector clock missing")
		}
	}
}
