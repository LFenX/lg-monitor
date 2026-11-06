# 审查意见

Claude 审查执行者（Codex）推上来的提交，意见写在这里。执行者每次开始下一个计划任务前，先处理这里还没关闭的条目。

## 怎么处理

- 未关闭的条目是 `[ ]`。按编号顺序处理；本轮的全部条目处理完，再继续 PLAN 里的下一个任务。
- 一个条目一个提交，提交信息以条目编号开头，例如 `R1-02: enumerate processes with NtQuerySystemInformation`。条目里写明“一起改”的，可以合成一个提交。
- 改完把 `[ ]` 改成 `[x]`，在条目末尾加一行 `处理：<提交号> <一句话说明>`，和代码放在同一个提交里。不要删除条目，也不要在 PLAN 的执行记录里重复写一遍。
- 对某条有异议、或者核实后发现不成立：把 `[ ]` 改成 `[?]`，写明理由和证据，然后继续处理别的条目。下一轮审查时回复。
- 审查意见是对 PLAN 的修订。两者冲突时，以审查意见为准。

优先级：**P0** 不改就达不到 M0 目标；**P1** 会造成误报、漏报或以后难改的约定；**P2** 体验和可维护性问题。

---

## 第 1 轮（2026-11-06）

审查范围：`875ebb2` M0-01 ～ `3aa5133` M0-04。

本机核对结果：`gofmt -l .` 输出为空，`go vet ./...`、`go test ./...` 和 `scripts\build.ps1` 都通过，`go test -tags integration ./internal/config ./internal/collector` 也通过。

做得好的地方：

- 配置报错带文件和行号，能一次列出全部语义错误。Schema 和示例有同步测试。
- 热加载遇到错误配置时保留旧配置。
- 进程身份用（PID，创建时间）。
- 集成测试只操作自己启动的进程，CI 的 Windows 编码问题也处理干净了。

主要问题集中在进程采集。它在真实机器上太慢，以普通用户运行时还会漏掉三分之一以上的进程。这两点不改，M0 的“15 秒内告警”达不到，而且会对数据库、Tailscale 这类以服务账户运行的进程误报。

### [x] R1-01 不要强推 main（流程，P1）

M0-01 到 M0-03 的 CI 跑在 `e6393a4`、`d6971de`、`acaa35c` 上。这三个提交后来被改写后强推，已经不在 main 的历史里。你在“持续执行约定”里已经改成“不为补写 CI 结果修订已发布提交”，这一点很好。还要再明确两点：

- 审查意见也直接提交到 main。强推（`--force` 或 `--force-with-lease`）会把审查提交冲掉，所以 main 上一律不要强推。
- 每个任务或条目开始前先 `git pull --rebase`。推送被拒时，同样先 rebase 再推。

**完成标准**：AGENTS.md 里已经写入这两条（本轮由 Claude 加上了）。你确认后，把本条标成 `[x]`。

