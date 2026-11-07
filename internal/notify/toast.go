package notify

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type Toast struct {
	Title   string
	Message string
}

type ToastResult struct {
	Skipped bool
	Reason  string
}

type ToastPlatform interface {
	Deliver(context.Context, []byte) (ToastResult, error)
}

type ToastClient interface {
	Show(context.Context, Toast) (ToastResult, error)
}

type ToastOptions struct {
	Platform ToastPlatform
	Logger   *slog.Logger
}

type toastClient struct{ options ToastOptions }

func NewToastClient(options ToastOptions) ToastClient {
	if options.Platform == nil {
		options.Platform = nativeToastPlatform()
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &toastClient{options: options}
}

func (c *toastClient) Show(parent context.Context, value Toast) (ToastResult, error) {
	if strings.TrimSpace(value.Title) == "" {
		return ToastResult{}, fmt.Errorf("Windows 通知标题不能为空")
	}
	text := func(value string, limit int) string {
		runes := []rune(value)
		if len(runes) > limit {
			return string(runes[:limit-1]) + "…"
		}
		return value
	}
	content := struct {
		XMLName xml.Name `xml:"toast"`
		Visual  struct {
			Binding struct {
				Template string   `xml:"template,attr"`
				Text     []string `xml:"text"`
			} `xml:"binding"`
		} `xml:"visual"`
	}{}
	content.Visual.Binding.Template = "ToastGeneric"
	content.Visual.Binding.Text = []string{text(value.Title, 128), text(value.Message, 1024)}
	data, err := xml.Marshal(content)
	if err != nil {
		return ToastResult{}, fmt.Errorf("编码 Windows 通知失败：%w", err)
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	result, err := c.options.Platform.Deliver(ctx, data)
	if result.Skipped {
		c.options.Logger.Warn("Windows notification skipped", "reason", result.Reason)
	}
	return result, err
}
