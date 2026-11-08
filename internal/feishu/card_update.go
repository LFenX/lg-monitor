package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type CardUpdater interface {
	UpdateCard(context.Context, string, json.RawMessage) error
}

func (c *apiClient) UpdateCard(parent context.Context, id string, card json.RawMessage) error {
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "/\\?#%\r\n") || len(card) > 30000 || !json.Valid(card) {
		return fmt.Errorf("更新卡片需要合法消息 ID 和不超过 30 KB 的卡片 JSON")
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	token, err := c.TenantToken(ctx)
	if err != nil {
		return err
	}
	body := larkim.NewPatchMessageReqBodyBuilder().Content(string(card)).Build()
	response, err := c.im.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().MessageId(id).Body(body).Build(), larkcore.WithTenantAccessToken(token))
	path := "/open-apis/im/v1/messages/" + id
	if err != nil {
		return c.classify(path, 0, larkcore.CodeError{}, err)
	}
	if response == nil || response.ApiResp == nil {
		return fmt.Errorf("飞书更新卡片响应为空")
	}
	var code larkcore.CodeError
	if err := json.Unmarshal(response.RawBody, &code); err != nil {
		return c.classify(path, response.StatusCode, code, err)
	}
	if code.Code != 0 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.classify(path, response.StatusCode, code, nil)
	}
	return nil
}
