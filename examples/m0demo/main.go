package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"gopkg.in/yaml.v3"
)

const targetID = "m0-own-demo"

type secretReader interface {
	Get(context.Context, string) ([]byte, error)
}

type secretWriter interface {
	Set(context.Context, string, []byte) error
}

type selection struct {
	TargetID string `json:"targetId"`
	Notifier string `json:"notifier"`
	ChatName string `json:"chatName"`
}

type prepareOptions struct {
	SourceHome string
	Home       string
	Notifier   string
	Demo       string
	Nonce      string
	Port       int
}

func selected(home, notifierID string) (config.FeishuApp, config.Notifier, error) {
	cfg, err := (setup.FileConfigs{Home: home}).Read()
	if err != nil {
		return config.FeishuApp{}, config.Notifier{}, err
	}
	for _, notifier := range cfg.Notifiers {
		if notifier.ID != notifierID || notifier.Type != "feishu_app" {
			continue
		}
		for _, app := range cfg.FeishuApps {
			if app.ID == notifier.App && app.SecretRef != "" && app.AppID != "" && notifier.ChatID != "" {
				return app, notifier, nil
			}
		}
	}
	return config.FeishuApp{}, config.Notifier{}, fmt.Errorf("未找到完整的飞书通知渠道，请先完成 feishu setup 并指定 --notifier")
}

func prepare(ctx context.Context, opts prepareOptions, source secretReader, destination secretWriter) (selection, error) {
	if opts.Port < 1 || opts.Port > 65535 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(opts.Nonce) || opts.Demo == "" || opts.Home == "" {
		return selection{}, fmt.Errorf("演示配置需要空闲端口、演示程序、32 位随机标记和新的临时目录")
	}
	app, notifier, err := selected(opts.SourceHome, opts.Notifier)
	if err != nil {
		return selection{}, err
	}
	notifier.Template = "detailed"
	target := config.Target{ID: targetID, Name: "M0 自建演示 HTTP 服务", Match: config.Match{Process: filepath.Base(opts.Demo), Cmdline: `serve\s+--nonce\s+` + opts.Nonce + `(?:\s|$)`, Port: &opts.Port}}
	target.Probes = []config.Probe{{Type: "http", URL: fmt.Sprintf("http://127.0.0.1:%d/", opts.Port), Interval: "2s", Timeout: "1s", ExpectStatus: "200"}}
	rule := config.Rule{ID: "m0-demo-down", Name: "M0 演示进程停止", Type: "process_absent", Target: targetID, For: "5s", Severity: "critical", Notify: []string{notifier.ID}, Template: "detailed"}
	files := map[string]any{
		"targets.yaml":   config.TargetsFile{Targets: []config.Target{target}},
		"rules.yaml":     config.RulesFile{Rules: []config.Rule{rule}},
		"notifiers.yaml": config.NotifiersFile{FeishuApps: []config.FeishuApp{app}, Notifiers: []config.Notifier{notifier}},
	}
	// The destination must be new, so a typo cannot overwrite an existing installation.
	if err := os.Mkdir(opts.Home, 0o700); err != nil {
		return selection{}, fmt.Errorf("创建独立验收目录失败（目录必须不存在）：%w", err)
	}
	if err := os.Mkdir(filepath.Join(opts.Home, "config"), 0o700); err != nil {
		return selection{}, fmt.Errorf("创建验收配置目录失败：%w", err)
	}
	for name, value := range files {
		data, err := yaml.Marshal(value)
		if err != nil {
			return selection{}, fmt.Errorf("编码验收配置失败：%w", err)
		}
		if err := os.WriteFile(filepath.Join(opts.Home, "config", name), data, 0o600); err != nil {
			return selection{}, fmt.Errorf("写入验收配置失败：%w", err)
		}
	}
	if _, err := config.Load(os.DirFS(filepath.Join(opts.Home, "config"))); err != nil {
		return selection{}, fmt.Errorf("校验验收配置失败：%w", err)
	}
	plain, err := source.Get(ctx, app.SecretRef)
	if err != nil {
		return selection{}, err
	}
	defer clear(plain)
	// Re-encrypt through the native vault to secure the destination before publishing ciphertext.
	if err := destination.Set(ctx, app.SecretRef, plain); err != nil {
		return selection{}, err
	}
	return selection{TargetID: targetID, Notifier: notifier.ID, ChatName: notifier.ChatName}, nil
}

func execute(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("需要 serve、inspect、prepare 或 audit 子命令")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	home := flags.String("home", "", "")
	notifierID := flags.String("notifier", "", "")
	sourceHome := flags.String("source-home", "", "")
	demo := flags.String("demo", "", "")
	nonce := flags.String("nonce", "", "")
	port := flags.Int("port", 0, "")
	alertID := flags.String("alert-id", "", "")
	state := flags.String("state", "", "")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("验收参数无效：%w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("验收命令不接受位置参数")
	}
	var result any
	switch args[0] {
	case "serve":
		if *port < 1 || *port > 65535 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(*nonce) {
			return fmt.Errorf("演示服务需要合法端口和随机标记")
		}
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
		if err != nil {
			return fmt.Errorf("启动演示 HTTP 服务失败：%w", err)
		}
		defer listener.Close()
		server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "lgmon owned demo\n") })}
		return server.Serve(listener)
	case "inspect":
		_, notifier, err := selected(*home, *notifierID)
		if err != nil {
			return err
		}
		result = selection{TargetID: targetID, Notifier: notifier.ID, ChatName: notifier.ChatName}
	case "prepare":
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		source, err := secrets.New(*sourceHome, logger)
		if err != nil {
			return err
		}
		destination, err := secrets.New(*home, logger)
		if err != nil {
			return err
		}
		result, err = prepare(ctx, prepareOptions{SourceHome: *sourceHome, Home: *home, Notifier: *notifierID, Demo: *demo, Nonce: *nonce, Port: *port}, source, destination)
		if err != nil {
			return err
		}
	case "audit":
		view, err := store.OpenView(ctx, filepath.Join(*home, "data", "lgmon.db"))
		if err != nil {
			return err
		}
		defer view.Close()
		result, err = view.Delivery(ctx, *alertID, *state, *notifierID)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("未知验收子命令")
	}
	return json.NewEncoder(output).Encode(result)
}

func main() {
	if err := execute(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
