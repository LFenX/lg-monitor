# 第一版验收

本文件对应 [PLAN M1-19](PLAN.md) 的八项原始要求。工程测试通过不等于真实验收通过；目前下表全部待本人执行或确认，M1-19 保持未勾选。

| 项目 | 自动部分 | 本人必须确认 | 当前结果 |
|---|---|---|---|
| 1 B 机监控器和网页飞书向导 | 配置校验、数据库快照 | 安装、网页从零接入、真实测试群收件 | Pending |
| 2 A 机采集器服务和 Tailscale 配对 | 已登记身份、状态与心跳记录 | 两台实机、服务启动、完整指纹核对 | Pending |
| 3 演示进程退出后 15 秒告警 | 指定事件、状态和渠道的成功提交审计 | 15 秒内群内真实收到、恢复卡片 | Pending |
| 4 离线 10 分钟后补传 | 原始 down→up 时间、数据缺口 | 实际断网时长、补传追平、A 机原操作时间一致 | Pending |
| 5 关机 35 秒失联及未登录恢复 | 失联通知提交审计、恢复状态快照 | 关机时间、真实收件、开机未登录已恢复 | Pending |
| 6 网页自定义标准模板 | 保存后配置校验 | 未保存模板试发、实际卡片效果、确认后保存 | Pending |
| 7 SQL、MCP、面板 Agent 任务 | 查询和结果可复核 | 新 Agent 会话独立完成，原配置未丢失 | Pending |
| 8 连续 24 小时采集器资源 | 原始进程 CPU/RSS/覆盖统计 | 自监控目标正确、完整时段、按正式口径判定 | Pending |

## 准备与只读预检

需要 A、B 两台 Windows 10/11 x64 实机、已登录的 Tailscale、本人确认的飞书测试群。安装、开机启动、断网、关机重启和真实发消息均由本人决定并执行；脚本不执行这些操作。已有 lgmon 服务不能被覆盖；其他业务服务不能作为故障实验对象。

使用专用验收 home，B 只与本次验收的 A 配对，通知只指向本人确认的测试群；不能将验收全局失联策略覆盖到已有业务监控器。若两机已有必须保留的 lgmon 服务，先由本人另选验收机器，不卸载或替换原服务。

使用同一已通过 CI 的版本，在验收记录中保存完整提交 SHA、A/B 的版本、操作人、开始/结束时间和所用角色。凭证只在本机网页填写；报告和截图先隐藏真实 App ID、群 ID、用户 ID、令牌、证书及内部业务数据，不提交数据库。

先阅读 [Windows 服务](windows-service.md)、[配对与传输](transport.md)、[网页使用](web-panel.md)、[Agent 指南](agent-guide.md)。A/B 的系统时间须校准，由本人核对；时钟偏差会影响跨机的 15/35 秒比较。采集器时钟警告不能忽略，也不能据此修改历史采样时间。

以下命令在 **B 机**、仓库根目录运行。`$monitorHome` 必须是监控器真实 home，不能填采集器 home；不要使用 PowerShell 的系统变量 `$HOME` 存放路径。已安装版可以把 `$monitorExe` 换成受保护的 Program Files 程序路径。

```powershell
$monitorExe = (Resolve-Path -LiteralPath '.\bin\lgmon.exe').Path
$monitorHome = 'C:\ProgramData\lg-monitor'
.\scripts\m1-acceptance.ps1 -LgmonPath $monitorExe -MonitorHome $monitorHome -CheckOnly
```

预检只运行 `version`、`config validate --json`、`status --json`。尚无数据库时允许预检通过，并明确 `databaseAvailable=false`；它不会创建数据库、运行监控器或发消息。读取服务保护目录需要相应权限，由本人选择权限运行，脚本不提权。

脚本输出 JSON：

- `automaticStatus=Passed`：本次自动检查的事实满足该模式要求。
- `Pending`：证据未覆盖本模式的完成条件，退出码 2；补齐后重跑。
- `Failed`：已观察到超时或缺口等不合格事实，退出码 1。
- 参数、配置、只读命令失败：报错并退出 1，修复原因后重跑。
- 每种结果都保留 `manualAcceptance=Pending`。单次 `Passed` 不表示整版已验收。

