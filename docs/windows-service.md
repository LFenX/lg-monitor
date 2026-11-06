# Windows 服务与人工验收

实现支持 `lgmon install`、`uninstall` 和 `service start|stop|status`。安装只注册服务，由本人显式启动；名称固定为 lgmon，LocalSystem、自动启动、失败后 10 秒重启。开发验证使用假 SCM/事件来源和只读原生查询，尚未注册真实服务、改防火墙、重启电脑或验证真实飞书收件。

## 安装准备

以管理员打开 Windows PowerShell，在仓库根目录执行。`install` 自动把当前程序复制到 `%ProgramFiles%\lg-monitor\lgmon.exe`，服务始终指向该副本；后续构建 bin/lgmon.exe 不影响它。不要直接从开发目录注册 SYSTEM 服务。

```powershell
$projectRoot = (Get-Location).Path
$serviceHome = Join-Path $projectRoot 'dev-home\service-acceptance'
$program = Join-Path $projectRoot 'bin\lgmon.exe'
$installedProgram = Join-Path $env:ProgramFiles 'lg-monitor\lgmon.exe'
& $program service status --json
```

上面的只读查询不注册服务。后续安装、启停、卸载与重启须本人明确确认；AGENTS.md 禁止执行者自行安装或重启已有服务，也禁止自动重启电脑。

只装采集器时，先在另一台监控器生成配对码并核对指纹，再按 [配对命令](cli.md) 将本机 serviceHome 配对。监控目标集中在监控器修改，采集器不用复制 config。然后本人执行：

```powershell
& $program install --role collector --home $serviceHome --json
& $program service start --json
& $program service status --json
Get-CimInstance Win32_Service -Filter "Name='lgmon'" |
    Select-Object Name,StartName,StartMode,State,PathName
```

安装结果应为 LocalSystem、自动启动；运行状态为 running。只装 monitor 或 both 时，先在 serviceHome/config 配置目标/通知并保存机器 DPAPI 凭据，然后以 `install --role monitor` 或 `install --role both` 注册，其他步骤相同；需要改变监听地址时在 install 带 --listen。不要把实例指向当前正在被交互式 run 使用的同一个 home，也不要覆盖已有同名服务。

## 文件权限核对与升级

安装时保护程序目录：SYSTEM、Administrators 完全控制，Users 只读/执行，关闭来自父目录的权限继承。安装和每次服务启动时保护 home：只有 SYSTEM、Administrators 完全控制，清除既有子项的额外授权，让普通子项继承。PKI 和密钥组件还会设置独立的保护 DACL，服务写入时同样不授予交互创建者权限。home 即使用 `--home` 放在 D 盘也执行这些限制；不接受磁盘根目录、链接、重解析点或包含硬链接的受保护树。

本人安装后检查：

```powershell
icacls (Split-Path -Parent $installedProgram)
icacls $installedProgram
icacls $serviceHome
Get-ChildItem -LiteralPath $serviceHome -Force | ForEach-Object { icacls $_.FullName }
```

程序目录和文件不应出现 Users、Authenticated Users 或其他普通账户的写入/修改/删除授权；程序目录显示 Users 的 RX 正常。home 及子项不应出现普通用户授权。服务每次启动检查程序文件及其目录的所有者和写权限；不安全时写 Windows 应用事件日志、拒绝启动，并提示重新 `lgmon install`。普通用户直接读取服务数据库时会提示以管理员身份运行；后续 HTTP API 提供授权读取。

升级只操作这个 lgmon 服务。本人确认停止后复制新程序，重置副本的继承与所有者，再启动：

```powershell
& $program service stop --json
Copy-Item -LiteralPath (Join-Path $projectRoot 'bin\lgmon.exe') -Destination $installedProgram -Force
icacls $installedProgram /reset
icacls $installedProgram /setowner '*S-1-5-32-544'
& $installedProgram service start --json
& $installedProgram service status --json
```

首次放入脚本也由管理员操作：在 `$serviceHome\scripts` 建目录、复制可信的 `.ps1/.cmd/.bat/.exe` 文件。新文件继承 home 的限制；复制已有文件后可用以下命令规范其权限，再按 [脚本配置](collection.md) 在监控器引用文件名：

