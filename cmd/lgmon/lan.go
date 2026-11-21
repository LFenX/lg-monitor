// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/spf13/cobra"
)

// newLANCommand handles LAN sharing from the terminal through the same local management channel
// as team commands. Turning visibility on or off is a settings.yaml change (lan.visible).
func newLANCommand(call teamCall) *cobra.Command {
	root := &cobra.Command{Use: "lan", Short: "内网共享：查看状态、找附近的电脑、处理查看申请；开关写在 settings.yaml 的 lan.visible"}
	root.PersistentFlags().Bool("json", false, "输出 JSON")
	root.AddCommand(&cobra.Command{Use: "status", Short: "内网可见开关、本机地址与指纹、待处理申请数", Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, _ []string) error {
		return teamOutput(cmd, call, api.GetLAN, nil, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "discover", Short: "找局域网里打开了内网可见的 lgmon，列出它们公开的面板", Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, _ []string) error {
		return teamOutput(cmd, call, api.DiscoverLAN, nil, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "requests", Short: "收到的和发出的申请，包括配对码", Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, _ []string) error {
		return teamOutput(cmd, call, api.ListLANRequest, nil, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "approve <申请ID>", Short: "和对方核对配对码一致后，同意对方只读查看该面板", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.ApproveLAN, map[string]string{"id": args[0]}, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "deny <申请ID>", Short: "拒绝申请", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.DenyLAN, map[string]string{"id": args[0]}, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "grants", Short: "谁能看我的哪个面板", Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, _ []string) error {
		return teamOutput(cmd, call, api.ListLANGrants, nil, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "revoke <授权ID>", Short: "撤销授权，立即生效", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return teamOutput(cmd, call, api.RevokeLANGrant, map[string]string{"id": args[0]}, nil)
	}})
	return root
}
