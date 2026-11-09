package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func writeCommandHelp(output *cobra.Command, target *cobra.Command) error {
	children := []string{}
	for _, child := range target.Commands() {
		if child.IsAvailableCommand() {
			children = append(children, child.Name())
		}
	}
	return json.NewEncoder(output.OutOrStdout()).Encode(struct {
		Command  string   `json:"command"`
		Use      string   `json:"use"`
		Summary  string   `json:"summary"`
		Usage    string   `json:"usage"`
		Commands []string `json:"commands"`
	}{target.CommandPath(), target.Use, target.Short, target.UsageString(), children})
}

func installJSONHelp(root *cobra.Command) {
	plainHelp := root.HelpFunc()
	root.SetHelpFunc(func(command *cobra.Command, args []string) {
		if commandJSON(command) {
			if err := writeCommandHelp(command, command); err != nil {
				fmt.Fprintln(command.ErrOrStderr(), "输出帮助 JSON 失败：", err)
			}
			return
		}
		plainHelp(command, args)
	})
	help := &cobra.Command{Use: "help [command]", Short: "查看命令帮助", RunE: func(cmd *cobra.Command, args []string) error {
		target, remaining, err := root.Find(args)
		if err != nil || len(remaining) != 0 {
			return &usageError{fmt.Errorf("帮助命令路径无效")}
		}
		if commandJSON(cmd) {
			return writeCommandHelp(cmd, target)
		}
		plainHelp(target, args)
		return nil
	}}
	help.Flags().Bool("json", false, "输出命令说明、用法和子命令 JSON")
	root.SetHelpCommand(help)
	root.InitDefaultHelpCmd()
}
