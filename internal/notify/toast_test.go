package notify

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type fakeToastPlatform struct {
	content []byte
	result  ToastResult
	err     error
	budget  time.Duration
}

func (p *fakeToastPlatform) Deliver(ctx context.Context, content []byte) (ToastResult, error) {
	p.content = bytes.Clone(content)
	if deadline, ok := ctx.Deadline(); ok {
		p.budget = time.Until(deadline)
	}
	return p.result, p.err
}

func TestToastXMLIsTextAndUsesBoundedDisplay(t *testing.T) {
	platform := &fakeToastPlatform{}
	client := NewToastClient(ToastOptions{Platform: platform})
	value := Toast{Title: `严重 <&> ' " $(fake)`, Message: strings.Repeat("🙂", 1100)}
	if result, err := client.Show(context.Background(), value); err != nil || result.Skipped {
		t.Fatalf("show=%+v,%v", result, err)
	}
	var document struct {
		Visual struct {
			Binding struct {
				Template string   `xml:"template,attr"`
				Text     []string `xml:"text"`
			} `xml:"binding"`
		} `xml:"visual"`
	}
	if err := xml.Unmarshal(platform.content, &document); err != nil {
		t.Fatal(err)
	}
	texts := document.Visual.Binding.Text
	if document.Visual.Binding.Template != "ToastGeneric" || len(texts) != 2 || texts[0] != value.Title || texts[1] != strings.Repeat("🙂", 1023)+"…" {
		t.Fatalf("wrong toast content: %+v", document)
	}
	if platform.budget <= 0 || platform.budget > 30*time.Second {
		t.Fatalf("unbounded notification: %s", platform.budget)
	}
	if _, err := client.Show(context.Background(), Toast{Title: " "}); err == nil {
		t.Fatal("empty notification title accepted")
	}
}

func TestToastSkipIsLoggedAndFailureRemainsVisible(t *testing.T) {
	var logs bytes.Buffer
	platform := &fakeToastPlatform{result: ToastResult{Skipped: true, Reason: "没有登录用户"}}
	client := NewToastClient(ToastOptions{Platform: platform, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	result, err := client.Show(context.Background(), Toast{Title: "fake-private-alert-title"})
	if err != nil || !result.Skipped || !strings.Contains(logs.String(), "没有登录用户") || strings.Contains(logs.String(), "fake-private-alert-title") {
		t.Fatalf("skip=%+v,%v logs=%s", result, err, &logs)
	}
	platform.result, platform.err = ToastResult{}, context.Canceled
	if _, err := client.Show(context.Background(), Toast{Title: "标题"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("platform error lost: %v", err)
	}
}
