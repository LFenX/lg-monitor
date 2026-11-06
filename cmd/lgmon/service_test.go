package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

type fakeServiceController struct {
	definition winsvc.Definition
	action     string
	err        error
}

func (c *fakeServiceController) Install(_ context.Context, definition winsvc.Definition) error {
	c.definition, c.action = definition, "install"
	return c.err
}
func (c *fakeServiceController) Uninstall(context.Context) error {
	c.action = "uninstall"
	return c.err
}
func (c *fakeServiceController) Start(context.Context) error { c.action = "start"; return c.err }
func (c *fakeServiceController) Stop(context.Context) error  { c.action = "stop"; return c.err }
func (c *fakeServiceController) Status(context.Context) (winsvc.Status, error) {
	return winsvc.Status{Installed: true, State: winsvc.Running, PID: 42}, c.err
}

func TestServiceCLIUsesAbsolutePathsReportsProxyAndJSON(t *testing.T) {
	controller := &fakeServiceController{}
	home := filepath.Join(t.TempDir(), "home with spaces")
	validated := false
	deps := serviceDependencies{Controller: func() (serviceController, error) { return controller, nil }, Executable: func() (string, error) { return filepath.Join(t.TempDir(), "lgmon.exe"), nil }, ProxyConfigured: func() bool { return true }, Validate: func(got string) error { validated = got == home; return nil }}
	command := newInstallCommand(deps)
	command.PersistentFlags().String("home", home, "")
	command.PersistentFlags().String("log-level", "debug", "")
	command.SetArgs([]string{"--role", "monitor", "--listen", "127.0.0.1:17443", "--json"})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var result serviceResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "ok" || result.Installed == nil || !*result.Installed || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "notifiers.yaml") {
		t.Fatalf("install JSON: %s %v", output.String(), err)
	}
	if !validated || controller.definition.Home != home || controller.definition.Role != "monitor" || controller.definition.LogLevel != "debug" || controller.definition.Listen != "127.0.0.1:17443" {
		t.Fatalf("lost service configuration: %+v", controller.definition)
	}
	for _, args := range [][]string{{"service", "start", "--json"}, {"service", "stop", "--json"}, {"service", "status", "--json"}, {"uninstall", "--json"}} {
		root := &cobra.Command{Use: "test"}
		root.AddCommand(newServiceCommand(deps), newUninstallCommand(deps))
		root.SetArgs(args)
		output.Reset()
		root.SetOut(&output)
		if err := root.Execute(); err != nil || !json.Valid(output.Bytes()) {
			t.Fatalf("%v: %v %s", args, err, output.String())
		}
	}
}

func TestServiceCLIFailureDoesNotInventInstalledState(t *testing.T) {
	controller := &fakeServiceController{err: errors.New("请用管理员身份运行")}
	deps := serviceDependencies{Controller: func() (serviceController, error) { return controller, nil }}
	command := newServiceCommand(deps)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs([]string{"start", "--json"})
	var reported *reportedError
	if err := command.Execute(); !errors.As(err, &reported) {
		t.Fatalf("service failure: %v", err)
	}
	var result serviceResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "error" || result.Installed != nil || result.State != "" {
		t.Fatalf("failure invented a state: %s %v", output.String(), err)
	}
}

func TestFeishuServiceFlagDelegatesWithoutLoadingApplicationCredential(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing-home")
	called := false
	deps := feishuDependencies{
		NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) {
			t.Fatal("CLI loaded application credentials")
			return nil, nil
		},
		ServiceTest: func(_ context.Context, gotHome, notifier, template, testID string) (setup.Step, error) {
			called = true
			if gotHome != home || notifier != "fake-channel" || template != "compact" || !strings.HasPrefix(testID, "test-") {
				t.Fatal("wrong service test request")
			}
			return setup.Step{Step: "test", Status: setup.OK, Message: "LocalSystem 测试成功", MessageID: "om_fake_service"}, nil
		},
	}
	command := newFeishuTestCommand(deps)
	command.PersistentFlags().String("home", home, "")
	command.SetArgs([]string{"--service", "--notifier", "fake-channel", "--template", "compact", "--json"})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil || !called || !json.Valid(output.Bytes()) || !strings.Contains(output.String(), "LocalSystem") {
		t.Fatalf("service test CLI: %v %s", err, output.String())
	}
}
