package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type setupUI struct {
	cmd    *cobra.Command
	reader *bufio.Reader
	deps   feishuDependencies
	json   bool
	opened map[string]bool
}

func (u *setupUI) read(prompt, fallback string) (string, error) {
	if fallback != "" {
		prompt += " [" + fallback + "]"
	}
	if _, err := fmt.Fprint(u.cmd.OutOrStdout(), prompt+"："); err != nil {
		return "", fmt.Errorf("输出向导提示失败：%w", err)
	}
	line, err := u.reader.ReadString('\n')
	if err != nil && (err != io.EOF || len(line) == 0) {
		return "", fmt.Errorf("输入已结束，进度已保存，重新运行 feishu setup 可继续：%w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = fallback
	}
	return line, nil
}

func (u *setupUI) secret() ([]byte, error) {
	if _, err := fmt.Fprint(u.cmd.ErrOrStderr(), "App Secret（不回显）："); err != nil {
		return nil, fmt.Errorf("输出密钥输入提示失败：%w", err)
	}
	if input, ok := u.cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(input.Fd())) {
		value, err := readSecret(input)
		fmt.Fprintln(u.cmd.ErrOrStderr())
		return value, err
	}
	line, err := u.reader.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return nil, &secretInputError{cause: err}
	}
	value := []byte(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
	if len(value) == 0 || len(value) > 65536 {
		clear(value)
		return nil, fmt.Errorf("Secret 须为 1～64KiB")
	}
	return value, nil
}

func (u *setupUI) poll(step func(context.Context) (setup.Step, error), interval time.Duration) (setup.Step, error) {
	start := u.deps.Now()
	last, published := "", false
	for {
		result, err := step(u.cmd.Context())
		if err != nil {
			return result, err
		}
		key := result.Status + result.Message + result.URL
		if u.json || key != last || result.Status == setup.OK {
			if err := printFeishuStep(u.cmd, result, u.json); err != nil {
				return result, err
			}
		}
		last = key
		if result.Status == setup.OK {
			return result, nil
		}
		if result.Status == setup.Error || u.json {
			return result, &reportedError{fmt.Errorf("%s", result.Message)}
		}
		if result.Step == "permissions" {
			if result.URL != "" && !u.opened[result.URL] {
				u.opened[result.URL] = true
				if err := u.deps.OpenURL(u.cmd.Context(), result.URL); err != nil {
					if _, err := fmt.Fprintln(u.cmd.ErrOrStderr(), "未能自动打开浏览器，请手动打开上述链接"); err != nil {
						return result, fmt.Errorf("输出浏览器提示失败：%w", err)
					}
				}
			}
			if !published && u.deps.Now().Sub(start) >= time.Minute {
				published = true
				if _, err := fmt.Fprintln(u.cmd.OutOrStdout(), setup.PublishMessage); err != nil {
					return result, fmt.Errorf("输出发布提示失败：%w", err)
				}
			}
		}
		if err := u.deps.Wait(u.cmd.Context(), interval); err != nil {
			return result, fmt.Errorf("向导已中断，进度已保存：%w", err)
		}
	}
}