`Snapshot` 读取已保存状态，包含当前全局暂停和规则错误；`collectors.lastSeen` 是最后心跳的接收时间，不是实时连接证明。两次快照之间 lastSeen 前进，且目标有新采样，才是本次观察到持续采集的证据。

```powershell
.\scripts\m1-acceptance.ps1 -LgmonPath $monitorExe -MonitorHome $monitorHome -Mode Snapshot
& $monitorExe collectors list --home $monitorHome --json
```

验收规则不能被静音、确认或维护时段覆盖；全局通知须未暂停。脚本只报告这些状态，不会为验收解除真实配置里的静音或暂停。

## 1. B 机安装和飞书接入

1. 先只读查询 `lgmon service status --json`，确认没有需要保留的 lgmon 服务。按 windows-service.md，由本人安装监控器角色并启动；独立托盘使用其专用认证流程，不能把本机只读网页会话当作配置令牌。
2. 打开网页，以本人本地保存的 config/admin 令牌进入飞书向导。read 会话只能看，不能保存配置。不得把令牌粘贴到验收记录或 Agent 聊天。
3. 从空的飞书接入状态开始，选择新建或已有应用。本人填写凭证，按界面处理机器人能力、必要权限、发布及可用范围，再把机器人加入**本人已确认的测试群**。
4. 在网页发现并选择该群，选标准模板，试发。本人核对群名与卡片，点“确认收到”后才保存通知渠道；仅 API 返回成功不能代替这一步。
5. 保存脱敏截图和所选渠道的本地配置 ID。运行预检与 Snapshot，确认配置有效；不要复制加密 Secret 文件到报告。

结果 Pending：真实网页操作、应用和群内收件尚未由本人确认。

## 2. A 机服务与 Tailscale 配对

按 transport.md，在 B 创建一次性配对码；本人通过可信渠道核对 B 的 CA 指纹（16 位短指纹或完整 64 位，不接受旧的 8 位），在 A 使用 B 的真实 Tailscale 地址配对。配对码和证书不写报告。

配对完成后，由本人按 windows-service.md 安装 **collector** 角色。安装会复制到 Program Files 并保护 home，只有本人明确执行时才发生。不能让普通用户可写的程序或脚本以 SYSTEM 身份运行；自定义脚本只放本机受保护的 scripts 目录。

在 B 确认出现 A 的稳定 collector ID，连续读取两次快照，核对 A 的 hostname、前进的 lastSeen、目标采样时间和无时钟警告。将实际 ID 赋给后续命令的 `$collectorID`。这一步必须是真实双机，单进程或回环集成测试不能代替。

## 3. 自建演示进程与 15 秒告警

仅操作本次创建的演示进程。不得停止 Nebula、LFEN、lgplayblog、数据库、Tunnel、Tailscale 或其他既有进程。

在 **A 的交互式 PowerShell** 创建自有 ping：

```powershell
$demoExe = Join-Path $env:WINDIR 'System32\ping.exe'
$demoArguments = @('-t', '127.0.0.42', '-l', '2057')
$demo = Start-Process -FilePath $demoExe -ArgumentList $demoArguments -WindowStyle Hidden -PassThru
$demo.Id
```

在 B 原 targets.yaml 的 `targets:` 列表末尾添加下例，替换 collector 值；保留原列表其他条目，按现有列表层级缩进，不要把文件根对象换成数组。将 payload 数字和匹配规则一起更换可避开已有 ping；用 `targets test` 核对只匹配上面记录的自建 PID。

```yaml
- id: acceptance-demo
  name: 自建验收进程
  collector: FAKE_REPLACE_WITH_A_COLLECTOR
  match:
    process: ping.exe
    cmdline: '127[.]0[.]0[.]42.*-l\s+2057(\s|$)'
```

在原 rules.yaml 的 `rules:` 列表追加进程告警规则，notify 换成本人已确认的测试渠道 ID：

```yaml
- id: acceptance-demo-down
  name: 验收演示进程退出
  type: process_absent
  target: acceptance-demo
  for: 0s
  severity: warning
  notify: [FAKE_REPLACE_WITH_CONFIRMED_NOTIFIER]
```

先校验配置，确认 A 收到配置且目标为 up。在 A 记录 UTC 并停止保留句柄的演示进程：

```powershell
$actionUtc = [DateTimeOffset]::UtcNow.ToString('o')
$demo.Refresh()
if (-not $demo.HasExited) { $demo.Kill(); $demo.WaitForExit() }
$demo.Dispose()
$actionUtc
```

