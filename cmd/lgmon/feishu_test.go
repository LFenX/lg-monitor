package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/spf13/cobra"
)

type cliFeishuClient struct {
	botCalls, scopeCalls int
	chats                []feishu.Chat
}

func (c *cliFeishuClient) TenantToken(context.Context) (string, error) { return "fake-token", nil }
func (c *cliFeishuClient) BotInfo(context.Context) (feishu.Bot, error) {
	c.botCalls++
	return feishu.Bot{Name: "虚构机器人", ActivateStatus: 2}, nil
}
func (c *cliFeishuClient) AppScopes(context.Context) ([]feishu.Scope, error) {
	c.scopeCalls++
	return []feishu.Scope{{Name: "im:message:send_as_bot", Type: "tenant", Granted: true}, {Name: "im:chat:read", Type: "tenant", Granted: true}}, nil
}
func (c *cliFeishuClient) ListChats(context.Context) ([]feishu.Chat, error) {
	return slices.Clone(c.chats), nil
}
func (c *cliFeishuClient) ChatInfo(_ context.Context, id string) (feishu.Chat, error) {
	count := 3
	return feishu.Chat{ID: id, Name: "虚构群", MemberCount: &count}, nil
}
func (c *cliFeishuClient) SendCard(context.Context, feishu.CardMessage) (feishu.SendResult, error) {
	panic("CLI must use audited delivery")
}
func (c *cliFeishuClient) CreateChat(context.Context, feishu.NewChat) (feishu.Chat, error) {
	panic("CLI must not create chats")
}
func (c *cliFeishuClient) Close() error { return nil }

type cliDelivery struct{ requests []notify.Request }

func (d *cliDelivery) Send(_ context.Context, req notify.Request) (feishu.SendResult, error) {
	d.requests = append(d.requests, req)
	return feishu.SendResult{MessageID: "om_fake_cli_message"}, nil
}

func cliFeishuFixture() (feishuDependencies, *cliFeishuClient, *cliDelivery, *memorySecrets) {
	client := &cliFeishuClient{chats: []feishu.Chat{{ID: "oc_fake_group", Name: "虚构群"}}}
	delivery := &cliDelivery{}
	vault := &memorySecrets{values: make(map[string][]byte)}
	deps := feishuDependencies{
		Secrets:   func(string, *slog.Logger) (secrets.Store, error) { return vault, nil },
		NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil },
		NewDelivery: func(context.Context, string, feishu.Client) (setup.Delivery, func() error, error) {
			return delivery, func() error { return nil }, nil
		},
		Now:     func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
		Wait:    func(context.Context, time.Duration) error { return context.Canceled },
		OpenURL: func(context.Context, string) error { return errors.New("unexpected browser call") },
	}
	return deps, client, delivery, vault
}

