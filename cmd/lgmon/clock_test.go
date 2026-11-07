package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/store"
)

func TestStatusShowsClockWarningAndClearsAfterSync(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: "fake-clock", Hostname: "虚构主机"}); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int64{40000, -300000, 0} {
		if err := db.SetClockOffset(ctx, "fake-clock", offset); err != nil {
			t.Fatal(err)
		}
		for _, asJSON := range []bool{false, true} {
			cmd := newStatusCommand(openStatus)
			cmd.PersistentFlags().String("home", home, "test home")
			args := []string{}
			if asJSON {
				args = append(args, "--json")
			}
			cmd.SetArgs(args)
			var output bytes.Buffer
			cmd.SetOut(&output)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if asJSON {
				var result statusResult
				if err := json.Unmarshal(output.Bytes(), &result); err != nil || len(result.Collectors) != 1 || result.Collectors[0].ClockOffsetMS != offset {
					t.Fatalf("status clocks=%s %v", output.String(), err)
				}
			} else if strings.Contains(output.String(), "请同步时间") != (offset != 0) {
				t.Fatalf("clock warning=%q", output.String())
			}
		}
	}
}
