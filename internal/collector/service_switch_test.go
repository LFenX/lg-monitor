package collector

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

type serviceSwitchFilesFunc func(context.Context, string) (io.ReadCloser, error)

func (f serviceSwitchFilesFunc) OpenServiceSwitch(ctx context.Context, path string) (io.ReadCloser, error) {
	return f(ctx, path)
}

func TestServiceSwitchReaderUsesOnlyFixedPathAndLimitsSource(t *testing.T) {
	now := time.UnixMilli(2000000000000)
	reader := ServiceSwitchReader{Settings: config.ServiceSwitchSettings{Path: `C:\fake\config\services.json`}, Now: func() time.Time { return now }}
	reads := 0
	reader.Files = serviceSwitchFilesFunc(func(_ context.Context, path string) (io.ReadCloser, error) {
		reads++
		if path != reader.Settings.Path {
			t.Fatal(path)
		}
		return io.NopCloser(strings.NewReader(`{"schemaVersion":1,"apps":[{"id":"fake","ports":[1234],"env":{"secret":"fake-private-secret"}}]}`)), nil
	})
	result, err := reader.ReadServiceSwitch(context.Background())
	if err != nil || result.ReadAt != now.UnixMilli() || len(result.SHA256) != 64 || reads != 1 {
		t.Fatalf("result=%+v reads=%d %v", result, reads, err)
	}
	if _, _, err := config.ServiceSwitchFingerprint(result.Summary); err != nil {
		t.Fatal(err)
	}
	reader.Settings.Path = ""
	if _, err := reader.ReadServiceSwitch(context.Background()); !errors.Is(err, ErrServiceSwitchNotConfigured) || reads != 1 {
		t.Fatal("unconfigured source read file")
	}
	reader.Settings.Path = `C:\fake\config\services.json`
	for _, text := range []string{strings.Repeat("x", config.MaxServiceSwitchFile+1), `{"schemaVersion":2}`, `{"invalid":"fake-private-secret",`} {
		reader.Files = serviceSwitchFilesFunc(func(context.Context, string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(text)), nil
		})
		_, err := reader.ReadServiceSwitch(context.Background())
		var source *ServiceSwitchError
		if !errors.As(err, &source) || strings.Contains(err.Error(), "fake-private-secret") {
			t.Fatalf("invalid source error=%v", err)
		}
	}
	reader.Files = serviceSwitchFilesFunc(func(context.Context, string) (io.ReadCloser, error) { return nil, errors.New("fake-private-path") })
	if _, err := reader.ReadServiceSwitch(context.Background()); err == nil || strings.Contains(err.Error(), "fake-private-path") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.ReadServiceSwitch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
