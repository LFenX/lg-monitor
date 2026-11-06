package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/config"
	"gopkg.in/yaml.v3"
)

type FileCheckpoints struct{ Path string }

func (f FileCheckpoints) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: 1}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("读取向导进度文件失败：%w", err)
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, &privateError{message: "向导进度文件格式无效，请恢复备份或移走后重试", cause: err}
	}
	if state.Version != 1 || decoder.Decode(new(any)) != io.EOF {
		return State{}, fmt.Errorf("向导进度文件版本或内容无效，请恢复备份")
	}
	return state, nil
}

func (f FileCheckpoints) Save(ctx context.Context, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("编码向导进度失败：%w", err)
	}
	return atomicWrite(ctx, f.Path, append(data, '\n'))
}

type FileConfigs struct{ Home string }

func (f FileConfigs) Read() (config.NotifiersFile, error) {
	data, err := os.ReadFile(filepath.Join(f.Home, "config", "notifiers.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return config.NotifiersFile{}, nil
	}
	if err != nil {
		return config.NotifiersFile{}, fmt.Errorf("读取飞书配置失败：%w", err)
	}
	return decodeConfig(data)
}

func decodeConfig(data []byte) (config.NotifiersFile, error) {
	var cfg config.NotifiersFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && err != io.EOF {
		return cfg, &privateError{message: "notifiers.yaml 格式无效，请先修复配置", cause: err}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return cfg, fmt.Errorf("notifiers.yaml 只能包含一个 YAML 文档")
	}
	return cfg, nil
}

func (f FileConfigs) SaveNotifier(ctx context.Context, app config.FeishuApp, notifier config.Notifier) error {
	path := filepath.Join(f.Home, "config", "notifiers.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取原通知配置失败：%w", err)
	}
	output, err := updatedConfig(data, app, &notifier)
	if err != nil {
		return err
	}
	return atomicWrite(ctx, path, output)
}

func ApplicationConfig(data []byte, app config.FeishuApp) ([]byte, error) {
	return updatedConfig(data, app, nil)
}

func updatedConfig(data []byte, app config.FeishuApp, notifier *config.Notifier) ([]byte, error) {
	if _, err := decodeConfig(data); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, &privateError{message: "解析原通知配置失败", cause: err}
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("notifiers.yaml 根节点必须是对象")
	}
	if err := upsert(root, "feishuApps", app.ID, app); err != nil {
		return nil, err
	}
	if notifier != nil {
		if err := upsert(root, "notifiers", notifier.ID, *notifier); err != nil {
			return nil, err
		}
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, fmt.Errorf("编码通知配置失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("完成通知配置编码失败：%w", err)
	}
	if _, err := decodeConfig(output.Bytes()); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func upsert(root *yaml.Node, key, id string, value any) error {
	var sequence *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			sequence = root.Content[i+1]
			break
		}
	}
	if sequence == nil {
		sequence = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, sequence)
	}
	if sequence.Kind == yaml.AliasNode {
		sequence = sequence.Alias
	}
	if sequence.Kind != yaml.SequenceNode {
		return fmt.Errorf("notifiers.yaml 的 %s 必须是列表", key)
	}
	var encoded yaml.Node
	if err := encoded.Encode(value); err != nil {
		return fmt.Errorf("编码飞书配置项失败：%w", err)
	}
	for i, item := range sequence.Content {
		if item.Kind == yaml.AliasNode {
			item = item.Alias
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			if item.Content[j].Value == "id" && item.Content[j+1].Value == id {
				encoded.HeadComment, encoded.LineComment, encoded.FootComment = item.HeadComment, item.LineComment, item.FootComment
				sequence.Content[i] = &encoded
				return nil
			}
		}
	}
	sequence.Content = append(sequence.Content, &encoded)
	return nil
}

func atomicWrite(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建飞书配置目录失败：%w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".feishu-*")
	if err != nil {
		return fmt.Errorf("创建临时配置文件失败：%w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("写入临时配置文件失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("刷新临时配置文件失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭临时配置文件失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replaceFile(name, path); err != nil {
		return fmt.Errorf("发布飞书配置文件失败：%w", err)
	}
	return nil
}
