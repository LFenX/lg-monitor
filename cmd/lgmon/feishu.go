package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type feishuDependencies struct {
	Secrets     secretFactory
	NewClient   setup.ClientFactory
	NewDelivery func(context.Context, string, feishu.Client) (setup.Delivery, func() error, error)
	OpenURL     func(context.Context, string) error
	Wait        func(context.Context, time.Duration) error
	Now         func() time.Time
}

type feishuCommandError struct {
	message string
	cause   error
}

func (e *feishuCommandError) Error() string { return e.message }
func (e *feishuCommandError) Unwrap() error { return e.cause }

func defaultFeishuDependencies() feishuDependencies {
	return feishuDependencies{
		Secrets: secrets.New, Now: time.Now, OpenURL: openBrowser, Wait: waitFeishu,
		NewClient: func(app config.FeishuApp, secret []byte) (feishu.Client, error) {
			return feishu.NewClient(app, secret, feishu.Options{})
		},
		NewDelivery: func(ctx context.Context, home string, client feishu.Client) (setup.Delivery, func() error, error) {
			db, err := store.Open(ctx, store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
			if err != nil {
				return nil, nil, err
			}
			sender, err := notify.NewSender(notify.Options{Client: client, Recorder: db})
			if err != nil {
				db.Close()
				return nil, nil, err
			}
			return sender, db.Close, nil
		},
	}
}

func waitFeishu(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type templateFS struct{ fs.FS }

func (f templateFS) Open(name string) (fs.File, error) {
	if name == "card-templates" || strings.HasPrefix(name, "card-templates/") {
		return f.FS.Open(name)
	}
	return nil, fs.ErrNotExist
}

func feishuRenderer(home string, now func() time.Time) (*cards.Renderer, error) {
	manager, err := config.NewManager(config.FSLoader{FS: templateFS{os.DirFS(filepath.Join(home, "config"))}}, slog.Default())
	if err != nil {
		return nil, err
	}
	return cards.New(manager, cards.Options{Now: now})
}

func printFeishuStep(cmd *cobra.Command, step setup.Step, jsonOutput bool) error {
	if jsonOutput {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(step); err != nil {
			return fmt.Errorf("输出飞书步骤失败：%w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s：%s\n", step.Status, step.Step, step.Message); err != nil {
		return fmt.Errorf("输出飞书步骤失败：%w", err)
	}
	if step.URL != "" {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), step.URL); err != nil {
			return fmt.Errorf("输出开通链接失败：%w", err)
		}
	}
	for _, p := range step.Permissions {
		status, required := "未开通", "必需"
		if p.Granted {
			status = "已开通"
		}
		if !p.Required {
			required = "可选"
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [%s/%s] %s\n", p.Name, required, status, p.Description); err != nil {
			return fmt.Errorf("输出权限状态失败：%w", err)
		}
	}
	for i, chat := range step.Chats {
		count := "人数未知"
		if chat.MemberCount != nil {
			count = fmt.Sprintf("%d 人", *chat.MemberCount)
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%d. %s（%s）\t%s\n", i+1, chat.Name, count, chat.ID); err != nil {
			return fmt.Errorf("输出群列表失败：%w", err)
		}
	}
	if step.MessageID != "" {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "消息 ID：%s\n", step.MessageID); err != nil {
			return fmt.Errorf("输出消息 ID 失败：%w", err)
		}
	}
	return nil
}

func reportFeishuFailure(cmd *cobra.Command, message string, jsonOutput bool) error {
	step := setup.Step{Step: "credentials", Status: setup.ActionRequired, Message: message}
	if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
		return err
	}
	return &reportedError{fmt.Errorf("%s", message)}
}

func selectFeishuApp(cfg config.NotifiersFile, id string) (config.FeishuApp, error) {
	if id == "" && len(cfg.FeishuApps) == 1 {
		return cfg.FeishuApps[0], nil
	}
	for _, app := range cfg.FeishuApps {
		if app.ID == id {
			return app, nil
		}
	}
	if len(cfg.FeishuApps) == 0 {
		return config.FeishuApp{}, fmt.Errorf("尚未配置飞书应用，请先运行 lgmon feishu setup")
	}
	return config.FeishuApp{}, fmt.Errorf("请选择已有应用：使用 --app <配置中的应用 ID>")
}

func readFeishuClient(ctx context.Context, deps feishuDependencies, home, id string) (config.FeishuApp, feishu.Client, error) {
	cfg, err := (setup.FileConfigs{Home: home}).Read()
	if err != nil {
		return config.FeishuApp{}, nil, err
	}
	app, err := selectFeishuApp(cfg, id)
	if err != nil {
		return app, nil, err
	}
	vault, err := deps.Secrets(home, slog.Default())
	if err != nil {
		return app, nil, err
	}
	secret, err := vault.Get(ctx, app.SecretRef)
	defer clear(secret)
	if err != nil {
		return app, nil, &feishuCommandError{message: "读取应用密钥失败，请运行 secret set 重新保存密钥", cause: err}
	}
	client, err := deps.NewClient(app, secret)
	if err != nil {
		return app, nil, &feishuCommandError{message: "创建飞书客户端失败，请检查应用配置", cause: err}
	}
	return app, client, nil
}

