package winsvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type SetupRequest struct {
	Home, Role, Listen, Monitor string
	Code, Fingerprint           string
	Confirmed                   bool
}

type SetupPlan struct {
	Role            string `json:"role"`
	Home            string `json:"home"`
	Program         string `json:"program"`
	Listen          string `json:"listen"`
	Monitor         string `json:"monitor"`
	ExistingConfig  bool   `json:"existingConfig"`
	PairingRequired bool   `json:"pairingRequired"`
	Message         string `json:"message"`
}

type SetupReceipt struct {
	Version     string `json:"version"`
	Role        string `json:"role"`
	Home        string `json:"home"`
	Program     string `json:"program"`
	Listen      string `json:"listen"`
	Monitor     string `json:"monitor"`
	InstalledAt int64  `json:"installedAt"`
}

type SetupResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	SetupPlan
	Service Status `json:"service"`
}

type SetupFiles interface {
	Check(context.Context, string, string) (bool, error)
	Initialize(context.Context, string) error
	SaveReceipt(context.Context, string, SetupReceipt) error
}

type SetupPairer interface {
	Paired(context.Context, string, string) (bool, error)
	Join(context.Context, string, string, string, string) error
}

type SetupService interface {
	Install(context.Context, Definition) error
	Start(context.Context) error
	Uninstall(context.Context) error
	Status(context.Context) (Status, error)
}

type SetupOptions struct {
	Service SetupService
	Files   SetupFiles
	Pairer  SetupPairer
	Binding func(context.Context) (Definition, bool, error)
	Lock    func(context.Context) (func(), error)
	Now     func() time.Time
	Source  string
	Program string
	Version string
}

type Setup struct{ options SetupOptions }

func (s *Setup) VerifyAbsent(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, found, err := s.options.Binding(ctx)
	if err != nil {
		return fmt.Errorf("只读核对安装服务是否消失失败：%w", err)
	}
	if found {
		return fmt.Errorf("仍检测到 lgmon 服务，拒绝复制覆盖或删除程序文件")
	}
	return nil
}

func NewSetup(options SetupOptions) (*Setup, error) {
	if options.Service == nil || options.Files == nil || options.Pairer == nil || options.Binding == nil || options.Lock == nil || options.Now == nil || !filepath.IsAbs(options.Source) || !filepath.IsAbs(options.Program) {
		return nil, fmt.Errorf("安装编排需要服务、文件、配对、绑定、时钟和绝对程序路径")
	}
	return &Setup{options}, nil
}

func validateSetupHome(home, program string) error {
	clean := filepath.Clean(home)
	volume := filepath.VolumeName(clean)
	if !filepath.IsAbs(clean) || clean == volume+string(filepath.Separator) || strings.HasPrefix(clean, `\\`) || strings.ContainsFunc(clean, unicode.IsControl) {
		return fmt.Errorf("安装数据目录必须是本机绝对路径，不能为磁盘根目录或网络共享")
	}
	programDir := filepath.Dir(program)
	for _, pair := range [][2]string{{clean, programDir}, {programDir, clean}} {
		if !strings.EqualFold(filepath.VolumeName(pair[0]), filepath.VolumeName(pair[1])) {
			continue
		}
		rel, err := filepath.Rel(strings.ToLower(pair[0]), strings.ToLower(pair[1]))
		if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return fmt.Errorf("程序目录和数据目录不能相同或互相包含")
		}
	}
	return nil
}

func setupAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	n, e := strconv.Atoi(port)
	return err == nil && e == nil && host != "" && n >= 1 && n <= 65535 && !strings.ContainsAny(host, `/\\@?# "'`) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validateSetupRequest(request SetupRequest, program string) error {
	if err := validateSetupHome(request.Home, program); err != nil {
		return err
	}
	if request.Role != "both" && request.Role != "collector" && request.Role != "monitor" {
		return fmt.Errorf("安装角色必须为 both、collector 或 monitor")
	}
	if request.Role == "collector" {
		if request.Listen != "" || !setupAddress(request.Monitor) {
			return fmt.Errorf("采集器安装需要有效的监控器 host:port，不能设置本机监听地址")
		}
	} else if request.Monitor != "" || !setupAddress(request.Listen) || request.Code != "" || request.Fingerprint != "" {
		return fmt.Errorf("monitor/both 需要有效监听 host:port，不能指定远程配对参数")
	}
	return nil
}

func (s *Setup) Check(ctx context.Context, request SetupRequest) (SetupPlan, error) {
	plan := SetupPlan{Role: request.Role, Home: filepath.Clean(request.Home), Program: s.options.Program, Listen: request.Listen, Monitor: request.Monitor}
	if err := ctx.Err(); err != nil {
		return plan, fmt.Errorf("安装检查已取消：%w", err)
	}
	if err := validateSetupRequest(request, s.options.Program); err != nil {
		return plan, err
	}
	_, installed, err := s.options.Binding(ctx)
	if err != nil {
		return plan, fmt.Errorf("查询现有 lgmon 服务绑定失败：%w", err)
	}
	if installed {
		return plan, fmt.Errorf("lgmon 服务已存在，没有覆盖；请备份 home，使用原卸载方式卸载后再安装，数据会保留")
	}
	plan.ExistingConfig, err = s.options.Files.Check(ctx, request.Home, s.options.Program)
	if err != nil {
		return plan, err
	}
	if request.Role == "collector" {
		paired, err := s.options.Pairer.Paired(ctx, request.Home, request.Monitor)
		if err != nil {
			return plan, err
		}
		plan.PairingRequired = !paired
	}
	plan.Message = "安装检查通过；确认后将初始化缺失配置、注册并启动 LocalSystem 自动启动服务，保留已有数据"
	return plan, nil
}