处理：`5b31840` 已确认禁止强推 main；本次推送被拒后执行 `git pull --rebase origin main`，保留了审查提交，后续每项开始前同步远端。[CI 通过](https://github.com/LFenX/lg-monitor/actions/runs/42176984909)。

### [x] R1-02 进程采集是 O(n²)，一次要 4～20 秒（P0，和 R1-03 一起改）

**证据**

| 机器 / 方式 | 进程数 | 一次快照耗时 |
|---|---|---|
| 你那台，执行记录里的数字 | — | 20393 ms |
| 本机，integration 测试 | 566 | 4433 ms |
| 本机：当前的 8 个调用 | 566 | 8018 ms |
| 本机：只去掉 `NumThreadsWithContext` | 564 | 103 ms |
| 本机：只用 `NtQuerySystemInformation` 取核心字段 | 564 | 18 ms |
| 本机：在核心字段之外，再为每个进程读 exe 路径 | 564 | 再加 23 ms |
| 本机：在核心字段之外，再为每个进程读命令行 | 564 | 再加 10 ms |

**根因**

gopsutil v4.26.10 的 `NumThreadsWithContext`（`process/process_windows.go:595`）每调用一次，都会通过 `getFromSnapProcess`（同文件 `:887`）重新拍一张全系统的 Toolhelp 快照，然后线性查找。`ProcessesWithContext` 其实已经用一张快照填好了线程数，但它是私有字段，读不到。

**影响**

- PLAN 要求采集器每 2 秒采集一次，现在一次就要好几秒到 20 秒。
- 采集器 CPU 会长期占满，PRD 要求平均低于 1%。
- 结果是“15 秒内告警”达不到。

**改法**（和 R1-03 一起）

- **核心字段**
  - 一次 `windows.NtQuerySystemInformation(windows.SystemProcessInformation, ...)` 取全部进程：PID、父 PID、映像名、线程数、创建时间、用户态和内核态 CPU 时间、工作集（RSS）。
  - `golang.org/x/sys/windows` 已经有 `SYSTEM_PROCESS_INFORMATION`，用法可以参考 `x/sys/windows/svc/security.go`。
  - 缓冲区不够时会返回 `STATUS_INFO_LENGTH_MISMATCH`，按返回的大小加一些余量重试。缓冲区用 `[]uint64` 分配，保证 8 字节对齐。
  - `CreateTime`、`UserTime`、`KernelTime` 都是 100 纳秒为单位；`CreateTime` 从 1601-01-01 UTC 起算，要换算成 UTC 的 Unix 毫秒。
- **exe 路径**：`OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` 加 `QueryFullProcessImageName`。
- **命令行**：继续用 gopsutil 的 `CmdlineWithContext`，或者自己写同等的实现。
- **缓存**
  - exe 和命令行在进程存活期间不变，按（PID，创建时间）缓存，每个新进程只读一次。读取失败的结果也要缓存，避免每 2 秒对几百个“拒绝访问”的进程重试。
  - 进程消失时清掉它的缓存。PID 被复用时，新的身份重新读取。
- **保留**：后端接口还是要保留，方便单元测试用假实现。端口采集继续用 gopsutil，不用改。
- **对 PLAN 的修订**：M0-03 里“真实实现用 gopsutil/process”这一句，由本条修订。

**完成标准**

- 单元测试（假后端）覆盖：
  - FILETIME 换算成 Unix 毫秒；
  - 连续两次快照里，同一身份只读一次命令行和 exe；
  - 进程退出后缓存被清掉；
  - PID 复用后重新读取；
  - 命令行读不到时，`CmdlineUnavailable` 为 true（见 R1-04）。
- integration 测试：
  - 连续调用两次 `Snapshot`，断言第二次（缓存已热）少于 1 秒；
  - 在执行记录里写出本机的进程数和两次的实际耗时。

处理：`d9eab21`（Git 提交定位符，与 R1-03 同一提交）改用 8 字节对齐、按返回大小扩容的 NT 系统快照；核心字段不再逐进程调用 Toolhelp。路径用有限查询句柄读取，命令行保留 gopsutil；详情读取前核对创建时间，按身份缓存成功与失败并在退出/复用时清理。假后端测试覆盖 FILETIME、缓存、退出、复用和 `CmdlineUnavailable`。真实测试冷采集 77.622ms、热采集 33.0918ms（633 个进程，排除 PID 0 后 632 个）；热采集断言小于 1 秒。首轮测试发现 PID 0 也会返回正创建时间，已改为显式排除并复验。完整格式、vet、test、PowerShell 5.1 构建检查通过。接口依据：[Microsoft NT 系统查询](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntquerysysteminformation)及当前 x/sys Windows 结构体。

### [x] R1-03 以普通用户运行时，38% 的进程被整条丢掉（P0，和 R1-02 一起改）

**证据**

本机以非管理员身份运行（`IsElevated() == false`）。566 个进程里，有 215 个调用 gopsutil 的 `CreateTimeWithContext` 失败。按 M0-03 的设计，这些进程会被直接跳过，其中包括 `mysqld.exe`、`tailscaled.exe`、`sshd.exe`、`services.exe` 和 95 个 `svchost.exe`。

改用 `NtQuerySystemInformation` 后，566 个进程全部拿得到创建时间，不需要打开进程句柄。

**影响**

M0 的 `lgmon run` 是以当前用户运行的。凡是以 SYSTEM、NetworkService 或其他账户运行的被监控进程，都会一直被判成“不在”，`process_absent` 规则就会误报。M1 改成以 SYSTEM 运行后情况会好一些，但受保护的进程照样打不开。

**改法**：见 R1-02，创建时间和其他核心字段都从 `SYSTEM_PROCESS_INFORMATION` 取。只有 PID 0（System Idle Process）没有创建时间，可以继续跳过。

**完成标准**

- integration 测试断言：快照里有 `services.exe`，而且它的 `CreatedAt > 0`、`ParentPID > 0`。这条在普通用户下也必须成立。
- 执行记录里写出：以普通用户运行时，跳过的进程数，以及进程总数。

处理：`d9eab21`（与 R1-02 同一提交）系统服务的创建时间、父 PID 和资源字段均来自系统快照，不受详情读取权限影响。integration 断言 `services.exe` 存在且创建时间和父 PID 均大于 0。本机原会话为管理员；不存在可用关联令牌，改用 [CreateRestrictedToken](https://learn.microsoft.com/en-us/windows/win32/api/securitybaseapi/nf-securitybaseapi-createrestrictedtoken) 创建自身的 LUA 子进程令牌，禁用管理员 SID 并移除特权，仅启动测试自身：实际 `IsElevated()==false`，总数 630，跳过 1（仅 PID 0），输出 629；冷采集 58.5567ms、热采集 33.1004ms，服务身份断言通过。标准用户终端可复验 `go test -count=1 -tags integration -v ./internal/collector -run TestProcessSourcePerformanceAndSystemProcesses`。未修改账户、服务或系统配置。

验证：R1-02、R1-03 的提交 `d9eab21` [CI 全部通过](https://github.com/LFenX/lg-monitor/actions/runs/42177618251)，包含真实 Windows 采集集成测试。

### [x] R1-04 目标状态要有 `unknown`，查不清的情况不能报 `down`（P1）

**问题**

现在有两种情况，条件根本没法判断，却会返回 `down`，接上 `process_absent` 规则就会误报。

1. **只配了服务条件的目标**：例如 `examples/targets.yaml` 里的 `demo-service`。M0 不实现服务匹配，但 `match.go` 返回的是 `State: "down"`，原因写的是“服务名匹配将在 M1-07 提供”。
2. **命令行读不到**：进程名对上了，但命令行因为权限不足读不到（R1-03 修好之后，其他账户的进程正是这种情况）。现在返回“进程名匹配，但命令行不匹配”，状态是 `down`。

**改法**

- `collector.Process` 增加 `CmdlineUnavailable bool`（JSON 字段 `cmdlineUnavailable,omitempty`）。零值表示读到了，这样现有的假数据不用改。
- 目标状态定义成常量 `StateUp`、`StateDown`、`StateUnknown`（值为 `up`、`down`、`unknown`），M0-06 的 pipeline 直接复用。
- **判 `unknown` 的情况**：
  - 目标带 `service` 条件（M0 期间都是这样）；
  - 或者没有一个进程完全匹配，但至少有一个进程满足了所有能判断的条件，只是因为命令行读不到而没法确认。
- **原因要告诉用户怎么办**，例如：“进程 postgres.exe 的命令行读取不到（权限不足）：请以管理员身份运行，或者去掉 cmdline 条件”。
- **只要有一个进程完全匹配，就是 `up`。**
- **对 M0-08 的约定**（已写进 PLAN M0-08）：`unknown` 不触发新告警，也不会让已经触发的告警恢复；规则处于 `pending` 时遇到 `unknown`，按条件不满足处理，回到 `ok`。

**完成标准**

`match_test.go` 补充以下用例，并把现有的 `service deferred` 用例改成期望 `unknown`：

- 只配服务条件 → `unknown`；
- 进程名匹配但命令行读不到 → `unknown`，原因里有“权限不足”；
- 一个进程读不到命令行、另一个进程完全匹配 → `up`。

处理：`7f8320d`（Git 提交定位符）新增共享 `collector.StateUp/StateDown/StateUnknown` 常量。服务条件和仅因命令行不可读而无法确认的目标返回 unknown，原因提供权限或配置操作；其他可判断条件失败仍返回 down，存在完整匹配优先返回 up。七组新测试覆盖服务、权限、完整匹配、名称/端口不符及可读空命令行；默认测试与全部提交前检查通过。M0-08 的 unknown 状态约定保留在 PLAN，后续实现时逐项验证。

### [x] R1-05 规则类型和目标不相符，现在静默通过校验（P1）

**问题**

- `port_closed` 规则指向一个没有 `match.port` 的目标；
- `probe_failed` 规则指向一个没有任何 `probes` 的目标。

这两种配置都能通过 `config validate`，但规则永远不会触发，等于悄悄少了一项监控。

**改法**：在 `Validate` 里检查这两种情况，报错带行号，指向规则的 `type`。例如：`rules.yaml 第 4 行：规则 web-port 的类型是 port_closed，但目标 web 没有配置 match.port`。

**完成标准**：`TestConfigurationErrors` 里两种情况各加一个失败用例。

处理：`4d8457f`（Git 提交定位符）校验规则所指目标的实际能力：缺少 `match.port` 时拒绝 port_closed，缺少 probes 时拒绝 probe_failed；错误定位到规则 type。`TestConfigurationErrors` 增加两项失败用例，断言仅有一处错误、文件和第 4 行及中文说明。全部提交前检查通过。上项 R1-04 提交 `7f8320d` [CI 通过](https://github.com/LFenX/lg-monitor/actions/runs/42177878755)。

### [x] R1-06 CPU% 改成整机口径 0～100%（P1）

**问题**

现在单核满载算 100%，8 核机器上一个进程最高可以显示 800%。

- 用户拿它和任务管理器对比时会困惑。任务管理器是按整机算，所有核心都满载才是 100%。
- 也和 PRD C1 的系统 CPU（0～100%）不在同一个尺度上。
- 以后加 CPU 阈值规则、存进数据库之后再改口径，代价会大得多。

**改法**

- 用 CPU 时间差除以时间间隔，再除以逻辑处理器总数。逻辑处理器总数用 `windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)`，并通过注入的方式提供，测试里用假值。
- README 里的口径说明同步更新。

**完成标准**：单元测试，4 个逻辑处理器，1 秒内用了 2 个 CPU 秒，结果是 50%。

处理：`96ff0f5`（Git 提交定位符）后端注入整机逻辑处理器数量；Windows 使用 [GetActiveProcessorCount(ALL_PROCESSOR_GROUPS)](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getactiveprocessorcount)，CPU 时间差按整机容量归一并限制在 0～100%。README 已同步。假后端验证 4 个逻辑处理器、1 秒内消耗 2 CPU 秒时为 50%，并检查计数超出采样容量时不超过 100%；全部提交前检查通过。上项 R1-05 提交 `4d8457f` [CI 通过](https://github.com/LFenX/lg-monitor/actions/runs/42178162148)。

### [x] R1-07 只配了端口的目标：有监听就算 `up`（P2）

**问题**

现在只配 `port` 的目标，要求监听这个端口的 PID 在进程快照里，否则判 `down`。以下几种情况，端口明明在监听，目标却会被判成 `down`：

- 由 http.sys 托管的监听，归属 PID 4（例如 IIS、部分 .NET 服务）；
- 进程快照和端口表不是同一时刻拍的，进程刚启动时会对不上。

**改法**

- 目标只有 `port` 条件（没有 `process`、`cmdline`）时，端口表里有这个端口的监听就算 `up`。
- 进程在快照里，就附带上它的进程信息；不在，就在原因里写明“端口 3001 由 PID 4 监听，进程信息不可用”。
- 有 `process` 或 `cmdline` 条件时，保持现在“端口必须属于这个进程”的规则。

**完成标准**：修改 `TestEmptySnapshotsAndConfigurationIsolation` 里对应的断言，并新增一个用例：PID 4 监听，目标为 `up`。

处理：`65e25a4`（Git 提交定位符）仅端口目标直接信任监听表，进程不在快照时保留监听、返回 up，并说明所属 PID 的进程信息不可用；存在名称或命令行条件时继续要求同一进程完整匹配。更新空快照隔离测试并新增 PID 4 监听用例，全部提交前检查通过。上项 R1-06 提交 `96ff0f5` [CI 通过](https://github.com/LFenX/lg-monitor/actions/runs/42178494718)。

### [x] R1-08 匹配对 Windows 用户更宽容（P2）

1. **命令行正则默认不区分大小写**：Windows 路径本身不区分大小写，`D:\AppMy\GRM` 和 `d:\appmy\grm` 是同一个目录。编译正则时在前面加 `(?i)`，并在 Schema 字段说明和 README 里写明。用户确实需要区分大小写时，可以写 `(?-i)`。
2. **进程名可以省略 `.exe`**：配置的 `process` 没有扩展名时（例如 `node`），也匹配 `node.exe`。

**完成标准**：各加一个用例：大小写不同的命令行能匹配上；`process: node` 能匹配到 `node.exe`。

处理：`725ab5f`（Git 提交定位符）匹配和配置校验均在正则前加 `(?i)`，保留用户 `(?-i)` 的覆盖能力；无扩展名的进程名也匹配对应 `.exe`。README 和生成 Schema 的字段说明已同步。新增省略 exe、正则大小写不同及显式区分大小写三项测试；示例/Schema 同步测试和全部提交前检查通过。上项 R1-07 提交 `65e25a4` [CI 通过](https://github.com/LFenX/lg-monitor/actions/runs/42178891118)。

### [x] R1-09 `config validate` 的输出（P2）

**问题**：现在的输出是这样的：

```
命令执行失败，请运行 lgmon --help 查看用法：rules.yaml 第 5 行：字段必须使用字符串，数字文本请加引号
```

- 配置写错不是用法写错，提示去看 `--help` 会误导用户；
- 有多条错误时，都挤在第一行的冒号后面。

**改法**

- 遇到 `config.Errors`，先输出一行 `配置有 N 处错误：`，然后每处错误占一行，退出码仍为 1。`--help` 的提示只用在参数和用法错误上。
- 增加 `--json` 选项，输出 `{"ok": false, "errors": [{"file": "...", "line": 5, "message": "..."}]}`；校验通过时输出 `{"ok": true, "errors": []}`。
- Agent 和 M1 的 MCP `validate_config` 都会用到这个输出。M0-12 写 `docs/cli.md` 时一并写进去。

**完成标准**：CLI 测试覆盖文本和 JSON 两种输出，各包含多条错误。

处理：`16462f4`（Git 提交定位符）配置错误统一显示总数及逐行详情，JSON 使用 `ok/errors` 且成功时 errors 为 `[]`；已经输出的校验错误不再由 main 重复打印，原始结构化错误链仍保留。参数/用法错误才提示 `--help`，运行错误单独说明。CLI 测试覆盖文本和 JSON 的两处错误、真实入口使用的退出码函数、成功 JSON、参数错误及文件写入失败；全部提交前检查通过。README 已记录结构，M0-12 再纳入 docs/cli.md。上项 R1-08 提交 `725ab5f` [CI 通过](https://github.com/LFenX/lg-monitor/actions/runs/42179136069)。

### [x] R1-10 两处配置提示（P2）

**问题**

1. **时长写成纯数字**：`for: 10` 现在报“字段必须使用字符串，数字文本请加引号”。用户照着加上引号写成 `"10"`，还是错，只是换成另一条报错。`interval`、`timeout` 也有同样的问题。
2. **`.yml` 模板被静默忽略**：`card-templates/` 里扩展名是 `.yml` 的文件会被直接跳过，用户不知道自己的模板没有生效。

**改法**

1. `Duration` 类型的字段遇到数字时，改报“时长缺少单位，请写成 10s 或 5m 这样的格式”。
2. 遇到 `.yml` 文件时报错：“模板文件请使用 .yaml 扩展名：card-templates/x.yml”。监听器也要对 `.yml` 文件的变化做出反应，这样改名之前也能看到这条报错。

**完成标准**：两种情况各加一个失败用例。

处理：`:/^R1-10:`（Git 提交定位符）数字型 for、interval、timeout 明确提示时长单位；模板目录里的 `.yml` 返回含文件名的扩展名错误，监听器同时响应 `.yml`。新增三种数字时长及扩展名失败用例，真实监听测试验证 yml 新建/修改均报错并保留旧配置，改名 yaml 后恢复加载；全部提交前检查与配置 integration 测试通过。上项 R1-09 提交 `16462f4` [CI 通过](https://github.com/LFenX/lg-monitor/actions/runs/42179495058)。

### 给后续任务的提示（不用单独提交）

- **M0-06**
  - `config.Manager` 现在没有变更通知。如果 M0-06 需要在配置变化时重建匹配器和探测调度，就在那时给 Manager 加订阅（例如 `Subscribe() <-chan *Config`），不要提前做。
  - 采集循环要保证：上一轮还没结束时，不会开始新一轮（`time.Ticker` 会自动丢掉错过的 tick，但要有测试覆盖）。
- **M0-08**：`unknown` 的处理方式见 R1-04。
