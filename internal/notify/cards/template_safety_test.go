package cards

import (
	"strings"
	"testing"
	"time"
)

func TestTemplateStopsAtOutputLimit(t *testing.T) {
	r, err := New(nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = r.execute(`{{range .Alerts}}{{.Large}}{{end}}`, map[string]any{"Alerts": []any{map[string]any{"Large": strings.Repeat("x", 1024*1024)}}})
	if err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("large template output escaped limit: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("output limit did not stop promptly")
	}
	var output templateBuffer
	if n, err := output.Write([]byte(strings.Repeat("x", MaxTemplateOutput+1))); n != MaxTemplateOutput || err == nil || len(output.String()) != MaxTemplateOutput {
		t.Fatalf("bounded writer: %d %v", n, err)
	}
}