本人同时记录飞书实际显示卡片的时间，须在故障后 15 秒内。B 上填入实际参数，事后也可重查：

```powershell
$collectorID = 'FAKE_REPLACE_WITH_A_COLLECTOR'
$notifierID = 'FAKE_REPLACE_WITH_CONFIRMED_NOTIFIER'
$actionUtc = '2030-01-01T00:00:00Z' # 替换为刚才真实记录的、两机时钟已核对的时间
.\scripts\m1-acceptance.ps1 -LgmonPath $monitorExe -MonitorHome $monitorHome -Mode Delivery -CollectorID $collectorID -TargetID acceptance-demo -RuleID acceptance-demo-down -NotifierID $notifierID -FromUtc $actionUtc
```

脚本按原始 firing 事件审计，不接受其他渠道、旧事件、resolved、renotify、resume 或跳过记录。`submissionDelayMS` 以输入的操作时间为起点；审计时间是渠道调用**返回后**记录的 B 机时间，不能拿它冒充飞书客户端收件时间。若无成功审计但还未到期限则 Pending，到期后 Failed。

在 A 用原 `Start-Process` 命令重新创建 `$demo`，核对目标恢复为 up，同一告警的 resolved 卡片到达且未重复触发。保存脱敏证据。PRD 的“5 秒发现”单独核对原始状态变化时间与操作时间，不拿 15 秒提交审计代替。

## 4. 断网 10 分钟、补传与原始时间

1. 保持演示进程运行和目标 up。本人记录 A/B 的 UTC 起点、A 机自监控时间和 B 的最新状态。
2. **本人**断开 A 所用网络并计时 10 分钟，保留采集器服务运行；不由 Agent 修改网络、防火墙或 Tailscale 服务。期间按上节句柄方法关闭自有演示进程，记录退出时间，再重新创建并记录恢复时间，两次间隔至少数个目标采集周期。
3. 本人恢复 A 网络，在 B 观察补传追平后再记录结束时间。补传期间不应把早已结束的故障当成新故障发送；追平后按最新状态评估。
4. 在 B 以覆盖整个实验的源时间窗口查询：

```powershell
$fromUtc = '2030-01-01T00:00:00Z' # 替换为真实实验起点
$toUtc = '2030-01-01T00:12:00Z'   # 替换为真实实验终点
.\scripts\m1-acceptance.ps1 -LgmonPath $monitorExe -MonitorHome $monitorHome -Mode History -CollectorID $collectorID -TargetID acceptance-demo -FromUtc $fromUtc -ToUtc $toUtc
```

必须看到按原始采样时间排列的 down→up，并与 A 的退出/恢复记录逐项核对；采集延迟应在已配置周期内解释。脚本列出源时间、不重写成到达时间；检测到所选时间内的数据缺口/late_dropped 则 Failed。没有 down→up 时 Pending，先确认目标、时间范围和补传是否追平，不能直接判通过。查询被截断会报错，缩小范围；不能把截断结果当完整历史。

此检查不能证明真实断网 10 分钟，也不能证明其他目标都补传完成。默认缓存有时长和容量双上限；触及容量发生丢弃必须如实记录缺口。不要通过放宽结果来掩盖。

`gaps` 只包含实际数据丢失的 `data_gap` 和 `late_dropped`；`clock_skew`、`clock_synced` 单独列在 `clockEvents`，不能当成丢数判失败。时钟事件仍需本人对照原操作时间，确认偏差及恢复过程；没有缺口或时钟已恢复，都不能代替“原始时间正确”的人工验收。

## 5. 关机 35 秒失联、开机未登录恢复

在专用验收 B 上配置 `collector_offline` 规则，参照 [规则文档](rules.md)，指向本人确认的测试渠道并记录实际 ID。该规则适用于全部配对采集器，不是仅限 A 的目标规则；因此本次 B 只能配对验收 A，不能更改已有业务监控器的全局策略。本人记录关机时间并关闭 **A**，在 B 计时观察，35 秒内群中应真实收到失联告警。不要重启开发机 B 或其他服务来模拟。

