package feishu

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestCardActionSignatureBindsApplicationDestinationAndFixedOperation(t *testing.T) {
	key := bytes.Repeat([]byte("f"), 32)
	app := config.FeishuApp{ID: "fake-app", AppID: "cli_fake_fixture", Domain: "feishu"}
	channel := config.Notifier{ID: "fake-channel", Type: "feishu_app", App: app.ID, ChatID: "oc_fake_group", CardActions: true}
	signed, err := SignCardAction(key, app, channel, "fake-alert", "firing:1000", "ack")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(signed)
	parsed, err := ParseCardActionValue(data)
	if err != nil || !VerifyCardAction(key, app, channel, parsed) {
		t.Fatal("signed callback rejected", err)
	}
	for _, field := range []string{"key", "app", "domain", "channel", "group", "disabled", "alert", "event", "operation", "version", "signature"} {
		changedApp, changedChannel, changedValue := app, channel, parsed
		changedKey := key
		switch field {
		case "key":
			changedKey = bytes.Repeat([]byte("x"), 32)
		case "app":
			changedApp.AppID = "cli_fake_other"
		case "domain":
			changedApp.Domain = "lark"
		case "channel":
			changedChannel.ID = "other"
		case "group":
			changedChannel.ChatID = "oc_fake_other"
		case "disabled":
			changedChannel.CardActions = false
		case "alert":
			changedValue.AlertID = "other-alert"
		case "event":
			changedValue.EventKey = "firing:2000"
		case "operation":
			changedValue.Operation = "silence_1h"
		case "version":
			changedValue.Version = 2
		case "signature":
			changedValue.Signature = strings.Repeat("0", 64)
		}
		if VerifyCardAction(changedKey, changedApp, changedChannel, changedValue) {
			t.Fatal("signature scope bypass", field)
		}
	}
	for _, invalid := range [][]byte{nil, []byte(`{}`), append(data, []byte(` {}`)...), []byte(strings.Replace(string(data), `"lgmonVersion":1`, `"lgmonVersion":1,"sql":"DELETE FROM alerts"`, 1)), []byte(strings.Replace(string(data), `"operation":"ack"`, `"operation":"restart"`, 1))} {
		if _, err := ParseCardActionValue(invalid); err == nil {
			t.Fatal("malformed or extensible command accepted")
		}
	}
	if first, second := CardOperatorHash(key, app.ID, "ou_fake_operator"), CardOperatorHash(key, "other-app", "ou_fake_operator"); first == second || len(first) != 64 || strings.Contains(first, "ou_fake") {
		t.Fatal("operator audit identity leaked or linked across applications")
	}
}