func newFeishuCommand(deps feishuDependencies) *cobra.Command {
	command := &cobra.Command{Use: "feishu", Short: "开通、检查或测试飞书告警"}
	var appID string
	command.PersistentFlags().StringVar(&appID, "app", "", "配置中的应用 ID（多个应用时指定）")
	command.AddCommand(newFeishuSetupCommand(deps, &appID))
	var checkJSON bool
	check := &cobra.Command{Use: "check", Short: "只读检查凭证、机器人、权限和群", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := cmd.Flags().GetString("home")
			app, client, err := readFeishuClient(cmd.Context(), deps, home, appID)
			if err != nil {
				if checkJSON {
					report := setup.Report{Steps: []setup.Step{{Step: "credentials", Status: setup.ActionRequired, Message: err.Error()}}}
					if writeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(report); writeErr != nil {
						return fmt.Errorf("输出飞书检查结果失败：%w", writeErr)
					}
					return &reportedError{err}
				}
				return reportFeishuFailure(cmd, err.Error(), checkJSON)
			}
			defer client.Close()
			report := setup.Inspect(cmd.Context(), app, client)
			if checkJSON {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(report); err != nil {
					return fmt.Errorf("输出飞书检查结果失败：%w", err)
				}
			} else {
				for _, step := range report.Steps {
					if err := printFeishuStep(cmd, step, false); err != nil {
						return err
					}
				}
			}
			if slices.ContainsFunc(report.Steps, func(step setup.Step) bool { return step.Status != setup.OK }) {
				return &reportedError{fmt.Errorf("飞书检查需要处理上述步骤")}
			}
			return nil
		}}
	check.Flags().BoolVar(&checkJSON, "json", false, "以 JSON 输出每一步状态")
	command.AddCommand(check)
	var chatsJSON, watch bool
	chats := &cobra.Command{Use: "chats", Short: "列出机器人所在的群", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := cmd.Flags().GetString("home")
			_, client, err := readFeishuClient(cmd.Context(), deps, home, appID)
			if err != nil {
				return reportFeishuFailure(cmd, err.Error(), chatsJSON)
			}
			defer client.Close()
			seen := make(map[string]bool)
			initial := true
			lastNotice := ""
			for {
				step := setup.QueryChats(cmd.Context(), client, "应用机器人")
				if watch && !initial {
					step.Chats = slices.DeleteFunc(step.Chats, func(chat feishu.Chat) bool { return seen[chat.ID] })
				}
				if initial || len(step.Chats) > 0 || (step.Status != setup.OK && step.Message != lastNotice) {
					if err := printFeishuStep(cmd, step, chatsJSON); err != nil {
						return err
					}
				}
				lastNotice = step.Message
				for _, chat := range step.Chats {
					seen[chat.ID] = true
				}
				if !watch || step.Status == setup.Error {
					if step.Status != setup.OK {
						return &reportedError{fmt.Errorf("%s", step.Message)}
					}
					return nil
				}
				initial = false
				if err := deps.Wait(cmd.Context(), 3*time.Second); err != nil {
					return nil
				}
			}
		}}
	chats.Flags().BoolVar(&chatsJSON, "json", false, "以 JSON 输出群和步骤状态（watch 时逐行输出）")
	chats.Flags().BoolVar(&watch, "watch", false, "每三秒检查并输出新加入的群")
	command.AddCommand(chats)
	command.AddCommand(newFeishuTestCommand(deps))
	return command
}

func newFeishuTestCommand(deps feishuDependencies) *cobra.Command {
	var notifierID, templateID string
	var jsonOutput bool
	command := &cobra.Command{Use: "test", Short: "向指定通知渠道发送测试卡片", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			if notifierID == "" {
				return &usageError{fmt.Errorf("请用 --notifier 指定已确认的通知渠道")}
			}
			home, _ := cmd.Flags().GetString("home")
			cfg, err := (setup.FileConfigs{Home: home}).Read()
			if err != nil {
				return err
			}
			index := slices.IndexFunc(cfg.Notifiers, func(n config.Notifier) bool { return n.ID == notifierID })
			if index < 0 {
				return fmt.Errorf("通知渠道 %s 不存在，请先运行 feishu setup", notifierID)
			}
			notifier := cfg.Notifiers[index]
			_, client, err := readFeishuClient(cmd.Context(), deps, home, notifier.App)
			if err != nil {
				return reportFeishuFailure(cmd, err.Error(), jsonOutput)
			}
			defer client.Close()
			renderer, err := feishuRenderer(home, deps.Now)
			if err != nil {
				return err
			}
			delivery, closeDelivery, err := deps.NewDelivery(cmd.Context(), home, client)
			if err != nil {
				return err
			}
			defer func() {
				if err := closeDelivery(); err != nil && runErr == nil {
					runErr = fmt.Errorf("关闭通知记录存储失败：%w", err)
				}
			}()
			selected := templateID
			if selected == "" {
				selected = notifier.Template
			}
			id := "test-" + rand.Text()
			card := renderer.RenderTest(selected, id)
			sent, err := delivery.Send(cmd.Context(), notify.Request{Message: feishu.CardMessage{AlertID: id, State: "firing", ChatID: notifier.ChatID, Card: card.JSON}, NotifierID: notifier.ID, Template: card.Template})
			step := setup.Step{Step: "test", Status: setup.OK, Message: "测试卡片已发送；请在群内确认实际收到", MessageID: sent.MessageID}
			if err != nil {
				step.Status, step.Message = setup.Error, notify.UserMessage(err)
				var apiError *feishu.Error
				if errors.As(err, &apiError) {
					step.Message, step.URL = apiError.Message, apiError.URL
				}
			}
			if card.Warning != "" {
				step.Message += "；" + card.Warning
			}
			if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
				return err
			}
			if step.Status != setup.OK {
				return &reportedError{fmt.Errorf("%s", step.Message)}
			}
			return nil
		}}
	command.Flags().StringVar(&notifierID, "notifier", "", "已确认的通知渠道 ID")
	command.Flags().StringVar(&templateID, "template", "", "覆盖渠道的默认模板")
	command.Flags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出发送状态和消息 ID")
	return command
}