```powershell
$shutdownUtc = '2030-01-01T00:00:00Z' # 替换为本人记录的 A 关机时间
.\scripts\m1-acceptance.ps1 -LgmonPath $monitorExe -MonitorHome $monitorHome -Mode Delivery -Scenario Offline -CollectorID $collectorID -RuleID acceptance-collector-offline -NotifierID $notifierID -FromUtc $shutdownUtc
```

失联规则不配置 target，告警内部使用连接身份 @collector；脚本 Offline 模式自动选用该身份，不能传演示进程的 target。35 秒只用于本次失联触发审计；本人收件时间另记。

本人再次开机 A，停留登录界面，**不要登录**。在 B 观察 lastSeen 开始前进、服务采集的新时间、同一失联告警转 resolved 及真实恢复卡片；通过后才登录 A 核对服务日志。演示 ping 是交互进程，不应在重启前注册自启动；它的 down 不等于采集器未恢复。这里只证明采集器服务无需登录，不能用登录后的状态补证。

## 6. 网页新模板与未保存试发

在模板库从“标准”复制一个新的 ID，修改标题、颜色、字段顺序、备注。在**保存前**预览 firing/resolved 并试发到本人确认的测试群，核对编辑中的内容确实出现在真实卡片；本人确认效果后保存。再把规则 template 改成该 ID，校验并观察真实故障/恢复选用了它，保留原渠道和其他规则。

模板损坏的自动降级已由测试覆盖，真实验收仍需本人选择安全方式核对。不得为了试错破坏正在使用的生产模板或收件人。接入新群的“试发后确认保存”和未保存模板试发是不同流程，两者都需本人确认。

## 7. 三类 Agent 任务

按 agent-guide.md 和 M1-18，开一个新会话，只给 `agent prompt` 的输出。原 Nebula 服务只能只读检查和增加监控，故障实验继续用自建 ping。

| 类型 | 交给 Agent 的任务 | 本人核对 |
|---|---|---|
| 只读 SQL | 查询 A 在指定时段的演示目标状态变化和计数；解释表结构与时间单位 | SQL 只读、原始时间与步骤 4 一致，无密钥查询 |
| MCP | 以 read 令牌列采集器、目标和当前告警，并查询同一时段 | MCP 结果与 SQL 一致；read 写操作被拒绝，不提升权限 |
| 面板 | 在监控器原有文件旁新增一页，显示 A 的状态、CPU 趋势和告警，使用固定卡片 ID | 实际渲染/刷新、指定主机过滤、手机深浅主题，原面板未覆盖 |

配置类任务由本人提供 config/admin 权限或批准本地文件访问；不能假设 read MCP 能写面板。新增监控、规则与 critical 使用排查模板的完整实际会话仍需本人签字；自动示例加载不能代替 Agent 会话。模板里的诊断数据只能来自已有采集/调用方输入，不应宣称能任意读取采集器文件。

## 8. 自监控 24 小时资源

在 B 的 targets 列表追加采集器自身目标，collector 仍替换为 A：

```yaml
- id: acceptance-collector-self
  name: 采集器自身资源
  collector: FAKE_REPLACE_WITH_A_COLLECTOR
  match:
    process: lgmon.exe
    cmdline: '\brun\b.*--role\s+collector\b'
```

先用匹配测试核对：只匹配 A 的服务采集器进程，不能混入 monitor、tray 或其他 lgmon。服务参数里 run 与 --role 之间可以有 --home，因此不能写成相邻参数的正则。原始记录 RSS 单位为字节，CPU 已按整机归一化；不要再除逻辑 CPU 数。

留出原始数据保留余量，避免采样满 24 小时后首端已清理。建议在实验前由本人把 raw 保留改成 48h，同时保证 raw≤minute≤hour，校验通过后记录修改；不要仅凭分钟平均值反推原始最大 RSS。记录实际目标采样周期、PID/创建时间、实验开始和结束，运行满 24 小时后：

```powershell
$resourceFromUtc = '2030-01-01T00:00:00Z' # 替换为自监控实际开始时间
$resourceToUtc = '2030-01-02T00:00:00Z'   # 替换为自监控满 24 小时的实际结束时间
$targetCount = 2 # 替换为 A 的实际已应用目标数，至少两个
$probeCount = 0  # 替换为 A 的实际已应用探测数，没有探测时为零
.\scripts\m1-acceptance.ps1 -LgmonPath $monitorExe -MonitorHome $monitorHome -Mode Resources -CollectorID $collectorID -TargetID acceptance-collector-self -FromUtc $resourceFromUtc -ToUtc $resourceToUtc -TargetCount $targetCount -ProbeCount $probeCount
```

