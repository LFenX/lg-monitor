package setup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
)

func CheckPermissions(ctx context.Context, client feishu.Client, app config.FeishuApp) Step {
	permissions := RequiredPermissions()
	scopes, err := client.AppScopes(ctx)
	if err != nil {
		result := failure("permissions", err)
		var apiError *feishu.Error
		if errors.As(err, &apiError) && apiError.Kind == feishu.MissingScopes && len(apiError.Scopes) > 0 {
			if result.URL == "" {
				result.URL = applyURL(app, apiError.Scopes)
			}
			for _, name := range apiError.Scopes {
				if !slices.ContainsFunc(permissions, func(p Permission) bool { return p.Name == name }) {
					permissions = append(permissions, Permission{Name: name, Required: true, Description: "飞书接口提示需要开通的应用身份权限"})
				}
			}
		}
		result.Permissions = permissions
		return result
	}
	granted := make(map[string]bool)
	for _, scope := range scopes {
		if scope.Type == "tenant" && scope.Granted {
			granted[scope.Name] = true
		}
	}
	var missing []string
	for i := range permissions {
		permissions[i].Granted = granted[permissions[i].Name]
		if permissions[i].Required && !permissions[i].Granted {
			missing = append(missing, permissions[i].Name)
		}
	}
	result := Step{Step: "permissions", Status: OK, Message: "发送和读取群所需的应用身份权限均已开通", Permissions: permissions}
	if len(missing) > 0 {
		result.Status = ActionRequired
		result.Message = "请开通应用身份权限：" + strings.Join(missing, "、")
		result.URL = applyURL(app, missing)
	}
	return result
}

func QueryChats(ctx context.Context, client feishu.Client, botName string) Step {
	chats, err := client.ListChats(ctx)
	if err != nil {
		return failure("chat", err)
	}
	result := Step{Step: "chat", Status: OK, Message: "请选择接收告警的群", Chats: slices.Clone(chats)}
	if len(chats) == 0 {
		result.Status = ActionRequired
		result.Message = fmt.Sprintf("在飞书群中打开右上角设置 → 群机器人 → 添加机器人 → 搜索「%s」→ 添加。搜不到时检查应用是否已发布、你是否在“版本管理与发布 → 可用范围”内、机器人能力是否已生效。", botName)
		return result
	}
	unknown := 0
	for i := range result.Chats {
		info, err := client.ChatInfo(ctx, result.Chats[i].ID)
		if err != nil {
			unknown++
			if detail := failure("chat", err); detail.URL != "" {
				result.URL = detail.URL
			}
			continue
		}
		result.Chats[i].MemberCount = info.MemberCount
		if info.Name != "" {
			result.Chats[i].Name = info.Name
		}
	}
	if unknown > 0 {
		result.Message += fmt.Sprintf("；%d 个群的详情查询失败，人数显示为未知，可检查权限后重试", unknown)
	}
	return result
}

func Inspect(ctx context.Context, app config.FeishuApp, client feishu.Client) Report {
	result := Report{Steps: make([]Step, 0, 4)}
	if _, err := client.TenantToken(ctx); err != nil {
		result.Steps = append(result.Steps, failure("credentials", err))
		return result
	}
	result.Steps = append(result.Steps, Step{Step: "credentials", Status: OK, Message: "应用凭证有效"})
	bot, err := client.BotInfo(ctx)
	if err != nil {
		result.Steps = append(result.Steps, failure("bot", err))
	} else if bot.ActivateStatus != 2 {
		result.Steps = append(result.Steps, Step{Step: "bot", Status: ActionRequired, Message: BotMessage, URL: appURL(app.Domain)})
	} else {
		result.Steps = append(result.Steps, Step{Step: "bot", Status: OK, Message: "机器人已启用：" + bot.Name})
	}
	result.Steps = append(result.Steps, CheckPermissions(ctx, client, app), QueryChats(ctx, client, bot.Name))
	return result
}
