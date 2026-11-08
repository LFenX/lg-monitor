package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/collector/match"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

type targetSources func() (collector.ProcessSource, collector.PortSource)

func nativeTargetSources() (collector.ProcessSource, collector.PortSource) {
	return collector.NewProcessSource(nil, slog.Default()), collector.NewPortSource(nil, slog.Default())
}

func newTargetsCommand(sources targetSources) *cobra.Command {
	command := &cobra.Command{Use: "targets", Short: "调试监控目标配置"}
	var jsonOutput bool
	test := &cobra.Command{Use: "test <id>", Short: "只采集一次，显示匹配结果和原因", Args: templateArgs(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := cmd.Flags().GetString("home")
			cfg, err := config.Load(os.DirFS(filepath.Join(home, "config")))
			if err != nil {
				return err
			}
			index := slices.IndexFunc(cfg.Targets, func(target config.Target) bool { return target.ID == args[0] })
			if index < 0 {
				return fmt.Errorf("监控目标 %s 不存在，请检查 targets.yaml", args[0])
			}
			matcher, err := match.New([]config.Target{cfg.Targets[index]})
			if err != nil {
				return err
			}
			processes, ports := sources()
			rows, err := processes.Snapshot(cmd.Context())
			if err != nil {
				return fmt.Errorf("单次进程采集失败：%w", err)
			}
			listening, err := ports.ListeningTCP(cmd.Context())
			if err != nil {
				return fmt.Errorf("单次端口采集失败：%w", err)
			}
			directories, _ := processes.(collector.WorkingDirectorySource)
			rows, err = matcher.ResolveDirectories(cmd.Context(), collector.ProcessSnapshot{Processes: rows, Ports: listening}, directories)
			if err != nil {
				return err
			}
			result := matcher.Match(rows, listening)[0]
			if jsonOutput {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return fmt.Errorf("输出目标诊断 JSON 失败：%w", err)
				}
				return nil
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s：%s\n原因：%s\n", result.TargetID, result.State, result.Reason); err != nil {
				return fmt.Errorf("输出目标诊断失败：%w", err)
			}
			for _, process := range result.Processes {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "PID %d\t%s\n", process.PID, process.Name); err != nil {
					return fmt.Errorf("输出匹配进程失败：%w", err)
				}
			}
			for _, port := range result.Ports {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "监听 %s:%d\tPID %d\n", port.LocalAddress, port.Port, port.PID); err != nil {
					return fmt.Errorf("输出匹配端口失败：%w", err)
				}
			}
			return nil
		}}
	test.Flags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出完整匹配结果")
	command.AddCommand(test)
	return command
}