var setupAppID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func newFeishuSetupCommand(deps feishuDependencies, selectedApp *string) *cobra.Command {
	var appID, domain, chatID, templateID string
	var secretStdin, yes, jsonOutput bool
	command := &cobra.Command{Use: "setup", Short: "交互式配置飞书，支持中断后续跑", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			home, _ := cmd.Flags().GetString("home")
			id := *selectedApp
			if id == "" {
				id = "default"
			}
			if !setupAppID.MatchString(id) || len(id) > 100 {
				return &usageError{fmt.Errorf("--app 须为 1～100 个小写字母、数字或连字符")}
			}
			ui := &setupUI{cmd: cmd, reader: bufio.NewReader(cmd.InOrStdin()), deps: deps, json: jsonOutput, opened: make(map[string]bool)}
			vault, err := deps.Secrets(home, slog.Default())
			if err != nil {
				return err
			}
			renderer, err := feishuRenderer(home, deps.Now)
			if err != nil {
				return err
			}
			var closes []func() error
			defer func() {
				for i := len(closes) - 1; i >= 0; i-- {
					if err := closes[i](); err != nil && runErr == nil {
						runErr = fmt.Errorf("关闭向导资源失败：%w", err)
					}
				}
			}()
			machine, err := setup.New(cmd.Context(), setup.Options{
				Checkpoints: setup.FileCheckpoints{Path: filepath.Join(home, "data", "feishu-setup-"+id+".json")},
				Secrets:     vault, Configs: setup.FileConfigs{Home: home}, NewClient: deps.NewClient, Templates: renderer,
				NewDelivery: func(client feishu.Client) (setup.Delivery, error) {
					delivery, closeDelivery, err := deps.NewDelivery(cmd.Context(), home, client)
					if err == nil {
						closes = append(closes, closeDelivery)
					}
					return delivery, err
				},
			})
			if err != nil {
				return err
			}
			closes = append(closes, machine.Close)
			state := machine.State()
			if state.Complete && appID == "" && !secretStdin && chatID == "" && templateID == "" {
				return printFeishuStep(cmd, setup.Step{Step: "complete", Status: setup.OK, Message: "已完成：通知配置已保存；运行 feishu check 重新检查当前状态", MessageID: state.MessageID}, jsonOutput)
			}
			if !state.CredentialsOK || appID != "" || secretStdin {
				app := config.FeishuApp{ID: id, AppID: appID, SecretRef: "feishu-" + id, Domain: domain, Proxy: "auto"}
				var secret []byte
				if secretStdin {
					if app.AppID == "" {
						return &usageError{fmt.Errorf("--secret-stdin 需要同时提供 --app-id")}
					}
					secret, err = readSecret(cmd.InOrStdin())
				} else if yes || jsonOutput {
					step := setup.Step{Step: "credentials", Status: setup.ActionRequired, Message: "请使用 --app-id、--secret-stdin 提供凭证；" + setup.CreateAppMessage, URL: "https://open.feishu.cn/app"}
					if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
						return err
					}
					return &reportedError{fmt.Errorf("需要应用凭证")}
				} else {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), setup.CreateAppMessage); err != nil {
						return fmt.Errorf("输出应用创建步骤失败：%w", err)
					}
					if app.AppID == "" {
						app.AppID, err = ui.read("App ID", state.App.AppID)
						if err != nil {
							return err
						}
					}
					secret, err = ui.secret()
					if err == nil && !cmd.Flags().Changed("domain") {
						app.Domain, err = ui.read("平台 feishu/lark", domain)
					}
				}
				if err != nil {
					clear(secret)
					return err
				}
				step, err := machine.Credentials(cmd.Context(), app, secret)
				clear(secret)
				if err != nil {
					return err
				}
				if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
					return err
				}
				if step.Status != setup.OK {
					return &reportedError{fmt.Errorf("%s", step.Message)}
				}
			} else if err := printFeishuStep(cmd, setup.Step{Step: "credentials", Status: setup.OK, Message: "已完成：应用凭证已验证，密钥已加密保存"}, jsonOutput); err != nil {
				return err
			}
			if _, err := ui.poll(machine.Bot, 5*time.Second); err != nil {
				return err
			}
			if _, err := ui.poll(machine.Permissions, 5*time.Second); err != nil {
				return err
			}
			state = machine.State()
			if chatID == "" {
				chatID = state.Chat.ID
			}
			if templateID == "" {
				templateID = state.Template
			}
			if state.Chat.ID == "" || cmd.Flags().Changed("chat-id") || cmd.Flags().Changed("template") {
				chats, err := ui.poll(func(ctx context.Context) (setup.Step, error) { return machine.Chats(ctx), nil }, 3*time.Second)
				if err != nil {
					return err
				}
				if chatID == "" {
					if yes || jsonOutput {
						step := setup.Step{Step: "chat", Status: setup.ActionRequired, Message: "请用 --chat-id 选择并确认接收测试消息的群", Chats: chats.Chats}
						if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
							return err
						}
						return &reportedError{fmt.Errorf("尚未确认告警群")}
					}
					if len(chats.Chats) == 1 {
						answer, err := ui.read("检测到机器人已加入「"+chats.Chats[0].Name+"」，用这个群接收告警吗？y/N", "N")
						if err != nil {
							return err
						}
						if !strings.EqualFold(answer, "y") && answer != "是" {
							return fmt.Errorf("尚未选择群，进度已保存")
						}
						chatID = chats.Chats[0].ID
					} else {
						answer, err := ui.read("选择群编号", "")
						if err != nil {
							return err
						}
						index, err := strconv.Atoi(answer)
						if err != nil || index < 1 || index > len(chats.Chats) {
							return &usageError{fmt.Errorf("请选择列表中的群编号")}
						}
						chatID = chats.Chats[index-1].ID
					}
				}
				if templateID == "" && !yes && !jsonOutput {
					for _, entry := range renderer.List() {
						if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", entry.ID, entry.Name); err != nil {
							return fmt.Errorf("输出模板选项失败：%w", err)
						}
					}
					templateID, err = ui.read("选择模板 ID", "standard")
					if err != nil {
						return err
					}
				}
				if templateID == "" {
					templateID = "standard"
				}
				step, err := machine.Select(cmd.Context(), chatID, templateID)
				if err != nil {
					return err
				}
				if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
					return err
				}
				if step.Status != setup.OK {
					return &reportedError{fmt.Errorf("%s", step.Message)}
				}
			}
			if jsonOutput && !yes {
				step := setup.Step{Step: "test", Status: setup.ActionRequired, Message: "群和模板已保存；带 --yes 继续发送测试卡片"}
				if err := printFeishuStep(cmd, step, true); err != nil {
					return err
				}
				return &reportedError{fmt.Errorf("等待测试发送确认")}
			}
			step, err := machine.SendTest(cmd.Context())
			if err != nil {
				return err
			}
			if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
				return err
			}
			if step.Status != setup.OK {
				return &reportedError{fmt.Errorf("%s", step.Message)}
			}
			received := yes
			if !yes {
				answer, err := ui.read("群里收到了吗？y/N", "N")
				if err != nil {
					return err
				}
				received = strings.EqualFold(answer, "y") || answer == "是"
			}
			step, err = machine.Confirm(cmd.Context(), received)
			if err != nil {
				return err
			}
			if yes && step.Status == setup.OK {
				step.Message += "；--yes 已跳过收件询问，仍需到群内确认实际显示"
			}
			if err := printFeishuStep(cmd, step, jsonOutput); err != nil {
				return err
			}
			if step.Status != setup.OK {
				return &reportedError{fmt.Errorf("%s", step.Message)}
			}
			return nil
		}}
	command.Flags().StringVar(&appID, "app-id", "", "开发者后台的 App ID（cli_ 开头）")
	command.Flags().StringVar(&domain, "domain", "feishu", "平台：feishu 或 lark")
	command.Flags().BoolVar(&secretStdin, "secret-stdin", false, "从标准输入读取 App Secret（直到 EOF）")
	command.Flags().StringVar(&chatID, "chat-id", "", "明确选定的测试/告警群 ID")
	command.Flags().StringVar(&templateID, "template", "", "群默认模板（未指定时选 standard）")
	command.Flags().BoolVar(&yes, "yes", false, "确认向所选群发送测试，并跳过收件询问")
	command.Flags().BoolVar(&jsonOutput, "json", false, "逐行输出步骤 JSON；需人工处理时立即返回")
	return command
}