func (s *Setup) Install(ctx context.Context, request SetupRequest) (SetupResult, error) {
	result := SetupResult{Status: "error", Message: "安装没有完成"}
	if !request.Confirmed {
		return result, fmt.Errorf("安装需要 --confirm 明确同意注册并启动 LocalSystem 自动启动服务")
	}
	unlock, err := s.options.Lock(ctx)
	if err != nil {
		return result, err
	}
	defer unlock()
	plan, err := s.Check(ctx, request)
	result.SetupPlan = plan
	if err != nil {
		return result, err
	}
	if plan.PairingRequired {
		fingerprint := strings.ToLower(strings.TrimSpace(request.Fingerprint))
		if request.Code == "" || (len(fingerprint) != 16 && len(fingerprint) != 64) || strings.Trim(fingerprint, "0123456789abcdef") != "" {
			return result, fmt.Errorf("首次安装采集器需要一次性配对码和本人已核对的 16/64 位 CA 指纹，请通过受保护输入或 stdin 提供")
		}
	} else if request.Role == "collector" && (request.Code != "" || request.Fingerprint != "") {
		return result, fmt.Errorf("采集器已有该监控器的有效身份，请留空配对码和指纹以复用，不覆盖旧身份")
	}
	if err := s.options.Files.Initialize(ctx, request.Home); err != nil {
		return result, err
	}
	if plan.PairingRequired {
		if err := s.options.Pairer.Join(ctx, request.Home, request.Monitor, request.Code, request.Fingerprint); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("注册服务前安装已取消：%w", err)
	}
	definition := Definition{Executable: s.options.Source, Home: request.Home, Role: request.Role, Listen: request.Listen, Monitor: request.Monitor, LogLevel: "info"}
	if err := s.options.Service.Install(ctx, definition); err != nil {
		return result, fmt.Errorf("注册本次 lgmon 服务失败：%w", err)
	}
	rollback := func(cause error) (SetupResult, error) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
		defer cancel()
		removed, err := s.remove(cleanup, request.Home)
		if err != nil {
			return result, fmt.Errorf("本次安装失败且服务回滚未完成，请保留文件并人工检查 service status：%w", errors.Join(cause, err))
		}
		result.Service = removed.Service
		return result, fmt.Errorf("本次服务已回滚，配置和配对身份保留供重试：%w", cause)
	}
	if err := s.options.Service.Start(ctx); err != nil {
		return rollback(fmt.Errorf("启动本次服务失败：%w", err))
	}
	result.Service, err = s.options.Service.Status(ctx)
	if err != nil || !result.Service.Installed || result.Service.State != Running {
		if err == nil {
			err = fmt.Errorf("未能确认服务处于 Running")
		}
		return rollback(err)
	}
	receipt := SetupReceipt{Version: s.options.Version, Role: request.Role, Home: filepath.Clean(request.Home), Program: s.options.Program, Listen: request.Listen, Monitor: request.Monitor, InstalledAt: s.options.Now().UnixMilli()}
	if err := s.options.Files.SaveReceipt(ctx, request.Home, receipt); err != nil {
		return rollback(err)
	}
	result.Status, result.Message = "ok", "lgmon 已安装并确认服务正在运行；卸载保留配置、数据和配对身份"
	return result, nil
}

func sameSetupPath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

func (s *Setup) Remove(ctx context.Context, home string, confirmed bool) (SetupResult, error) {
	if !confirmed {
		return SetupResult{Status: "error"}, fmt.Errorf("卸载需要 --confirm 明确同意停止并移除本安装的 lgmon 服务")
	}
	unlock, err := s.options.Lock(ctx)
	if err != nil {
		return SetupResult{Status: "error"}, err
	}
	defer unlock()
	return s.remove(ctx, home)
}

func (s *Setup) remove(ctx context.Context, home string) (SetupResult, error) {
	result := SetupResult{Status: "error", SetupPlan: SetupPlan{Home: filepath.Clean(home), Program: s.options.Program}}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("卸载操作已取消：%w", err)
	}
	if err := validateSetupHome(home, s.options.Program); err != nil {
		return result, err
	}
	binding, found, err := s.options.Binding(ctx)
	if err != nil {
		return result, fmt.Errorf("核对卸载服务绑定失败：%w", err)
	}
	if found {
		if !sameSetupPath(binding.Executable, s.options.Program) || !sameSetupPath(binding.Home, home) {
			return result, fmt.Errorf("现有 lgmon 服务不属于此程序路径和 home，拒绝停止或移除")
		}
		if err := s.options.Service.Uninstall(ctx); err != nil {
			return result, fmt.Errorf("卸载本安装服务失败，程序文件和数据应继续保留：%w", err)
		}
	}
	result.Service, err = s.options.Service.Status(ctx)
	if err != nil || result.Service.Installed {
		if err == nil {
			err = fmt.Errorf("仍检测到 lgmon 服务")
		}
		return result, fmt.Errorf("卸载结果尚未确认，拒绝继续删除程序文件：%w", err)
	}
	result.Status, result.Message = "ok", "已确认本安装的 lgmon 服务不存在；全部 home 数据保持原样"
	return result, nil
}
