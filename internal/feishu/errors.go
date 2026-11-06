package feishu

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

type ErrorKind string

const (
	Credentials   ErrorKind = "credentials"
	MissingScopes ErrorKind = "missingScopes"
	BotDisabled   ErrorKind = "botDisabled"
	NotInChat     ErrorKind = "notInChat"
	RateLimited   ErrorKind = "rateLimited"
	Network       ErrorKind = "network"
	API           ErrorKind = "api"
)

type Error struct {
	Kind      ErrorKind `json:"kind"`
	Code      int       `json:"code"`
	Message   string    `json:"message"`
	Scopes    []string  `json:"scopes,omitempty"`
	URL       string    `json:"url,omitempty"`
	retryable bool
	cause     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }
func Retryable(err error) bool {
	var failure *Error
	return errors.As(err, &failure) && (failure.retryable || failure.Kind == Network || failure.Kind == RateLimited)
}

func ApplyURL(appID string, scopes []string) string {
	return "https://open.feishu.cn/page/scope-apply?clientID=" + url.QueryEscape(appID) + "&scopes=" + url.QueryEscape(strings.Join(scopes, ","))
}

var scopePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*(?::[a-z0-9_.-]+)+$`)
var scopeList = regexp.MustCompile(`\[([^\]]+)\]`)

func (c *apiClient) classify(path string, status int, code larkcore.CodeError, cause error) error {
	if cause != nil {
		return &Error{Kind: Network, Message: "飞书网络请求失败，请检查网络和代理设置", retryable: true, cause: cause}
	}
	text := strings.ToLower(c.redact(code.Msg))
	if status == http.StatusTooManyRequests || code.Code == 99991400 || code.Code == 11232 || code.Code == 11233 || strings.Contains(text, "rate limit") || strings.Contains(text, "frequency limit") || strings.Contains(text, "too frequently") {
		return &Error{Kind: RateLimited, Code: code.Code, Message: "飞书请求过于频繁，请稍后重试", retryable: true}
	}
	if status >= 500 {
		return &Error{Kind: Network, Code: code.Code, Message: "飞书服务暂时不可用，请稍后重试", retryable: true}
	}
	if code.Code == 99991672 {
		var scopes []string
		seen := make(map[string]bool)
		add := func(value string) {
			value = strings.Trim(strings.TrimSpace(value), "'\"")
			if scopePattern.MatchString(value) && !strings.Contains(value, c.secret) && !seen[value] {
				scopes = append(scopes, value)
				seen[value] = true
			}
		}
		if code.Err != nil {
			for _, violation := range code.Err.PermissionViolations {
				if violation != nil {
					add(violation.Subject)
				}
			}
		}
		for _, list := range scopeList.FindAllStringSubmatch(c.redact(code.Msg), -1) {
			for _, item := range strings.Split(list[1], ",") {
				add(item)
			}
		}
		link := ApplyURL(c.appID, scopes)
		if c.domain == "lark" {
			link = strings.Replace(link, "https://open.feishu.cn", "https://open.larksuite.com", 1)
		}
		message := "飞书应用缺少权限，请在开发者后台开通并发布新版本"
		if len(scopes) > 0 {
			message = fmt.Sprintf("飞书应用缺少权限 %s：请打开 %s 开通并发布新版本", strings.Join(scopes, "、"), link)
		}
		return &Error{Kind: MissingScopes, Code: code.Code, Message: message, Scopes: scopes, URL: link}
	}
	if strings.Contains(path, "tenant_access_token") || code.Code == 99991663 || code.Code == 99991665 {
		return &Error{Kind: Credentials, Code: code.Code, Message: "App ID 或 App Secret 不对，请去开发者后台的“凭证与基础信息”页重新复制"}
	}
	if code.Code == 230006 || strings.Contains(path, "/bot/v3/info") {
		return &Error{Kind: BotDisabled, Code: code.Code, Message: "机器人未启用，请去开发者后台“应用能力”中添加“机器人”"}
	}
	if code.Code == 230002 || code.Code == 10002 || code.Code == 10030 {
		return &Error{Kind: NotInChat, Code: code.Code, Message: "机器人不在告警群里，请把机器人重新拉进群"}
	}
	return &Error{Kind: API, Code: code.Code, Message: fmt.Sprintf("飞书接口请求失败（错误码 %d），请检查请求参数和应用状态", code.Code)}
}

func (c *apiClient) redact(value string) string {
	if c.secret != "" {
		value = strings.ReplaceAll(value, c.secret, "[redacted]")
	}
	return value
}