func runFeishu(deps feishuDependencies, home, input string, args ...string) (string, string, error) {
	command := newFeishuCommand(deps)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.PersistentFlags().String("home", home, "test home")
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetIn(strings.NewReader(input))
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func TestFeishuAutomationCheckChatsAndTest(t *testing.T) {
	deps, client, delivery, vault := cliFeishuFixture()
	home := t.TempDir()
	out, stderr, err := runFeishu(deps, home, "fake-CLI-private-secret\n", "setup", "--app-id", "cli_fake_cli", "--secret-stdin", "--chat-id", "oc_fake_group", "--template", "compact", "--yes", "--json")
	if err != nil || stderr != "" || strings.Contains(out, "fake-CLI-private-secret") {
		t.Fatalf("automation: %v %s %s", err, out, stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	var steps []setup.Step
	for {
		var step setup.Step
		if err := decoder.Decode(&step); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if step.Status != setup.OK || step.Message == "" {
			t.Fatalf("invalid automation step: %+v", step)
		}
		steps = append(steps, step)
	}
	if len(steps) < 6 || steps[len(steps)-1].Step != "complete" || len(delivery.requests) != 1 || delivery.requests[0].Template != "compact" {
		t.Fatal("automation did not complete through audited delivery")
	}
	if string(vault.values["feishu-default"]) != "fake-CLI-private-secret" {
		t.Fatal("secret was not stored under the reference")
	}
	configPath := filepath.Join(home, "config", "notifiers.yaml")
	before, err := os.ReadFile(configPath)
	if err != nil || bytes.Contains(before, []byte("fake-CLI-private-secret")) {
		t.Fatal("configuration contains the secret")
	}
	checkpointPath := filepath.Join(home, "data", "feishu-setup-default.json")
	checkpointBefore, _ := os.ReadFile(checkpointPath)
	out, stderr, err = runFeishu(deps, home, "", "check", "--json")
	var report setup.Report
	if err != nil || stderr != "" || json.Unmarshal([]byte(out), &report) != nil || len(report.Steps) != 4 {
		t.Fatalf("check: %v %s %s", err, out, stderr)
	}
	after, _ := os.ReadFile(configPath)
	checkpointAfter, _ := os.ReadFile(checkpointPath)
	if !bytes.Equal(before, after) || !bytes.Equal(checkpointBefore, checkpointAfter) || len(delivery.requests) != 1 {
		t.Fatal("read-only check modified state or sent a message")
	}
	out, stderr, err = runFeishu(deps, home, "", "chats", "--json")
	var step setup.Step
	if err != nil || stderr != "" || json.Unmarshal([]byte(out), &step) != nil || len(step.Chats) != 1 || *step.Chats[0].MemberCount != 3 {
		t.Fatalf("chats: %v %s %s", err, out, stderr)
	}
	iterations := 0
	deps.Wait = func(context.Context, time.Duration) error {
		iterations++
		if iterations == 1 {
			client.chats = append(client.chats, feishu.Chat{ID: "oc_fake_second", Name: "另一个虚构群"})
			return nil
		}
		return context.Canceled
	}
	out, _, err = runFeishu(deps, home, "", "chats", "--watch", "--json")
	if err != nil || strings.Count(out, "oc_fake_group") != 1 || strings.Count(out, "oc_fake_second") != 1 {
		t.Fatalf("watch duplicates: %v %s", err, out)
	}
	out, _, err = runFeishu(deps, home, "", "test", "--notifier", "feishu-alerts", "--template", "detailed", "--json")
	if err != nil || !json.Valid([]byte(out)) || len(delivery.requests) != 2 || delivery.requests[1].Template != "detailed" || !strings.Contains(string(delivery.requests[1].Message.Card), "这是测试消息") {
		t.Fatalf("test: %v %s", err, out)
	}
}

func TestInteractiveSetupResumesAfterInterruption(t *testing.T) {
	deps, client, delivery, _ := cliFeishuFixture()
	home := t.TempDir()
	client.chats = nil
	out, stderr, err := runFeishu(deps, home, "cli_fake_cli\nfake-interactive-secret\nfeishu\n", "setup")
	if err == nil || !strings.Contains(err.Error(), "进度已保存") || strings.Contains(out+stderr, "fake-interactive-secret") {
		t.Fatalf("interrupt: %v %s %s", err, out, stderr)
	}
	botCalls, scopeCalls := client.botCalls, client.scopeCalls
	client.chats = []feishu.Chat{{ID: "oc_fake_group", Name: "虚构群"}}
	out, stderr, err = runFeishu(deps, home, "y\nstandard\ny\n", "setup")
	if err != nil || stderr != "" || !strings.Contains(out, "已完成：应用凭证") || !strings.Contains(out, "群里收到了吗") || !strings.Contains(out, "通知配置已保存") || len(delivery.requests) != 1 {
		t.Fatalf("resume: %v %s %s", err, out, stderr)
	}
	if client.botCalls != botCalls || client.scopeCalls != scopeCalls {
		t.Fatal("resume repeated completed remote checks")
	}
	out, _, err = runFeishu(deps, home, "", "setup", "--json")
	if err != nil || !json.Valid([]byte(out)) || len(delivery.requests) != 1 {
		t.Fatalf("completed resume sent again: %v %s", err, out)
	}
}

func TestPermissionsPollingOpensOnceAndExplainsPublishing(t *testing.T) {
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)
	command.SetErr(&stdout)
	command.SetContext(context.Background())
	now := time.Unix(1_000, 0)
	var delays []time.Duration
	var urls []string
	deps := feishuDependencies{
		Now: func() time.Time { return now },
		Wait: func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			now = now.Add(d)
			return nil
		},
		OpenURL: func(_ context.Context, url string) error { urls = append(urls, url); return nil },
	}
	start := now
	ui := setupUI{cmd: command, reader: bufio.NewReader(strings.NewReader("")), deps: deps, opened: make(map[string]bool)}
	_, err := ui.poll(func(context.Context) (setup.Step, error) {
		if now.Sub(start) >= 70*time.Second {
			return setup.Step{Step: "permissions", Status: setup.OK, Message: "权限已开通"}, nil
		}
		return setup.Step{Step: "permissions", Status: setup.ActionRequired, Message: "缺少权限", URL: "https://open.feishu.cn/page/scope-apply?fake"}, nil
	}, 5*time.Second)
	if err != nil || len(delays) != 14 || len(urls) != 1 || strings.Count(stdout.String(), setup.PublishMessage) != 1 {
		t.Fatalf("poll: %v delays=%v urls=%v %s", err, delays, urls, stdout.String())
	}
	for _, delay := range delays {
		if delay != 5*time.Second {
			t.Fatal("permissions must poll every five seconds")
		}
	}
}

func TestFeishuMissingConfigJSONAndUsage(t *testing.T) {
	deps, _, delivery, _ := cliFeishuFixture()
	home := filepath.Join(t.TempDir(), "missing")
	out, stderr, err := runFeishu(deps, home, "", "check", "--json")
	var report setup.Report
	if err == nil || stderr != "" || json.Unmarshal([]byte(out), &report) != nil || len(report.Steps) != 1 || report.Steps[0].Status != setup.ActionRequired {
		t.Fatalf("missing config: %v %s %s", err, out, stderr)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("read-only check created home")
	}
	for _, args := range [][]string{{"setup", "--app", "../escape"}, {"setup", "--secret-stdin", "--yes"}, {"test"}, {"chats", "extra"}} {
		_, _, err := runFeishu(deps, t.TempDir(), "", args...)
		var usage *usageError
		if !errors.As(err, &usage) {
			t.Fatalf("usage %v: %v", args, err)
		}
	}
	if !reflect.DeepEqual(delivery.requests, []notify.Request(nil)) {
		t.Fatal("validation sent a message")
	}
}
