package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type teamCall func(context.Context, string, api.Operation, map[string]string, any) (json.RawMessage, error)

func callTeam(ctx context.Context, home string, operation api.Operation, params map[string]string, body any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	client, closeClient, err := defaultPairDependencies().Admin(ctx, home)
	if err != nil {
		return nil, err
	}
	defer closeClient()
	var encoded []byte
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("编码团队参数失败：%w", err)
		}
	}
	stream, err := client.TeamAdmin(ctx, &lgmonv1.TeamAdminRequest{Operation: string(operation), Params: params, BodyJson: encoded})
	if err != nil {
		return nil, fmt.Errorf("连接团队管理失败：%w", err)
	}
	data := []byte{}
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if status.Code(err) == codes.Unimplemented {
				return nil, fmt.Errorf("监控器尚未支持团队管理，请升级并重启自己的监控器后重试")
			}
			return nil, fmt.Errorf("团队管理请求失败：%w", err)
		}
		if len(data)+len(chunk.BodyJson) > transport.TeamResponseLimit {
			return nil, fmt.Errorf("团队结果超过 16 MiB 上限")
		}
		data = append(data, chunk.BodyJson...)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("团队管理返回了无效 JSON")
	}
	return data, nil
}

func teamArgs(minimum, maximum int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < minimum || len(args) > maximum {
			return &usageError{fmt.Errorf("参数数量不正确，请查看 %s --help", cmd.CommandPath())}
		}
		return nil
	}
}

func teamOutput(cmd *cobra.Command, call teamCall, op api.Operation, params map[string]string, input any) error {
	home, _ := cmd.Flags().GetString("home")
	asJSON, _ := cmd.Flags().GetBool("json")
	data, err := call(cmd.Context(), home, op, params, input)
	if err != nil {
		if !asJSON {
			return err
		}
		data, _ = json.Marshal(map[string]any{"error": map[string]string{"code": "team_failed", "message": err.Error()}})
	}
	var envelope struct {
		Error *struct{ Code, Message string } `json:"error"`
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if failure := json.Unmarshal(data, &envelope); failure != nil && err == nil {
			return fmt.Errorf("读取团队结果失败：%w", failure)
		}
	} else if !json.Valid(data) && err == nil {
		return fmt.Errorf("团队管理返回了无效 JSON")
	}
	if err == nil && envelope.Error != nil {
		err = fmt.Errorf("%s", envelope.Error.Message)
	}
	if asJSON {
		if _, failure := fmt.Fprintln(cmd.OutOrStdout(), string(data)); failure != nil {
			return fmt.Errorf("输出团队 JSON 失败：%w", failure)
		}
		if err != nil {
			return &reportedError{err}
		}
		return nil
	}
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("读取团队结果失败：%w", err)
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码团队结果失败：%w", err)
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(pretty)); err != nil {
		return fmt.Errorf("输出团队结果失败：%w", err)
	}
	return nil
}

