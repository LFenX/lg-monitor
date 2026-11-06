package setup

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
)

type fakeVault struct{ values map[string][]byte }

func (v *fakeVault) Set(_ context.Context, id string, value []byte) error {
	v.values[id] = slices.Clone(value)
	return nil
}
func (v *fakeVault) Get(_ context.Context, id string) ([]byte, error) {
	value, ok := v.values[id]
	if !ok {
		return nil, errors.New("missing fake secret")
	}
	return slices.Clone(value), nil
}

type fakeClient struct {
	tokenErr, botErr, scopeErr, chatErr, detailErr error
	bot                                            feishu.Bot
	scopes                                         []feishu.Scope
	chats                                          []feishu.Chat
	tokens, bots, permissions, lists, closed       int
}

func (c *fakeClient) TenantToken(context.Context) (string, error) {
	c.tokens++
	return "fake-token", c.tokenErr
}
func (c *fakeClient) BotInfo(context.Context) (feishu.Bot, error) {
	c.bots++
	return c.bot, c.botErr
}
func (c *fakeClient) AppScopes(context.Context) ([]feishu.Scope, error) {
	c.permissions++
	return c.scopes, c.scopeErr
}
func (c *fakeClient) ListChats(context.Context) ([]feishu.Chat, error) {
	c.lists++
	return slices.Clone(c.chats), c.chatErr
}
func (c *fakeClient) ChatInfo(_ context.Context, id string) (feishu.Chat, error) {
	count := 7
	return feishu.Chat{ID: id, Name: "虚构测试群", MemberCount: &count}, c.detailErr
}
func (c *fakeClient) SendCard(context.Context, feishu.CardMessage) (feishu.SendResult, error) {
	panic("setup must send through the injected audited delivery")
}
func (c *fakeClient) CreateChat(context.Context, feishu.NewChat) (feishu.Chat, error) {
	panic("setup must not create a real chat")
}
func (c *fakeClient) Close() error { c.closed++; return nil }

func grantRequired(c *fakeClient) {
	c.scopes = nil
	for _, permission := range RequiredPermissions() {
		if permission.Required {
			c.scopes = append(c.scopes, feishu.Scope{Name: permission.Name, Type: "tenant", Granted: true})
		}
	}
}

type fakeDelivery struct {
	requests []notify.Request
	err      error
}

func (d *fakeDelivery) Send(_ context.Context, req notify.Request) (feishu.SendResult, error) {
	d.requests = append(d.requests, req)
	if d.err != nil {
		return feishu.SendResult{}, d.err
	}
	return feishu.SendResult{MessageID: "om_fake_test_message"}, nil
}

func machineFixture(t *testing.T) (*Machine, Options, *fakeClient, *fakeDelivery, *fakeVault) {
	t.Helper()
	home := t.TempDir()
	client := &fakeClient{bot: feishu.Bot{Name: "虚构机器人", ActivateStatus: 2}}
	grantRequired(client)
	delivery := &fakeDelivery{}
	vault := &fakeVault{values: make(map[string][]byte)}
	renderer, err := cards.New(nil, cards.Options{})
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Checkpoints: FileCheckpoints{Path: filepath.Join(home, "data", "checkpoint.json")},
		Secrets:     vault, Configs: FileConfigs{Home: home}, Templates: renderer,
		NewClient:   func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil },
		NewDelivery: func(feishu.Client) (Delivery, error) { return delivery, nil }, NewID: func() string { return "fixed-test-id" },
	}
	m, err := New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m, opts, client, delivery, vault
}

var fakeApp = config.FeishuApp{ID: "default", AppID: "cli_fake_setup", SecretRef: "fake-secret-ref", Domain: "feishu", Proxy: "none"}

func requireStep(t *testing.T, step Step, err error, want string) {
	t.Helper()
	if err != nil || step.Status != want || step.Message == "" {
		t.Fatalf("step=%+v err=%v want=%s", step, err, want)
	}
}