`-TargetCount` 和 `-ProbeCount` 由本人先在 B 的实际已应用配置里核对，再填入。目标至少两个（演示进程和自监控），没有探测可填零。不要按探测结果行数推断配置数量，长周期探测会没有本窗口结果。这两个数以 `loadSource=operator` 记录，脚本不把操作者填写的信息当作自动查证的配置事实。

自动判定现按 PLAN 的“资源判定口径”执行：

- 只查原始 `process_samples`，窗口为 `[from,to)`，固定目标周期为当前采集器默认的 2000 ms。
- RSS 是含共享页的工作集，阈值为 **41943040 字节（40 MiB）**。任一原始采样达到阈值即 Failed，包括覆盖不足的窗口。不得用任务管理器的专用工作集代替。
- CPU 已归一到整机。按入库顺序取相邻间隔，后一条 CPU 乘该间隔求加权平均；第一条、零间隔与回拨的行不参与计算，后两种保留在 `nonPositiveIntervals`，不能先按时间排序把回拨抹掉。
- 以下七条都满足才算覆盖完整：窗口至少 24h；只有一个 PID/启动时间身份且启动早于 from；没有同一时间多个进程；首条在 from+10s 以内且末条不早于 to−10s；相邻最大间隔不超过 30s；条数至少达到预期 95%；本采集器窗口内没有 `data_gap`/`late_dropped`。
- 覆盖完整、CPU 加权均值 < 1%、RSS 最大值 < 41943040 才 Passed。覆盖完整但 CPU ≥ 1% 为 Failed；其余覆盖不足为 Pending，`reasons` 列出原因。24h/2s 的预期为 43200 条，95% 为 41040 条。

报告在 `evidence` 提供窗口/周期、实际及预期条数、首末采样、最大间隔、进程身份/多进程歧义、数据缺口、时钟事件、被排除的非正间隔、加权 CPU、RSS 最大值及其时间、总均值/首小时均值/末小时均值、阈值、负载数量及原因。首末小时均值只报告，不用于判定；`clock_skew`/`clock_synced` 单列，不算数据缺口。进程身份变化要另外核查采集器稳定性，实验期间 A 不能睡眠。

不论自动结果，`manualAcceptance` 始终 Pending：本人仍需确认被采样的是 A 的采集器服务进程、实际配置数量正确、窗口内是日常负载。本轮自动夹具不等于真实运行 24h，也不证明 50 个目标下的资源占用。

## PRD 原文逐项追溯与未闭环项

范围依据 [PRD 第 5、6、8 节](PRD.md)，实现以 PLAN 和已合并 REVIEW 为准。下表列出实现载体和验收边界，未把“有测试文件”写成“真实环境已通过”。除明确的工程验证之外，真实操作证据均待上面步骤完成。