func newTeamCommand(call teamCall) *cobra.Command {
	root := &cobra.Command{Use: "team", Short: "管理本机团队与成员设备身份，真实安装和防火墙仍由本人操作"}
	root.PersistentFlags().Bool("json", false, "输出 JSON")
	var owner string
	create := &cobra.Command{Use: "create <名称>", Short: "建立团队并启用成员 HTTPS 入口", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.CreateTeam, nil, api.TeamCreateInput{Name: args[0], OwnerName: owner})
	}}
	create.Flags().StringVar(&owner, "owner", "负责人", "分享中显示的负责人名称")
	root.AddCommand(create)
	root.AddCommand(&cobra.Command{Use: "list", Short: "列出本机自建团队", Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, _ []string) error { return teamOutput(cmd, call, api.ListTeams, nil, nil) }})
	var name, ttl string
	invite := &cobra.Command{Use: "invite <团队ID>", Short: "生成一次性邀请，仅本次返回完整文字", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(name) == "" {
			return &usageError{fmt.Errorf("请用 --name 指定被邀请设备的显示名")}
		}
		return teamOutput(cmd, call, api.InviteTeamMember, map[string]string{"id": args[0]}, map[string]string{"name": name, "ttl": ttl})
	}}
	invite.Flags().StringVar(&name, "name", "", "被邀请设备的显示名")
	invite.Flags().StringVar(&ttl, "ttl", "24h", "邀请有效期，1m～7d")
	root.AddCommand(invite)
	root.AddCommand(&cobra.Command{Use: "remove <团队ID> <成员ID>", Short: "吊销成员设备身份并立即断开连接", Args: teamArgs(2, 2), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.RemoveTeamMember, map[string]string{"id": args[0], "member": args[1]}, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "members <团队ID>", Short: "列出成员和访问记录", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.ListTeamMembers, map[string]string{"id": args[0]}, nil)
	}})
	var fromStdin, preview bool
	var fingerprint string
	join := &cobra.Command{Use: "join --invite-stdin --fingerprint <已核对指纹>", Short: "从标准输入读取邀请并加入；--preview 只检查公开 CA", Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, _ []string) error {
		if !fromStdin {
			return &usageError{fmt.Errorf("请用 --invite-stdin 从标准输入读取完整邀请文字")}
		}
		if !preview && fingerprint == "" {
			return &usageError{fmt.Errorf("请先核对 CA 指纹，再用 --fingerprint 指定 16 位或完整指纹")}
		}
		data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 16*1024+1))
		if err != nil {
			return fmt.Errorf("读取邀请文字失败：%w", err)
		}
		if len(data) > 16*1024 {
			return &usageError{fmt.Errorf("邀请文字超过 16 KiB 上限")}
		}
		op := api.JoinTeam
		if preview {
			op = api.PreviewTeamJoin
		}
		return teamOutput(cmd, call, op, nil, api.TeamJoinInput{Invitation: strings.TrimSpace(string(data)), Fingerprint: fingerprint})
	}}
	join.Flags().BoolVar(&fromStdin, "invite-stdin", false, "从标准输入读取完整邀请，不放进命令参数")
	join.Flags().BoolVar(&preview, "preview", false, "只预览地址和公开 CA 指纹，不消耗邀请")
	join.Flags().StringVar(&fingerprint, "fingerprint", "", "通过可信渠道核对的 CA 指纹")
	root.AddCommand(join)
	root.AddCommand(&cobra.Command{Use: "joined", Short: "列出本机已加入的团队，不输出证书或密钥引用", Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, _ []string) error {
		return teamOutput(cmd, call, api.ListJoinedTeams, nil, nil)
	}})
	var address string
	change := &cobra.Command{Use: "address <本机加入ID> --address <HTTPS地址>", Short: "修改负责人地址，保留原 CA 和设备身份", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		if address == "" {
			return &usageError{fmt.Errorf("请指定 --address HTTPS 基础地址")}
		}
		return teamOutput(cmd, call, api.ChangeTeamAddress, map[string]string{"id": args[0]}, api.TeamAddressInput{Address: address})
	}}
	change.Flags().StringVar(&address, "address", "", "负责人新的 HTTPS 基础地址")
	root.AddCommand(change)
	root.AddCommand(&cobra.Command{Use: "leave <本机加入ID>", Short: "移除本机加入记录；负责人处的吊销需另外移除成员", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.LeaveTeam, map[string]string{"id": args[0]}, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "shared <本机加入ID>", Short: "实时列出分享给本设备的面板", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.ListJoinedShares, map[string]string{"id": args[0]}, nil)
	}})
	var collector, period, from, to string
	view := &cobra.Command{Use: "view <本机加入ID> <分享ID> [卡片ID]", Short: "读取共享定义或卡片结果，不接受 SQL", Args: teamArgs(2, 3), RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]string{"id": args[0], "share": args[1]}
		op := api.GetJoinedShare
		if len(args) == 3 {
			op = api.GetJoinedPanel
			params["panel"] = args[2]
			for key, value := range map[string]string{"collector": collector, "range": period, "from": from, "to": to} {
				if value != "" {
					params[key] = value
				}
			}
		} else if collector != "" || period != "" || from != "" || to != "" {
			return &usageError{fmt.Errorf("时间和主机参数只用于读取具体卡片")}
		}
		return teamOutput(cmd, call, op, params, nil)
	}}
	view.Flags().StringVar(&collector, "collector", "", "分享范围内的采集器 ID")
	view.Flags().StringVar(&period, "range", "", "时间范围，例如 1h、7d")
	view.Flags().StringVar(&from, "from", "", "窗口起点，UTC Unix 毫秒")
	view.Flags().StringVar(&to, "to", "", "窗口终点，UTC Unix 毫秒")
	root.AddCommand(view)
	return root
}

type shareFlags struct {
	dashboard                                        string
	panels, collectors, targets, members             []string
	allPanels, allCollectors, allTargets, allMembers bool
}