func advanceCredentials(t *testing.T, m *Machine) {
	t.Helper()
	step, err := m.Credentials(context.Background(), fakeApp, []byte("fake-private-secret"))
	requireStep(t, step, err, OK)
}

func advanceBotAndPermissions(t *testing.T, m *Machine) {
	t.Helper()
	step, err := m.Bot(context.Background())
	requireStep(t, step, err, OK)
	step, err = m.Permissions(context.Background())
	requireStep(t, step, err, OK)
}

func TestCredentialsValidationDoesNotPersistFailures(t *testing.T) {
	m, _, client, _, vault := machineFixture(t)
	invalid := fakeApp
	invalid.AppID = "invalid"
	step, err := m.Credentials(context.Background(), invalid, []byte("fake-private-secret"))
	requireStep(t, step, err, Error)
	if client.tokens != 0 || len(vault.values) != 0 || m.State().CredentialsOK {
		t.Fatal("invalid credential shape reached the API or storage")
	}
	client.tokenErr = &feishu.Error{Kind: feishu.Credentials, Message: "测试凭证错误"}
	step, err = m.Credentials(context.Background(), fakeApp, []byte("fake-private-secret"))
	requireStep(t, step, err, Error)
	if len(vault.values) != 0 || m.State().CredentialsOK || client.closed == 0 {
		t.Fatal("failed credentials were persisted or leaked the client")
	}
	client.tokenErr = nil
	advanceCredentials(t, m)
	if string(vault.values[fakeApp.SecretRef]) != "fake-private-secret" {
		t.Fatal("validated secret was not saved")
	}
}

func TestBotPermissionsAndLateChatBranches(t *testing.T) {
	m, _, client, _, _ := machineFixture(t)
	advanceCredentials(t, m)
	client.bot.ActivateStatus = 0
	step, err := m.Bot(context.Background())
	requireStep(t, step, err, ActionRequired)
	if m.State().BotOK || !strings.Contains(step.Message, "应用能力") {
		t.Fatal("disabled bot advanced")
	}
	client.bot.ActivateStatus = 2
	step, err = m.Bot(context.Background())
	requireStep(t, step, err, OK)
	client.scopes = []feishu.Scope{{Name: "im:message:send_as_bot", Type: "user", Granted: true}}
	step, err = m.Permissions(context.Background())
	requireStep(t, step, err, ActionRequired)
	if m.State().PermissionsOK || step.URL != "https://open.feishu.cn/page/scope-apply?clientID=cli_fake_setup&scopes=im%3Amessage%3Asend_as_bot%2Cim%3Achat%3Aread" {
		t.Fatalf("wrong missing tenant scopes: %+v", step)
	}
	client.scopeErr = &feishu.Error{Kind: feishu.MissingScopes, Code: 99991672, Message: "缺少查询权限", Scopes: []string{"application:application:readonly"}}
	step, err = m.Permissions(context.Background())
	requireStep(t, step, err, ActionRequired)
	if !strings.Contains(step.URL, "application%3Aapplication%3Areadonly") || len(step.Permissions) != 4 {
		t.Fatal("API-reported missing scope was ignored")
	}
	client.scopeErr = nil
	grantRequired(client)
	step, err = m.Permissions(context.Background())
	requireStep(t, step, err, OK)
	step = m.Chats(context.Background())
	requireStep(t, step, nil, ActionRequired)
	if !strings.Contains(step.Message, "虚构机器人") || !strings.Contains(step.Message, "可用范围") {
		t.Fatal("chat instructions missing bot identity or availability scope")
	}
	client.chats = []feishu.Chat{{ID: "oc_fake_group", Name: "原名称"}}
	step = m.Chats(context.Background())
	requireStep(t, step, nil, OK)
	if step.Chats[0].MemberCount == nil || *step.Chats[0].MemberCount != 7 || client.chats[0].MemberCount != nil {
		t.Fatal("chat detail or snapshot ownership is wrong")
	}
	step, err = m.Select(context.Background(), "oc_not_joined", "standard")
	requireStep(t, step, err, ActionRequired)
	step, err = m.Select(context.Background(), "oc_fake_group", "missing")
	requireStep(t, step, err, Error)
	step, err = m.Select(context.Background(), "oc_fake_group", "detailed")
	requireStep(t, step, err, OK)
	copy := m.State()
	*copy.Chat.MemberCount = 999
	if *m.State().Chat.MemberCount != 7 {
		t.Fatal("state leaked mutable member count")
	}
}