| 原文编号 | 实现载体/本轮核对点 | 验收边界 |
|---|---|---|
| C1 系统指标 | collector 的 system/disk/network；store 原始与汇总表 | 步骤 2、7、8 实机数据待验 |
| C2 完整进程字段 | collector 本机快照、targets test | 远端 list_processes 按 REVIEW/PLAN 有意不返回命令行；原始本机采集和远端最小字段须区分 |
| C3 名称/命令行/端口/目录/服务匹配 | config.Match、collector 匹配与目录源、RPC/CLI/API/网页配置状态 | exeDir/workDir、未知语义、按需缓存和旧版能力检查已实现；A 机本人确认 Pending，见下方 M1-20 待验 |
| C4 服务状态、C5 端口 | Windows SCM/端口采集、对应规则 | 实机自有演示对象待验，不操作现有业务服务 |
| C6 HTTP/TCP | config.Probe、collector HTTP/TCP、TLS 持久结果和详情、cert_expiry | 关键字有界读取、TLS 链最早到期和证书规则已实现；本人选真实 HTTPS 对照浏览器仍 Pending，见下方 M1-21 待验 |
| C7 脚本 | 受保护本机 scripts、参数白名单、超时/JSON 指标 | 按 R3-04 修订，“任意远端命令”不再允许；真实 SYSTEM 脚本权限待服务验收 |
| T1 主动连接、T2 mTLS | transport 配对、证书与连接测试 | 步骤 2 双机/Tailscale/完整指纹待验 |
| T3 离线补发 | 持久 outbox、批传/幂等/缺口、R3/R4 修复 | 步骤 4 待验；缓存容量耗尽不是无条件 24h 不丢保证 |
| T4 配置下发 | transport/app 配置版本与快照测试 | 步骤 3 添加目标后实际下发待验 |
| T5 采集器失联 | 心跳、collector_offline 规则 | 步骤 5 真实关机/未登录恢复待验 |
| A1 内置规则 | rules 的进程/服务/端口/探测/证书/指标/失联/脚本 | cert_expiry 已实现并验证独立探测实例、生命周期和回放；事件日志匹配由 4a7ae48 同 C9 改定 M2，M1 不实现 |
| A2 SQL 规则 | 隔离只读规则查询、实例 key 与历史回放 | 步骤 7 实际查询待验 |
| A3 生命周期 | 持续时间、事件身份、恢复/去重、补传抑制 | 步骤 3、4、5 真实卡片与时间待验 |
| A4 静音/维护/确认/抖动 | rules/store controls、通知决策与全局暂停 | 自动测试覆盖；真实操作须按需在测试规则验收，不能悄悄解除原静音 |
| N1 飞书应用、N3 Windows 通知 | SDK sender、toast、通知审计 | 飞书真实收件与 Windows 交互式通知均 Pending |
| D1 总览、D2 声明式面板 | web、dashboards、只读 SQL 视图 | 步骤 7 实机展示与热更新待验 |
| D4 移动端/主题 | web 响应布局与主题，已有前端/浏览器验证 | 真实手机访问待验 |
| G1 文件/Schema/热加载 | config/schema/documents/watch | 示例及无效变更保留旧版本自动验证；实际热加载待步骤 3、7 |
| G2 CLI JSON | docs/cli.md、命令测试 | 结构化业务命令已有；version、交互向导等文本命令与“全部支持 --json”的 PRD 绝对表述不等同，保留范围差异 |
| G3 REST/OpenAPI/SSE | api 路由同源 Schema、鉴权/SSE 测试 | 实际局域网访问与重新连接待验 |
| G4 SQL、G5 MCP | store 只读查询、api MCP/stdio/HTTP SDK | 步骤 7 新会话与实际权限待验 |
| G6 文档/技能 | agent-guide、agent install/prompt | 临时目录/原生 PowerShell 已验证，新会话 Pending |
| G7 权限分档 | read/config/admin 和独立托盘认证 | 远程执行 T6 尚未开发，admin 不代表已提供执行能力 |
| F1–F5 飞书向导 | CLI 与网页同一接入逻辑、DPAPI、机器人/权限/群/试发确认 | 步骤 1、6 真实应用/群收件待验；建群可选项没有授权时不执行 |
| K1 四预设、K2 基础编辑、K3 原始 JSON | cards 渲染/安全校验、网页编辑/预览 | 两种状态渲染已测，四预设真实收件仍见 acceptance-m0 |
| K4 渠道/规则模板、K5 降级 | 选择优先级、错误回退与审计 | 步骤 6 待真实确认 |
| 已提前实现的 P1 | A5 回放、N2/N4 Webhook、N6 路由、K6 网页模板库 | 按 M1-08/12/14 已交付；不据此声明其他 P1 已实现 |
| 尚未实现的 P1/P2 | C8/C9/C10/C11、T6/T7、A6 完整日志诊断、N5、D3/D5/D6 | 按 PLAN 的 M2 任务表、既定设计与开工条件推进；人工验收和独立开发并行 |
| CPU/RSS、5 秒发现、15 秒到达 | 步骤 3、8 的原始时间、真实收件和自监控 | 资源算法已按 4a7ae48 实现；真实 24h、原始时间和收件时限仍待本人确认 |
| 离线至少 24h、20×50 规模 | M1-22 固定容量测试与真实 30 分钟整链路负载 | 工程验证通过：24h 含 WAL 预留 248446976 字节，占默认 49.7%；30 分钟 1147122 条完整，提交 p99 1027.5028ms，见下方报告。真实断网 24h 仅作可选人工项 |
| 保留时长可配 | raw/minute/hour、整点水位与迟到事件 | 自动验证已有；长期实际留存须持续观察，不能声称已运行一年 |
| 单 exe、Windows 10/11 x64 | CGO_ENABLED=0 构建、嵌入前端、服务安装 | Win11 本机/CI；Windows 10 实机与双击安装体验待本人核验 |
| 安全要求 | mTLS、凭证保护、只读 SQL、token scope、R3/R4 | 自定义 HTML D3 尚未实现；受限远程操作默认不存在，不能声称该流程已验收 |

