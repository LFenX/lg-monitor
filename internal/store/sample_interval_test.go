package store

import (
	"context"
	"testing"
)

func TestRawSamplingPeriodsPersistConfiguredAndLegacyValues(t *testing.T) {
	s := openStore(t, Options{})
	for index, period := range []int64{0, 500, 4000, 5000} {
		message := sampleMessage(uint64(index+1), 2000000000000+int64(index)*5000)
		message.ProcessSample.IntervalMillis = period
		if err := s.Write(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT interval_ms FROM process_samples ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, want := range []int64{2000, 500, 4000, 5000} {
		var got int64
		if !rows.Next() || rows.Scan(&got) != nil || got != want {
			t.Fatalf("configured period lost: got %d want %d", got, want)
		}
	}
	for _, period := range []int64{-1, 86400001} {
		message := sampleMessage(10, 2000000000000)
		message.ProcessSample.IntervalMillis = period
		if err := s.Write(context.Background(), message); err == nil {
			t.Fatal("invalid wire period was stored")
		}
	}
}
