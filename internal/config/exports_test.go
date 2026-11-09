package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestExportsDefaultsAndLocatedValidation(t *testing.T) {
	cfg, err := Load(fstest.MapFS{})
	if err != nil || cfg.Settings.Exports.Prometheus.Enabled || cfg.Settings.Exports.OTLP.Enabled || cfg.Settings.Exports.OTLP.EffectiveInterval() != 30*time.Second || cfg.Settings.Exports.OTLP.EffectiveTimeout() != 10*time.Second {
		t.Fatal(cfg, err)
	}
	for _, endpoint := range []string{"http://127.0.0.1:4318/v1/metrics", "http://[::1]:4318/v1/metrics", "http://localhost:4318/v1/metrics", "https://example.com/collector/v1/metrics"} {
		data := "exports:\n  otlp:\n    enabled: true\n    endpoint: '" + endpoint + "'\n"
		if _, err := Load(fstest.MapFS{"settings.yaml": {Data: []byte(data)}}); err != nil {
			t.Fatal(endpoint, err)
		}
	}
	for _, field := range []string{"endpoint: http://192.0.2.20:4318/v1/metrics", "endpoint: https://fake-user:fake-pass@example.com/v1/metrics", "endpoint: https://example.com/v1/metrics?token=fake", "endpoint: https://0.0.0.0/v1/metrics", "endpoint: https://example.com/v1/metrics#fragment", "interval: 1s", "timeout: 31s"} {
		data := "exports:\n  otlp:\n    " + field + "\n"
		_, err := Load(fstest.MapFS{"settings.yaml": {Data: []byte(data)}})
		if err == nil || !strings.Contains(err.Error(), "settings.yaml 第 3 行") {
			t.Fatal("missing validation or location", field, err)
		}
	}
	for _, data := range []string{"exports:\n  otlp: {enabled: true}\n", "exports:\n  otlp:\n    tls: {certRef: fake-client-cert}\n", "exports:\n  otlp: {authorization: fake-plaintext}\n"} {
		if _, err := Load(fstest.MapFS{"settings.yaml": {Data: []byte(data)}}); err == nil {
			t.Fatal("accepted invalid settings", data)
		}
	}
	valid := map[string]any{"exports": map[string]any{"prometheus": map[string]any{"enabled": true}, "otlp": map[string]any{"enabled": true, "endpoint": "https://example.com/v1/metrics", "authorizationRef": "fake-otlp-header", "tls": map[string]any{"certRef": "fake-cert", "keyRef": "fake-key"}}}}
	if err := compiledSchemas(t)["settings.schema.json"].Validate(valid); err != nil {
		t.Fatal("generated settings schema rejects exporter config", err)
	}
}