原三处范围问题已由 4a7ae48 解决：M1-20、M1-21 必须完成后才能勾选 M1-19；事件日志归 M2。两项工程已补齐、本人待验；不能通过删去原文或改写检查表闭环。

M1-20 本人待验：在 A 机选定一个真实工作目录目标，保留原 targets，先通过远端试匹配核对 workDir、PID 与实际程序，保存后在采集器列表核对 config.state=applied 和新采样。读不到时应显示 unknown；旧版无 match_dir 时应显示 outdated、顶部升级提示并保留上一版配置，不应误报进程退出。本人结果仍为 Pending，不操作已有服务制造故障。

M1-21 本人待验：选本人确认的真实 HTTPS 网站，在已升级采集器的目标增加或核对 http probe，保留现有配置，校验后检查 config.state=applied 和最新结果。在网页目标详情核对“证书到期”、主体和观测时间，再用浏览器查看证书链：比较叶子与中间证书中最早到期的一张，自签根不计；链中间证书先到期时不能只对照叶子。当前网页时区为浏览器本地时区，通知日期为监控器本地时区，先换算到同一时区比较。本人结果仍 Pending；不变更真实证书或服务来制造过期，不自动发送通知。

## M1-22 工程证据

缓存默认 500000000 字节，本机 cache.maxSize 范围 64MB～10GB，重启生效。当前占用按已用页计算，写入速度按负载+24 字节的一小时分钟桶，预热五分钟；扣除 8 MiB WAL 后估算预计时长。CLI/网页明确提示不足 24 小时，不能对任意多进程或额外指标承诺无条件 24h。

固定形状：50 个目标、各一进程和一条 30 秒 HTTP 探测，进程 2 秒、完整状态 10 秒、系统 5 秒，模拟 10 分钟。最终实测 19120 条、protobuf 1289072 字节、SQLite 1667072 字节，均摊 87.2 字节；24h 加 WAL 预留为 248446976 字节，占默认 49.7%，低于 70%。累计负载+24 与已用页相差不超过 10% 且不低估的断言通过；确认、过期、超限丢弃仍计入速率。调大、调小可容纳数据、暂缓调小后清空生效、整理失败继续运行均有回归，数据装得下但尚未确认时的整理也保留记录。

规模验证使用独立监控器测试进程、真实 SQLite/规则/通知队列、假通知客户端，20 个模拟采集器持真实配对证书经回环 mTLS 传输，各 50 个目标，按同样周期生成数据，真实运行 1800.043 秒。每五分钟随机目标 down 后恢复，一台采集器断开两分钟并在离线时经历已结束的故障。最终测试返回 PASS；[完整脱敏报告](m1-22-scale.json)保留全量计数、五次故障、逐分钟队列及资源数字。

| 原文判据 | 最终结果 |
|---|---|
| 提交 p99 < 2 秒 | 1027.5028ms；1147122 条均有首次发送与真实原始事务提交时间 |
| 待处理量不随时间增长 | 24 个完整稳态分钟、每分钟 120 次采样，各采集器均观察到队列清空；最终全部确认 |
| 规则 5 秒 / 假渠道 15 秒 | 五次故障各触发一次并恢复；最大 22ms / 27ms |
| 离线补传不丢不重、已结束故障不误发 | 序号错误 0、数据库条数逐类一致、数据缺口事件 0；离线目标未产生触发通知，结束时 active_alerts=0 |
| 总览 API p95 < 500ms | targets 40.0005ms、alerts 1.0595ms、collectors 5.0012ms |
| 另报 ACK 时延 | p99 1059.7372ms；1147122 条均观察到 ACK，未观察数 0 |
| 监控器资源与磁盘（只报告） | 平均 CPU 1.7350%（24 逻辑处理器归一化）、最大 RSS 107806720 字节；数据库 239939584 字节，约 479626032 字节/小时，默认保留折算 32516920245 字节 |

