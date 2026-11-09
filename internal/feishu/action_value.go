package feishu

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/LFenX/lg-monitor/internal/config"
)

type CardActionValue struct {
	Version   int    `json:"lgmonVersion"`
	AlertID   string `json:"alertId"`
	EventKey  string `json:"eventKey"`
	Operation string `json:"operation"`
	Signature string `json:"signature"`
}

func actionValueValid(value CardActionValue) bool {
	return value.Version == 1 && value.AlertID != "" && value.EventKey != "" && (value.Operation == "ack" || value.Operation == "silence_1h")
}
func actionScopeValid(key []byte, app config.FeishuApp, channel config.Notifier) bool {
	return len(key) == 32 && app.AppID != "" && (app.Domain == "feishu" || app.Domain == "lark") && channel.Type == "feishu_app" && channel.CardActions && channel.ID != "" && channel.App == app.ID && channel.ChatID != ""
}
func cardActionMAC(key []byte, app config.FeishuApp, channel config.Notifier, value CardActionValue) []byte {
	data, _ := json.Marshal([]any{"lgmon-card-action-v1", app.ID, app.AppID, app.Domain, channel.ID, channel.ChatID, value.Version, value.AlertID, value.EventKey, value.Operation})
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func SignCardAction(key []byte, app config.FeishuApp, channel config.Notifier, alertID, eventKey, operation string) (CardActionValue, error) {
	value := CardActionValue{Version: 1, AlertID: alertID, EventKey: eventKey, Operation: operation}
	if !actionScopeValid(key, app, channel) || !actionValueValid(value) {
		return CardActionValue{}, fmt.Errorf("卡片动作需要有效渠道授权、告警事件、固定动作及签名密钥")
	}
	value.Signature = hex.EncodeToString(cardActionMAC(key, app, channel, value))
	return value, nil
}

func ParseCardActionValue(data []byte) (CardActionValue, error) {
	var value CardActionValue
	if len(data) == 0 || len(data) > 16384 {
		return value, fmt.Errorf("卡片动作值大小无效")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("卡片动作字段无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return value, fmt.Errorf("卡片动作含多余内容")
	}
	if !actionValueValid(value) || len(value.Signature) != 64 {
		return value, fmt.Errorf("卡片动作版本、身份或签名格式无效")
	}
	if _, err := hex.DecodeString(value.Signature); err != nil {
		return value, fmt.Errorf("卡片动作签名格式无效")
	}
	return value, nil
}

func VerifyCardAction(key []byte, app config.FeishuApp, channel config.Notifier, value CardActionValue) bool {
	if !actionScopeValid(key, app, channel) || !actionValueValid(value) || len(value.Signature) != 64 {
		return false
	}
	signature, err := hex.DecodeString(value.Signature)
	if err != nil {
		return false
	}
	return hmac.Equal(signature, cardActionMAC(key, app, channel, value))
}

func CardOperatorHash(key []byte, appID, operator string) string {
	data, _ := json.Marshal([]string{"lgmon-card-operator-v1", appID, operator})
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