func (f *shareFlags) bind(cmd *cobra.Command, update bool) {
	cmd.Flags().StringVar(&f.dashboard, "dashboard", "", "声明式面板 ID")
	cmd.Flags().StringSliceVar(&f.panels, "panels", nil, "所选卡片 ID，逗号分隔；创建时省略表示整页")
	cmd.Flags().StringSliceVar(&f.collectors, "collectors", nil, "所选采集器 ID；创建时省略表示不限制")
	cmd.Flags().StringSliceVar(&f.targets, "targets", nil, "所选目标 ID；创建时省略表示不限制")
	cmd.Flags().StringSliceVar(&f.members, "members", nil, "所选成员 ID；创建时省略表示全团队")
	if update {
		cmd.Flags().BoolVar(&f.allPanels, "all-panels", false, "明确改为整页面板")
		cmd.Flags().BoolVar(&f.allCollectors, "all-collectors", false, "明确解除主机限制")
		cmd.Flags().BoolVar(&f.allTargets, "all-targets", false, "明确解除目标限制")
		cmd.Flags().BoolVar(&f.allMembers, "all-members", false, "明确分享给全团队")
		for _, pair := range [][2]string{{"panels", "all-panels"}, {"collectors", "all-collectors"}, {"targets", "all-targets"}, {"members", "all-members"}} {
			cmd.MarkFlagsMutuallyExclusive(pair[0], pair[1])
		}
	}
}
func (f *shareFlags) apply(cmd *cobra.Command, input *api.ShareInput) error {
	if cmd.Flags().Changed("dashboard") {
		input.DashboardID = f.dashboard
	}
	for _, field := range []struct {
		name   string
		values []string
		all    bool
		out    *[]string
	}{{"panels", f.panels, f.allPanels, &input.Panels}, {"collectors", f.collectors, f.allCollectors, &input.Collectors}, {"targets", f.targets, f.allTargets, &input.Targets}, {"members", f.members, f.allMembers, &input.Members}} {
		if field.all {
			*field.out = nil
			continue
		}
		if cmd.Flags().Changed(field.name) {
			if len(field.values) == 0 || slices.Contains(field.values, "") {
				return &usageError{fmt.Errorf("--%s 不能是空列表，更新时解除限制请使用对应的 --all-*", field.name)}
			}
			*field.out = field.values
		}
	}
	return nil
}
func newShareCommand(call teamCall) *cobra.Command {
	root := &cobra.Command{Use: "share", Short: "负责人管理声明式面板的分享和数据范围"}
	root.PersistentFlags().Bool("json", false, "输出 JSON")
	createFlags := &shareFlags{}
	create := &cobra.Command{Use: "create <团队ID> --dashboard <面板ID>", Short: "建立分享，省略筛选表示整页/全团队/全部数据", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		input := api.ShareInput{TeamID: args[0]}
		if err := createFlags.apply(cmd, &input); err != nil {
			return err
		}
		if input.DashboardID == "" {
			return &usageError{fmt.Errorf("请用 --dashboard 指定声明式面板 ID")}
		}
		return teamOutput(cmd, call, api.CreateShare, nil, input)
	}}
	createFlags.bind(create, false)
	root.AddCommand(create)
	root.AddCommand(&cobra.Command{Use: "list [团队ID]", Short: "列出负责人已有分享及范围", Args: teamArgs(0, 1), RunE: func(cmd *cobra.Command, args []string) error {
		var params map[string]string
		if len(args) == 1 {
			params = map[string]string{"team": args[0]}
		}
		return teamOutput(cmd, call, api.ListShares, params, nil)
	}})
	updateFlags := &shareFlags{}
	update := &cobra.Command{Use: "update <分享ID>", Short: "保留未指定字段，使用当前 revision 更新分享", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := cmd.Flags().GetString("home")
		data, err := call(cmd.Context(), home, api.ListShares, nil, nil)
		if err != nil {
			return err
		}
		var values []store.TeamShare
		if err := json.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("读取当前分享失败：%w", err)
		}
		index := slices.IndexFunc(values, func(v store.TeamShare) bool { return v.ID == args[0] && v.RevokedAt == 0 })
		if index < 0 {
			return fmt.Errorf("分享不存在或已撤销")
		}
		v := values[index]
		input := api.ShareInput{TeamID: v.TeamID, DashboardID: v.DashboardID, Panels: v.Panels, Collectors: v.Collectors, Targets: v.Targets, Members: v.Members, Revision: v.Revision}
		if err := updateFlags.apply(cmd, &input); err != nil {
			return err
		}
		return teamOutput(cmd, call, api.UpdateShare, map[string]string{"id": v.ID}, input)
	}}
	updateFlags.bind(update, true)
	root.AddCommand(update)
	root.AddCommand(&cobra.Command{Use: "revoke <分享ID>", Short: "撤销分享并取消在途读取", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.RevokeShare, map[string]string{"id": args[0]}, nil)
	}})
	return root
}