入库时延使用同机共享的 Windows QueryPerformanceCounter，起点为首次 Batch.SendMsg，终点为真实 IngestBatch 原始事务成功返回；逐条匹配全部序号，拒绝缺失、重复和负间隔，重连不重置首次发送时间。计时记录按批次追加到 64KiB 缓冲文件，父进程分析，避免把计时表算成监控器内存。发送到 ACK 的时延另行报告，它还包含规则处理与应用水位提交；未观察到 ACK 的记录在该分位按无限时延处理。计时器依据 [Microsoft 的高精度计时说明](https://learn.microsoft.com/en-us/windows/win32/sysinfo/acquiring-high-resolution-time-stamps)。独立于 API 请求，每 500ms 读取各采集器待处理量，记录分钟均值/最大值及逐采集器最小值；每个完整稳态分钟内，每台采集器必须至少一次清空队列。排除启动、第 8～12 分钟主动断链/补传及停止后的样本，再验证最终全量排空。两次既有运行均未计为完整通过，问题与修正保留在 PLAN 执行记录。规则发现及假渠道提交的上界为 5 秒、15 秒；这些假渠道结果不能代替本人真实收件。

CPU 按整机逻辑处理器总数归一化，只量独立监控器进程，内存取运行期最大 RSS。数据文件在结束后补齐最后的分钟/小时桶，再按 raw 24h、minute 14d、hour 365d 的默认保留时长分别估算磁盘；仅在收尾推进存储的注入时钟两小时，不修改系统时间，也不把估算当作一年实测。

```powershell
$env:CGO_ENABLED = '0'
go test ./internal/transport -run '^TestDefaultOutboxCapacityForFiftyTargetsWithHTTPProbes$' -count=1 -v
Remove-Item Env:LGMON_SCALE_SMOKE -ErrorAction SilentlyContinue
go test -tags 'integration webui scale' ./internal/app -run '^TestScaleTwentyCollectorsFiftyTargets$' -count=1 -timeout 40m -v
```

规模标签不进入常规 CI，容量测试进入。LGMON_SCALE_SMOKE 仅作短时夹具诊断，Formal=false，不能当作正式验收；正式运行需移除该变量，并保持 LGMON_SCALE_CHILD_CPU_PROFILE 未设置，避免诊断 profiler 干扰资源数字。测试只用自身进程、回环监听和临时目录，均由测试清理；假凭据与数据库不提交。缓存展示的[桌面](m1-22-cache-desktop.png)、[手机](m1-22-cache-mobile.png)截图来自真实 Edge，覆盖字段、低于 24h 提示及恢复后的移除；预热空值另由前端组件和心跳回归验证。user_decision 仍为 Pending，真实双机、服务、收件和 24h 资源验收不由本轮代替。

验收基线按 PLAN 382d48d：M1-22 工程提交完整 CI 成功后，记录完整 SHA 并推送 m1-rc1；本人使用该标签做手动验收，main 可继续独立 M2。完整 SHA 与 CI 结果将在提交产生并完成远端检查后补录。

## 记录、清理与最终确认

每项保留：完整 SHA、场景/两机角色、操作 UTC、期望/实际、脱敏证据路径、脚本 automaticStatus、本人结论和未解决问题。证据文件由本人保存到明确位置；脚本只输出 JSON，不自动采集凭证/配置/数据库。包含真实身份的报告不得提交仓库。

实验结束，本人用仍持有的句柄停止最后一个自建 ping 并 Dispose，只删除本次新增的 acceptance 目标、规则、模板、面板和对应临时资源；恢复本次明确调整过的保留设置。不删除其他人的配置，不终止其他进程，不擅自卸载服务或撤销原授权。

最终签字清单：

- M0-13 四预设真实卡片和单机链路确认。
- M1-01 双机传输、M1-06 服务/重启/真实收件确认。
- M1-17 真托盘、独立认证与本人选择的开机启动确认。
- M1-18 新 Agent 会话确认。
- 本页八项均有本人结论；M1-20、M1-21 及其本人待验已完成，所需返修已验证；事件日志按 Claude 的既定决定留在 M2。

当前 user_decision=Pending、manual_acceptance=Pending；禁止据工程提交、CI 或某次脚本 Passed 勾选 M1-19。人工验收与后续开发按 PLAN 的“推进规则”并行；独立 M2 工程任务可推进，M2 整体验收仍须等待本人签署 M1-19。
