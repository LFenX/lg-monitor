//go:build windows && installerfixture

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

var installerFixtureRoot, installerFixtureTag string

type installerFixtureState struct {
	Definition winsvc.Definition `json:"definition"`
	Installed  bool              `json:"installed"`
	State      winsvc.State      `json:"state"`
	Registered int               `json:"registered"`
	Started    int               `json:"started"`
	Removed    int               `json:"removed"`
	Paired     int               `json:"paired"`
}

type installerFixtureDriver struct{ root, program string }

func (f installerFixtureDriver) state() (installerFixtureState, error) {
	data, err := os.ReadFile(filepath.Join(f.root, "scm.json"))
	if errors.Is(err, os.ErrNotExist) {
		return installerFixtureState{}, nil
	}
	var state installerFixtureState
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	return state, err
}

func (f installerFixtureDriver) save(state installerFixtureState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(f.root, ".scm-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	_, err = file.Write(data)
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(f.root, "scm.json"))
}

func (f installerFixtureDriver) fault() string {
	data, _ := os.ReadFile(filepath.Join(f.root, "fault.txt"))
	return strings.TrimSpace(string(data))
}

func (f installerFixtureDriver) binding(context.Context) (winsvc.Definition, bool, error) {
	state, err := f.state()
	if f.fault() == "binding" {
		state.Definition.Home = filepath.Join(f.root, "other-home")
	}
	return state.Definition, state.Installed, err
}

func (f installerFixtureDriver) Install(_ context.Context, definition winsvc.Definition) error {
	state, err := f.state()
	if err != nil {
		return err
	}
	if state.Installed {
		return fmt.Errorf("夹具服务已存在")
	}
	state.Definition, state.Installed, state.Registered = definition, true, state.Registered+1
	state.Definition.Executable, state.State = f.program, winsvc.Stopped
	if err := secrets.NativePathPolicy().ProtectTree(filepath.Dir(f.program), true); err != nil {
		return err
	}
	return f.save(state)
}

func (f installerFixtureDriver) Start(context.Context) error {
	state, err := f.state()
	if err != nil {
		return err
	}
	state.Started++
	if f.fault() == "start" {
		if err := f.save(state); err != nil {
			return err
		}
		return fmt.Errorf("注入的夹具启动失败")
	}
	state.State = winsvc.Running
	return f.save(state)
}

func (f installerFixtureDriver) Uninstall(context.Context) error {
	state, err := f.state()
	if err != nil {
		return err
	}
	if f.fault() == "remove" {
		return fmt.Errorf("注入的夹具移除失败")
	}
	state.Removed, state.Installed, state.State = state.Removed+1, false, winsvc.Stopped
	return f.save(state)
}

func (f installerFixtureDriver) Status(context.Context) (winsvc.Status, error) {
	state, err := f.state()
	return winsvc.Status{Installed: state.Installed, State: state.State}, err
}

func (f installerFixtureDriver) Paired(_ context.Context, home, monitor string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(home, "data", "setup", "fake-paired.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var value struct {
		Monitor string `json:"monitor"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return false, err
	}
	if value.Monitor != monitor {
		return false, fmt.Errorf("夹具身份属于另一个监控器")
	}
	return true, nil
}

func (f installerFixtureDriver) Join(_ context.Context, home, monitor, code, fingerprint string) error {
	if f.fault() == "pair" {
		return setupPrivateError{"注入的夹具配对失败，没有注册服务", errors.New(code)}
	}
	if code != "FAKE_INSTALLER_PAIR_CODE" || fingerprint != "0123456789abcdef" {
		return fmt.Errorf("夹具只接受虚构的固定配对输入")
	}
	state, err := f.state()
	if err != nil {
		return err
	}
	state.Paired++
	if err := f.save(state); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"monitor": monitor})
	return os.WriteFile(filepath.Join(home, "data", "setup", "fake-paired.json"), data, 0o600)
}

type fixtureSetupFiles struct {
	setupFileStore
	driver installerFixtureDriver
}

func fixtureGuardPath(path string) error {
	if installerFixtureRoot == "" || installerFixtureTag == "" {
		return fmt.Errorf("安装夹具未绑定临时目录")
	}
	if err := setupGuardPath(path); err != nil {
		return err
	}
	root, err := setupCanonical(installerFixtureRoot)
	if err != nil {
		return err
	}
	temporary, err := setupCanonical(os.TempDir())
	if err != nil || !setupContains(temporary, root) || strings.EqualFold(root, temporary) {
		return fmt.Errorf("安装夹具只能作用于独立系统临时目录")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	source, err = setupCanonical(source)
	if err != nil || !setupContains(temporary, source) {
		return fmt.Errorf("安装夹具载荷不在系统临时目录")
	}
	actual, err := setupCanonical(path)
	if err != nil {
		return err
	}
	if !setupContains(root, actual) && !setupContains(filepath.Dir(source), actual) {
		return fmt.Errorf("安装夹具拒绝作用于绑定目录之外")
	}
	return nil
}

func (f fixtureSetupFiles) SaveReceipt(ctx context.Context, home string, receipt winsvc.SetupReceipt) error {
	if f.driver.fault() == "receipt" {
		return fmt.Errorf("注入的夹具回执保存失败")
	}
	return f.setupFileStore.SaveReceipt(ctx, home, receipt)
}

func fixtureSetupDependencies() setupDependencies {
	paths := secrets.NativePathPolicy()
	deps := nativeSetupDependencies(setupGuardPath)
	deps.Administrator = func() (bool, error) { return true, nil }
	deps.Guard = fixtureGuardPath
	deps.Factory = func(context.Context) (*winsvc.Setup, error) {
		root, err := setupCanonical(installerFixtureRoot)
		if err != nil || installerFixtureTag == "" || installerFixtureRoot == "" {
			return nil, fmt.Errorf("安装夹具需要编译期绑定独立临时目录")
		}
		temporary, err := setupCanonical(os.TempDir())
		if err != nil || !setupContains(temporary, root) || strings.EqualFold(root, temporary) {
			return nil, fmt.Errorf("安装夹具只能作用于独立系统临时目录")
		}
		source, err := os.Executable()
		if err != nil {
			return nil, err
		}
		canonicalSource, err := setupCanonical(source)
		if err != nil || !setupContains(temporary, canonicalSource) {
			return nil, fmt.Errorf("安装夹具载荷只能在系统临时目录运行")
		}
		program := filepath.Join(root, "program", "lgmon.exe")
		driver := installerFixtureDriver{root: root, program: program}
		return winsvc.NewSetup(winsvc.SetupOptions{Service: driver, Files: fixtureSetupFiles{setupFileStore{Source: source, Paths: paths, Guard: fixtureGuardPath}, driver}, Pairer: driver, Binding: driver.binding, Lock: func(ctx context.Context) (func(), error) {
			return winsvc.LockSetupWithName(ctx, `Local\lg-monitor-setup-fixture-`+installerFixtureTag)
		}, Now: time.Now, Source: source, Program: program, Version: "installer-fixture"})
	}
	return deps
}

func platformSetupDependencies() setupDependencies { return fixtureSetupDependencies() }

func installerFixtureRootCommand() *cobra.Command {
	root := &cobra.Command{Use: "lgmon", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("home", filepath.Join(installerFixtureRoot, "home"), "只用于绑定临时目录的安装夹具")
	root.AddCommand(newSetupCommand(fixtureSetupDependencies()))
	root.AddCommand(&cobra.Command{Use: "version", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "lgmon installer fixture (not for installation)")
		return err
	}})
	return root
}
