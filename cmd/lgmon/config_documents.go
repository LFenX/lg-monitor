package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/spf13/cobra"
)

func configDocumentOperation(kind string, write bool) (api.Operation, error) {
	pair, ok := map[string][2]api.Operation{
		"targets":   {api.GetTargetsConfig, api.PutTargetsConfig},
		"rules":     {api.GetRulesConfig, api.PutRulesConfig},
		"notifiers": {api.GetNotifiersConfig, api.PutNotifiersConfig},
		"settings":  {api.GetSettingsConfig, api.PutSettingsConfig},
	}[kind]
	if !ok {
		return "", &usageError{fmt.Errorf("配置种类必须是 targets、rules、notifiers 或 settings")}
	}
	if write {
		return pair[1], nil
	}
	return pair[0], nil
}

func configResponse(cmd *cobra.Command, response api.MCPResponse) error {
	var value any
	if err := json.Unmarshal(response.Body, &value); err != nil {
		return fmt.Errorf("监控器返回了无效配置 JSON：%w", err)
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	if !commandJSON(cmd) {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("输出配置结果失败：%w", err)
	}
	if response.Status >= 400 {
		return &reportedError{fmt.Errorf("监控器拒绝配置请求（HTTP %d），详情见结果", response.Status)}
	}
	return nil
}

func configDeleteItem(data []byte, kind, id string) ([]byte, error) {
	if kind == "settings" {
		return nil, &usageError{fmt.Errorf("settings 不支持按 ID 删除，请先读取后使用 config put")}
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil || document == nil {
		return nil, fmt.Errorf("监控器返回了无效配置对象")
	}
	var items []json.RawMessage
	if len(document[kind]) > 0 {
		if err := json.Unmarshal(document[kind], &items); err != nil {
			return nil, fmt.Errorf("读取配置条目失败：%w", err)
		}
	}
	remaining := make([]json.RawMessage, 0, len(items))
	removed := false
	for _, item := range items {
		var entry struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &entry); err != nil {
			return nil, fmt.Errorf("读取配置条目 ID 失败：%w", err)
		}
		if entry.ID == id {
			removed = true
		} else {
			remaining = append(remaining, item)
		}
	}
	if !removed {
		return nil, fmt.Errorf("配置条目 %q 不存在", id)
	}
	document[kind], _ = json.Marshal(remaining)
	return json.Marshal(document)
}

func newConfigDocumentCommands(factory operationHTTPFactory) []*cobra.Command {
	commands := []*cobra.Command{}
	for _, action := range []string{"get", "put", "delete"} {
		var address, inputFile string
		minimum, maximum := 1, 1
		use := action + " <kind>"
		if action == "delete" {
			minimum, maximum = 2, 2
			use += " <id>"
		}
		cmd := &cobra.Command{Use: use, Short: map[string]string{
			"get":    "读取监控器完整配置，不含密钥明文",
			"put":    "校验并整体替换配置；先读取并保留无关条目",
			"delete": "按 ID 删除目标、规则或通知渠道；仍被引用时拒绝",
		}[action], Args: teamArgs(minimum, maximum), RunE: func(cmd *cobra.Command, args []string) error {
			readOperation, err := configDocumentOperation(args[0], false)
			if err != nil {
				return err
			}
			if action == "delete" && args[0] == "settings" {
				return &usageError{fmt.Errorf("settings 不支持按 ID 删除")}
			}
			var body []byte
			if action == "put" {
				if inputFile == "" {
					return &usageError{fmt.Errorf("config put 需要 --file，使用 - 从标准输入读取 JSON")}
				}
				reader := cmd.InOrStdin()
				if inputFile != "-" {
					file, err := os.Open(inputFile)
					if err != nil {
						return fmt.Errorf("打开配置输入失败：%w", err)
					}
					defer file.Close()
					reader = file
				}
				body, err = io.ReadAll(io.LimitReader(reader, api.MaxRequestBody+1))
				if err != nil {
					return fmt.Errorf("读取配置输入失败：%w", err)
				}
				if len(body) > api.MaxRequestBody {
					return fmt.Errorf("配置输入不能超过 1 MiB")
				}
				if !json.Valid(body) || !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
					return fmt.Errorf("配置输入必须是完整 JSON 对象")
				}
			}
			client, err := factory(address)
			if err != nil {
				return err
			}
			if action != "put" {
				response, err := client.Call(cmd.Context(), readOperation, api.Request{})
				if err != nil {
					return err
				}
				if action == "get" || response.Status >= 400 {
					return configResponse(cmd, response)
				}
				body, err = configDeleteItem(response.Body, args[0], args[1])
				if err != nil {
					return err
				}
			}
			writeOperation, _ := configDocumentOperation(args[0], true)
			response, err := client.Call(cmd.Context(), writeOperation, api.Request{Body: body, ContentType: "application/json"})
			if err != nil {
				return err
			}
			return configResponse(cmd, response)
		}}
		cmd.Flags().StringVar(&address, "url", "http://127.0.0.1:7444", "监控器 HTTP/HTTPS 地址；令牌仅从 LGMON_TOKEN 读取")
		cmd.Flags().Bool("json", false, "输出 JSON")
		if action == "put" {
			cmd.Flags().StringVar(&inputFile, "file", "", "完整 JSON 文件；- 表示标准输入，最大 1 MiB")
		}
		commands = append(commands, cmd)
	}
	return commands
}
