package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

type trayAutostart interface{ Set(string, bool) error }
type trayDependencies struct {
	Tokens     tokenDependencies
	Profile    func() (app.TrayProfile, error)
	Username   func() (string, error)
	Address    func(string) (string, error)
	Executable func() (string, error)
	Autostart  trayAutostart
	Run        func(context.Context, app.TrayBackend) error
}

func defaultTrayDependencies() trayDependencies {
	return trayDependencies{
		Tokens: defaultTokenDependencies(),
		Profile: func() (app.TrayProfile, error) {
			dir := os.Getenv("LOCALAPPDATA")
			if dir == "" {
				return app.TrayProfile{}, fmt.Errorf("无法定位当前账户的 LOCALAPPDATA")
			}
			return app.TrayProfile{Directory: filepath.Join(dir, "lg-monitor"), Protector: app.NewTrayProtector()}, nil
		},
		Username: func() (string, error) {
			u, err := user.Current()
			if err != nil {
				return "", fmt.Errorf("读取托盘账户失败：%w", err)
			}
			return u.Username, nil
		},
		Address: func(home string) (string, error) {
			s, err := config.LoadSettings(os.DirFS(filepath.Join(home, "config")))
			if err != nil {
				return "", err
			}
			return app.TrayMonitorURL(s.HTTPAddress())
		},
		Executable: os.Executable, Autostart: app.NativeTrayAutostart{}, Run: runNativeTray,
	}
}

func runNativeTray(ctx context.Context, backend app.TrayBackend) error {
	driver, err := app.NewNativeTrayDriver()
	if err != nil {
		return err
	}
	return app.RunTray(ctx, app.TrayOptions{Backend: backend, Driver: driver, Opener: app.NativeTrayOpener{}})
}

func newTrayCommand(deps trayDependencies) *cobra.Command {
	root := &cobra.Command{Use: "tray", Short: "登录用户托盘，通过 HTTP 读取监控器，不控制服务", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		profile, err := deps.Profile()
		if err != nil {
			return err
		}
		return deps.Run(cmd.Context(), app.NewRemoteTray(profile, nil))
	}}
	var remove, autostart bool
	setup := &cobra.Command{Use: "setup", Short: "以当前账户的管理员身份设置托盘配置令牌", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := requireTokenAdmin(deps.Tokens); err != nil {
			return err
		}
		profile, err := deps.Profile()
		if err != nil {
			return err
		}
		username, err := deps.Username()
		if err != nil {
			return err
		}
		if i := strings.LastIndexAny(username, `\/`); i >= 0 {
			username = username[i+1:]
		}
		name := "tray-" + username
		home, _ := cmd.Flags().GetString("home")
		home, err = filepath.Abs(home)
		if err != nil {
			return fmt.Errorf("解析托盘配置根目录失败：%w", err)
		}
		old, err := profile.Registration()
		if err != nil {
			return err
		}
		if old.Home != "" && !strings.EqualFold(old.Home, home) {
			return fmt.Errorf("托盘已绑定其他 home，请先使用原 home 运行 tray setup --remove")
		}
		db, err := deps.Tokens.Open(cmd.Context(), home)
		if err != nil {
			return err
		}
		defer db.Close()
		if old.TokenID != "" {
			rows, err := db.ListAPITokens(cmd.Context())
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.ID == old.TokenID {
					if row.Name != name || row.Scope != "config" {
						return fmt.Errorf("托盘令牌登记信息不匹配，未修改令牌")
					}
					if err := db.RevokeAPIToken(cmd.Context(), row.ID); err != nil {
						return err
					}
				}
			}
		}
		if remove {
			if autostart {
				exe, err := deps.Executable()
				if err != nil {
					return fmt.Errorf("读取程序路径失败：%w", err)
				}
				if err := deps.Autostart.Set(exe, false); err != nil {
					return err
				}
			}
			if err := profile.Remove(); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "已吊销托盘令牌并删除当前账户的托盘文件")
			return err
		}
		address, err := deps.Address(home)
		if err != nil {
			return err
		}
		token, err := api.NewToken(api.ScopeConfig, name, deps.Tokens.Entropy)
		if err != nil {
			return err
		}
		row, err := db.AddAPIToken(cmd.Context(), token.Digest[:], token.Name, string(token.Scope))
		if err != nil {
			return err
		}
		saved := false
		defer func() {
			if !saved {
				_ = db.RevokeAPIToken(context.WithoutCancel(cmd.Context()), row.ID)
			}
		}()
		value := []byte(token.Value)
		defer clear(value)
		if err := profile.Save(app.TrayRegistration{URL: address, TokenID: row.ID, Name: name, Home: home}, value); err != nil {
			return err
		}
		if autostart {
			exe, err := deps.Executable()
			if err != nil {
				return fmt.Errorf("读取程序路径失败：%w", err)
			}
			if err := deps.Autostart.Set(exe, true); err != nil {
				return errors.Join(err, profile.Remove())
			}
		}
		saved = true
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "已设置当前 Windows 账户的托盘；请用同一账户运行 lgmon tray")
		return err
	}}
	setup.Flags().BoolVar(&remove, "remove", false, "吊销令牌并删除当前账户的托盘文件")
	setup.Flags().BoolVar(&autostart, "autostart", false, "登记登录启动；与 --remove 一起使用时删除本程序的启动项")
	root.AddCommand(setup)
	return root
}
