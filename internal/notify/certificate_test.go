package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestWebhookCertificateDataIsOptionalAndUsesEventSnapshot(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("fake-local", 8*3600))
	input := cards.Sample("firing")
	input.Rule.Type = "cert_expiry"
	input.Alert.Cert = &rules.Certificate{NotAfter: now.Add(3 * 24 * time.Hour).UnixMilli(), Subject: "fake certificate"}
	input.Alert.Summary = input.Alert.Cert.Message(now)
	payload := WebhookPayload(input, now, false)
	certificate := payload.Data.Cert
	if certificate == nil || certificate.DaysLeft != 3 || string(certificate.NotAfter) != "2030-01-04T19:04:05.000Z" || !strings.Contains(payload.Data.Alert.Summary, "证书将在 3 天后到期") {
		t.Fatalf("webhook certificate changed: %+v", payload)
	}
	input.Rule.Type = "probe_failed"
	encoded, err := json.Marshal(WebhookPayload(input, now, false))
	if err != nil || strings.Contains(string(encoded), `"cert":`) {
		t.Fatalf("ordinary webhook gained certificate: %s %v", encoded, err)
	}
}