func TestResumeAndAuditedTestFailure(t *testing.T) {
	m, opts, client, delivery, _ := machineFixture(t)
	advanceCredentials(t, m)
	advanceBotAndPermissions(t, m)
	client.chats = []feishu.Chat{{ID: "oc_fake_group", Name: "虚构测试群"}}
	step, err := m.Select(context.Background(), "oc_fake_group", "detailed")
	requireStep(t, step, err, OK)
	delivery.err = &feishu.Error{Kind: feishu.NotInChat, Message: "机器人不在群里"}
	step, err = m.SendTest(context.Background())
	requireStep(t, step, err, ActionRequired)
	if m.State().MessageID != "" || len(delivery.requests) != 1 || !strings.Contains(string(delivery.requests[0].Message.Card), "这是测试消息") {
		t.Fatal("failed send advanced or did not use the test card")
	}
	testID := m.State().TestID
	botCalls, scopeCalls := client.bots, client.permissions
	resumed, err := New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	step, err = resumed.Bot(context.Background())
	requireStep(t, step, err, OK)
	step, err = resumed.Permissions(context.Background())
	requireStep(t, step, err, OK)
	if client.bots != botCalls || client.permissions != scopeCalls || !strings.Contains(step.Message, "已完成") {
		t.Fatal("resume repeated completed checks")
	}
	delivery.err = nil
	step, err = resumed.SendTest(context.Background())
	requireStep(t, step, err, OK)
	if len(delivery.requests) != 2 || delivery.requests[1].Message.AlertID != testID || step.MessageID == "" {
		t.Fatal("resume lost the idempotent test identity")
	}
	step, err = resumed.SendTest(context.Background())
	requireStep(t, step, err, OK)
	if len(delivery.requests) != 2 {
		t.Fatal("already sent test was sent twice")
	}
	step, err = resumed.Confirm(context.Background(), false)
	requireStep(t, step, err, ActionRequired)
	if resumed.State().Complete {
		t.Fatal("unconfirmed receipt completed setup")
	}
	step, err = resumed.Confirm(context.Background(), true)
	requireStep(t, step, err, OK)
	cfg, err := opts.Configs.(FileConfigs).Read()
	if err != nil || len(cfg.FeishuApps) != 1 || len(cfg.Notifiers) != 1 || cfg.Notifiers[0].ChatID != "oc_fake_group" || cfg.Notifiers[0].Template != "detailed" {
		t.Fatalf("saved config mismatch: %+v %v", cfg, err)
	}
}

func TestReadOnlyChecksAndUnknownChatCount(t *testing.T) {
	_, opts, client, _, vault := machineFixture(t)
	client.chats = []feishu.Chat{{ID: "oc_fake_group", Name: "虚构群"}}
	client.detailErr = &feishu.Error{Kind: feishu.MissingScopes, Message: "详情权限未开通", URL: "https://example.com/fake-permission"}
	report := Inspect(context.Background(), fakeApp, client)
	if len(report.Steps) != 4 || report.Steps[3].Chats[0].MemberCount != nil || report.Steps[3].URL == "" || len(vault.values) != 0 {
		t.Fatalf("read-only report: %+v", report)
	}
	state, err := opts.Checkpoints.Load(context.Background())
	if err != nil || state.CredentialsOK {
		t.Fatal("read-only check persisted progress")
	}
	client.tokenErr = errors.New("fake-private-secret")
	report = Inspect(context.Background(), fakeApp, client)
	if len(report.Steps) != 1 || report.Steps[0].Status != Error || strings.Contains(report.Steps[0].Message, "fake-private-secret") {
		t.Fatal("read-only failure leaked upstream content")
	}
}