```powershell
$scriptDirectory = Join-Path $serviceHome 'scripts'
icacls $scriptDirectory /reset /T
icacls $scriptDirectory /setowner '*S-1-5-32-544' /T
icacls $scriptDirectory
```

权限设置依据：[SetNamedSecurityInfo 与继承传播](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-setnamedsecurityinfow)、[文件与目录访问权限](https://learn.microsoft.com/en-us/windows/win32/fileio/file-security-and-access-rights)。

服务不继承当前交互用户的 HTTPS_PROXY。飞书一般可直连；需要代理时在对应 notifiers.yaml 的应用配置中显式设置 proxy 地址。安装提示不输出代理值。

## 服务身份发送

此项在运行 monitor/both 的那台服务机器上执行。collector 不保存飞书应用凭据，不能以 collector 的身份发通知。本人先通过 feishu setup 配好并确认测试群，再把已确认渠道填入变量：

```powershell
$confirmedNotifier = Read-Host '输入已经确认属于测试群的通知渠道 ID'
& $program feishu test --home $serviceHome --service --notifier $confirmedNotifier --json
```

调用方只加载管理 TLS 身份；正在运行的服务验证管理证书，在 LocalSystem 上读取飞书凭据、渲染和发送，发送尝试写入其 lgmon.db。结果 message 必须说明 LocalSystem，并有 messageId；本人还要在测试群确认真实收到。控制台 monitor 和采集器证书不能代替服务身份，原始凭据、DPAPI 错误内容不回传。

## 重启与跨机器验收

1. 在另一台监控器为此采集器配置一个本人的演示目标，确认该目标状态能更新；不使用或关闭 LFEN、lgplayblog、Nebula、PostgreSQL、cloudflared、Tailscale 等真实服务。
2. 本人重启被监控电脑，保持不登录；执行者不会调用重启命令。
3. 在另一台监控器确认采集器目标继续更新，记录重启时间、最后状态时间和采集器 ID。不要把 collectors list 中存在配对记录当作在线证明。
4. 仅操作自己创建的演示进程，验证触发/恢复和测试群真实收件。承担监控的服务机器另执行上一节 --service 测试，确认服务能读取机器密钥。
5. 记录事件日志、JSON 日志、群内收件时间以及失败恢复情况。检查点/状态转换遵循 [Microsoft 服务状态规则](https://learn.microsoft.com/en-us/windows/win32/services/service-status-transitions)。非崩溃失败恢复标志按 [Microsoft 的生效时机](https://learn.microsoft.com/en-us/windows/win32/api/winsvc/ns-winsvc-service_failure_actions_flag) 在重启后的验收中确认；单纯向服务发送正常停止不会触发失败重启。

```powershell
Get-Content -LiteralPath (Join-Path $serviceHome 'logs\lgmon.log') -Tail 20
Get-WinEvent -FilterHashtable @{LogName='Application'; ProviderName='lgmon'} -MaxEvents 10
```

## 停止与卸载

仅在本人决定结束此 lgmon 验收实例后执行，其他服务不会被操作：

```powershell
& $program service stop --json
& $program uninstall --json
& $program service status --json
```

卸载保留 serviceHome 的配置、数据、密钥和日志；不自动删除。ServiceMain/SCM 通过当前依赖的 [x/sys v0.49.0](https://pkg.go.dev/golang.org/x/sys@v0.49.0/windows/svc/mgr) 接入，程序保持 CGO_ENABLED=0。

## 当前验收状态

| 项目 | 当前状态 |
|---|---|
| 命令、SCM 门禁/回滚、生命周期、JSON 日志与假身份发送审计 | 自动测试验证 |
| 原生 SCM 只读查询与临时日志文件 | 已验证，不安装服务 |
| 实际 LocalSystem 安装、自动启动、失败后重启 | 待本人授权/验证 |
| 不登录时另一机器看到采集更新 | 待本人重启与验证 |
| LocalSystem 读取真实凭据并发到已确认测试群 | 待本人确认测试群与收件 |

后三项未确认前，PLAN 的 M1-06 保持未勾选；按 AGENTS 的人工待验约定继续独立任务。
