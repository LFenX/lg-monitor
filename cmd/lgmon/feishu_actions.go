package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

func newFeishuActionsCommand(administrator func() (bool, error)) *cobra.Command {
	if administrator == nil {
		administrator = winsvc.IsAdministrator
	}
	root := &cobra.Command{Use: "actions", Short: "授权或撤销通知群原消息收件人的确认与一小时告警组静音权限"}
	root.PersistentFlags().Bool("json", false, "输出 JSON 配置状态；实时连接状态在网页通知页查看")
	for _, operation := range []string{"enable", "disable", "status"} {
		root.AddCommand(&cobra.Command{Use: operation + " <渠道ID>", Short: map[string]string{"enable": "管理员授权卡片收件人操作该告警组", "disable": "管理员撤销卡片动作授权", "status": "只读查看磁盘授权配置"}[operation], Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := cmd.Flags().GetString("home")
			manager, err := config.NewManager(config.FSLoader{FS: os.DirFS(filepath.Join(home, "config"))}, slog.Default())
			if err != nil {
				return err
			}
			if operation != "status" {
				admin, err := administrator()
				if err != nil {
					return fmt.Errorf("检查卡片动作管理员权限失败：%w", err)
				}
				if !admin {
					return fmt.Errorf("修改卡片动作授权需要以管理员身份运行终端")
				}
				editor, err := config.NewEditor(config.DirectoryDocuments{Path: filepath.Join(home, "config")}, manager)
				if err != nil {
					return err
				}
				if _, err := editor.Update(cmd.Context(), "notifiers.yaml", func(data []byte) ([]byte, error) {
					return config.CardActionsConfig(data, args[0], operation == "enable")
				}); err != nil {
					return err
				}
			}
			for _, channel := range manager.Snapshot().Notifiers {
				if channel.ID == args[0] {
					if channel.Type != "feishu_app" {
						return fmt.Errorf("只有飞书应用渠道支持卡片动作")
					}
					state := "disabled"
					if channel.CardActions {
						state = "waiting"
					}
					result := api.CardActionsStatus{Notifier: channel.ID, App: channel.App, Enabled: channel.CardActions, State: state}
					asJSON, _ := cmd.Flags().GetBool("json")
					if asJSON {
						if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
							return fmt.Errorf("输出卡片动作配置失败：%w", err)
						}
						return nil
					}
					_, err := fmt.Fprintf(cmd.OutOrStdout(), "渠道 %s：卡片动作授权 enabled=%t。监控器加载后生效；实时连接状态在网页通知页查看。\n", channel.ID, channel.CardActions)
					if err != nil {
						return fmt.Errorf("输出卡片动作配置失败：%w", err)
					}
					return nil
				}
			}
			return fmt.Errorf("通知渠道不存在")
		}})
	}
	return root
}
