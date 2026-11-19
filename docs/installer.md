# Windows 安装包

给人用的是 `lgmon-setup-<版本>.exe`（U8）：一个 lgmon 自己的安装窗口，里面带着下面说的 Inno Setup 安装包 `lgmon-<版本>-windows-amd64-setup.exe`，安装和升级的规则都由后者执行。从 0.13.0 起发布页只放 `lgmon-setup-<版本>.exe`、构建记录 json 和 SHA256SUMS：以前同时发布的 Inno 安装包名字更像“安装程序”，下载它的人看到的是经典向导，而不是 lgmon 的安装窗口。电脑没有 WebView2 运行库、或者 WebView2 启动失败时，lgmon-setup 自动改用 Inno 向导完成安装（同样是 lgmon 的配色和图标）。后者内含一个嵌入网页的 lgmon.exe，适用于 Windows 10/11 x64。用户电脑不需要 Go、Node、Inno Setup、Docker 或外部数据库。构建记录与 exe 同名，扩展名为 json，包含源代码版本、平台和程序/安装包 SHA-256。

真实安装、UAC、LocalSystem、重启与双机验收保持 **Pending**。本轮工程使用临时目录、测试配对和假服务控制器，没有注册或操作本机真实服务。

## lgmon-setup：三个画面的安装器（U8）

双击 `lgmon-setup-<版本>.exe`：

- 欢迎：lgmon 的标识、“安装 lgmon”、一句话说明和黑色的“安装”按钮；下面一行写安装位置，“自定义”可以选这台电脑做什么（采集并显示看板〔推荐，即 both〕/ 只做监控器 / 只采集）、数据目录和接收采集器的地址。选“只采集”时打开下面的 Inno 向导填写配对码和指纹。
- 进度：点安装后 Windows 弹出一次管理员授权；安装包以 `/VERYSILENT` 运行并写日志，窗口按日志里的阶段（准备、检查这台电脑、复制文件、注册并启动服务）推进进度条。拒绝授权或失败时说明原因，可以重试或查看日志。
- 完成：“lgmon 已经装好”，“打开 lgmon”以本人身份打开小窗口。

已经装过时，旧版本显示“更新 lgmon”（从几版更新到几版，沿用已装的角色、数据目录和地址，配置和数据都保留）；同版本或更新的版本显示“lgmon 已经装好”和“打开 lgmon”。`lgmon-setup --update` 不显示欢迎画面，直接更新并在完成后重新打开 lgmon（应用内更新用，见 [Web 面板](web-panel.md)）。`--silent`、`--engine <安装包>`、`--role`、`--home`、`--listen`、`--monitor` 供自动化和 CI 用。电脑上没有 WebView2 运行时时直接运行 Inno 向导。

发布：推送 `v<版本>` 标签（例如 `v0.9.0`）时，`.github/workflows/release.yml` 用这个版本号测试、构建并验证两个安装包，生成 SHA256SUMS，创建 GitHub Release（附 lgmon-setup、Inno 安装包、构建记录和 SHA256SUMS，说明自动生成）。已装的 lgmon 小窗口读的就是这里的最新发布，见 [Web 面板](web-panel.md)。

原位更新：安装包使用 `/LGMONUPGRADE`。它先检查（`setup check --upgrade` 只接受属于本程序目录和这个 home 的现有服务，别的服务一律拒绝），再将旧 `lgmon.exe` 在同一程序目录内改为唯一的 `lgmon.exe.previous-<时间>-<序号>`。这一步在停止旧服务之前完成，移动失败就中止，旧服务继续运行。随后用改名后的原版本执行 `setup remove`，关闭 lgmon 自己的窗口程序，再写入新程序、注册并启动服务。停止旧服务失败时还原原程序名；home 的配置、身份和数据保留。没有这个开关时，发现已有服务仍然拒绝覆盖。

从 0.11.0 起，Agent 的 stdio MCP 进程可以在更新时保持连接：Windows 不允许覆盖它正在运行的映像，但允许在同卷改名后保留原映像和标准输入/输出管道。安装器不结束 MCP 或 Agent 进程，不安排重启替换；仍在使用的旧文件留在程序目录，空闲的在安装结束或下次更新时清理，卸载时也纳入清理。HTTP API 在服务重启期间短时不可用，恢复后原连接继续调用。现有 stdio 进程使用其启动版本的工具集；需要新版本增加的 MCP 工具时，让 Agent 重新连接一次。HTTP MCP 的工具集随监控器更新。

`test-installer.ps1` 的连接回归使用临时目录内的真实 stdio MCP 进程、隔离 HTTP API 和假服务控制器，覆盖两个不同代际的 Agent 连接连续更新、更新前后代理调用、预检/文件占用/停止服务失败时保留旧安装、断开后回收旧映像和 home 文件哈希保留。夹具不结束本机真实窗口或服务。

卸载时，辅助命令从 Inno 私有临时副本运行，避免检查命令自身继续占用即将删除的程序。改名、还原和卸载删除对短暂共享锁最多重试五秒，持续占用会明确报错；不会把程序仍在的状态报告成完整卸载成功。从 0.13.1 起，服务移除后、删除文件前，卸载程序结束所有仍从程序目录运行的 lgmon 程序（lgmon 窗口、Agent 的 stdio MCP 会话和改名保留的旧映像，按完整路径识别，不碰其他位置同名的程序），再删除 lgmon.exe 和 lgmon-window.exe。以前窗口或 MCP 会话还开着时，Windows 只能把这些文件安排到下次重启再删，之后每次重装都会停在“先前的程序安装或卸载未完成，您需要重启您的电脑”，直到重启。卸载结束后还会删除 lgmon 窗口的浏览器数据（本人的 `%LOCALAPPDATA%\lg-monitor\window`，以及旧版本服务误建在系统账户下的 `C:\Windows\System32\config\systemprofile\AppData\Local\lg-monitor`）。

## 使用向导（Inno Setup）

直接双击 Inno 安装包后选择角色：

| 角色 | 用途 |
|---|---|
| both（默认） | 采集本机，并在本机查看面板和告警；启动时走已有本机配对协议 |
| monitor | 汇总采集器数据和发送告警；以后用 `lgmon pair new` 生成配对码 |
| collector | 只采集本机，上报另一台监控器 |

程序目录固定为 `%ProgramFiles%\lg-monitor`，不能用 `/DIR` 改到其他位置。数据默认 `%ProgramData%\lg-monitor`，向导允许选择已有的 lgmon home。既有程序目录和 home 的所有文件/目录必须具有受信任所有者，且普通用户不能写入；不符合时拒绝复用，不能靠安装时改 ACL 把未知来源的配置或脚本变成 SYSTEM 输入。已有可信配置、身份和数据会保留，只创建缺失的最小配置；原为关闭的远程操作仍关闭。

collector 填写监控器 host:port、一次性配对码和通过可信方式核对的 16/64 位 CA 指纹。选择复用时，这个 home 必须已持有属于同一地址的有效本机身份；安装器不会把身份覆盖到另一台监控器。首次配对失败不注册服务。配对码通过临时受保护文件传递，原位写入已经收紧所有者/ACL 的占位文件，读取后删除，不进入进程参数或日志。

Ready 页会列出角色、程序/数据目录和接收/上报地址。点击“安装”即同意注册并启动本产品的 LocalSystem 自动启动服务；只有实查为 Running 且回执已保存才报告成功。安装器不修改防火墙、不注册用户登录启动、不重启电脑、不关闭其他程序；内网共享需要的入站规则在本人打开“内网可见”时由 lgmon 添加，见 [内网共享](teams.md#网络和安全)。

向导使用 lgmon 自己的配图和图标：构建时由 `scripts/installer-art.ps1` 用 lgmon 的标识画出向导大图、页眉小图（100%/150%/200% 缩放各一份）和多尺寸图标，放在系统临时目录，编译后删除，仓库里不放大图。图标随程序装到程序目录，用于开始菜单和“应用和功能”。

构建时 bin 里有 lgmon-window.exe（lgmon 自己的窗口，U7）就一起装到程序目录；没有时 lgmon 退回 Edge 应用窗口。monitor/both 装完后，完成页默认勾选“打开 lgmon”：以本人身份运行 lgmon.exe（不带命令），打开 lgmon 的小窗口，里面有服务状况、复制给 Agent 的一段话和“打开看板”（见 [命令行](cli.md#打开-lgmonu6)）；另有“在浏览器里打开看板”可选。开始菜单放两项：“lgmon”（同一个小窗口，带上安装时选的数据目录）和“lgmon 看板”（浏览器打开看板）。看板地址来自实际 settings 的 HTTP 监听配置。默认本机会话只有 read；配置权限继续通过本人现有的令牌或 `tray setup` 授权获得，不自动发放 admin 会话。飞书 Secret 仍由本人在接入向导中填写。

## 本机 CLI

下面命令会操作真实的本产品服务，由本人另行决定执行；开发验证没有运行它们。

```powershell
& "$env:ProgramFiles\lg-monitor\lgmon.exe" setup check --home "$env:ProgramData\lg-monitor" --role both --json
& "$env:ProgramFiles\lg-monitor\lgmon.exe" setup install --home "$env:ProgramData\lg-monitor" --role both --confirm --json
& "$env:ProgramFiles\lg-monitor\lgmon.exe" setup remove --home "$env:ProgramData\lg-monitor" --confirm --json
```

`setup check` 只读检查服务、目标程序与现有配置；`--service-absent` 仅核对无服务，`--panel-url` 只返回面板地址，`--require-paired` 要求 collector 已配对，`--upgrade` 允许属于本程序目录和 home 的现有服务通过（原位更新用，随后由安装包先移除它）。安装/卸载与向导共用互斥，不能并行覆盖或删除程序。`setup remove` 在程序路径和 home 不匹配时拒绝停止服务；服务不存在时为幂等成功，数据不删除。

collector 的 install 另需 `--monitor <host:port>`。配对输入通过 `--pair-stdin` 的 JSON 或 `--pair-input <绝对路径>` 提供，结构仅为 `{"code":"虚构配对码","fingerprint":"0123456789abcdef"}`。不得把真实码放进命令参数或脚本历史。用 `setup inbox --directory <新的绝对临时目录>` 创建受保护目录和空 pair.json，再原位写入该文件；没有 --confirm 不读取/消耗输入。额外字段、多段 JSON、超出 4096 字节、不安全 ACL 或路径均拒绝。

已有命令 `lgmon install/uninstall/service` 保持原行为；setup 提供初始化、配对、绑定校验与失败恢复的统一编排。服务权限与真实验收见 [Windows 服务](windows-service.md)。

## 卸载、重装与失败

安装包复制前发现已有 lgmon 服务时会拒绝覆盖。升级用 lgmon-setup（或安装包加 `/LGMONUPGRADE`）：先按卸载的方式移除旧服务、保留 home，再重新安装相同 home/角色，collector 复用已有身份。手动做法仍然可用：备份完整 home；使用原卸载程序卸载；重新安装并选择相同 home/角色。

卸载的实际服务移除发生在用户确认之后、文件删除之前。绑定不匹配、停止/移除失败或仍检测到服务时，终止文件删除。默认只删除安装器登记的程序文件，全部 home 保留，包括 `data/setup/installation.json` 安装回执。交互卸载时会问“要同时删除 lgmon 的配置和监控数据吗？”（默认“否”）；选“是”只删除 home 里 lgmon 自己的 config、data、logs、scripts 四个目录，home 里有别的文件时目录本身保留。静默卸载默认保留，加 `/LGMONPURGE` 才删除。回执只有角色、规范路径、地址、版本和 UTC 安装时间，没有配对码或令牌。

程序目录中的 setup.ini 使用带 BOM 的 UTF-16LE，避免中文或 emoji 路径在其他系统代码页下受损；卸载继续按原始路径核对服务绑定，不放宽匹配。原因见 [Microsoft 的 INI 编码说明](https://devblogs.microsoft.com/oldnewthing/20240606-00/?p=109861)。

注册成功后启动/保存回执失败，只回滚本次创建且绑定仍匹配的服务；配置和已经生成的有效配对身份保留。回滚也失败时明确报错，请保留程序、home 和安装日志，先查询 `service status`，再由本人处理。文件已经放入但安装失败时，安装器不会显示成功；使用本次卸载程序移除程序后可重试，不删除数据来绕过失败。

## 开发构建

```powershell
$tool = .\scripts\get-inno.ps1 | ConvertFrom-Json
try {
    .\scripts\build-installer.ps1 -Version dev -CompilerPath $tool.compiler
    .\scripts\test-installer.ps1 -CompilerPath $tool.compiler
}
finally {
    if ($tool.owned) { .\scripts\get-inno.ps1 -Directory $tool.directory -Remove }
}
```

`build-installer.ps1` 先复用 build.ps1；传入 `-SkipBuild` 时只打包已经通过版本/Go 构建信息/PE 架构检查的载荷。正式打包禁止 installerfixture 标签，要求 Windows amd64、CGO_ENABLED=0 和 webui；编译工具不随用户包分发。正式 exe 当前没有代码签名证书，构建记录如实记录 Authenticode 状态，不伪造发布者签名。

get-inno 使用 [官方 6.7.4 发布](https://github.com/jrsoftware/issrc/releases/tag/is-6_7_4) 的固定 SHA-256 和有效发行者签名。已有兼容的本账户安装会复用；否则用标准安装器在系统临时目录部署，明确关闭便携模式、快捷方式和文件关联，按归属记录回收，不覆盖其他版本。也可自行安装编译器并显式传入路径。

中文语言文件来自同一版本上游的 [社区简体中文翻译](https://github.com/jrsoftware/issrc/blob/is-6_7_4/Files/Languages/Unofficial/ChineseSimplified.isl)，保留原有署名，仅清理一处注释的行末空格；上游 Git blob 为 d6a11c4490de07dad443ade668289fc954dfa1ed。打包器和许可见 [Inno Setup 源码](https://github.com/jrsoftware/issrc/tree/is-6_7_4)。

CI 不安装真实服务，运行不同 AppId、仅作用于临时目录的同源 Inno 夹具，并上传该 run 的正式 exe/json；不自动创建 GitHub Release。夹具只有 setup/version，服务操作为独立文件状态，不能调用真实 install/service/run 命令，也不能越出编译期绑定目录。

## 验证状态

已有工程证据：[安装器夹具结果](m2-09-installer-results.json)，包括三角色、重复安装不覆盖、缺少确认、配对失败不注册服务、启动/回执失败回滚、移除失败/绑定不匹配保留程序、卸载 home 字节不变，以及日志没有配对码。九组用例已在 Windows PowerShell 5.1 通过。Go 原生集成使用真实临时 mTLS 监控器验证错误指纹零服务效果、正确配对和重装保持身份。

四项必需门禁、完整 integration webui 原生回归、嵌入网页回归及 PowerShell 5.1 内联打包/九组夹具均已通过；自有临时编译工具已经通过归属校验回收。最终 CI 以本次提交对应的 GitHub Actions run 为准。Computer Use 打开测试向导时两次返回 `GetCursorPos failed: 拒绝访问 (0x80070005)`，没有获得页面截图，不据此推断桌面锁屏，也不声称视觉通过。按用户“人工验收先保留 Pending，不要卡住开发”的要求，视觉验收保留 Pending，工程检查继续。

人工视觉检查无需安装真实服务：运行 test-installer.ps1 后，在输出的临时目录进入 confirmation，打开其中的 `lgmon-installer-fixture-*.exe`；检查欢迎页、默认 both、三角色选择、数据目录、监听地址及 collector 配对表单；前进到 Ready，核对角色、目录、地址和服务影响说明。返回检查另外两个角色，最后点击“取消”，不要点击“安装”。记录中文是否完整、控件是否重叠、配对码是否遮蔽、复用选项和 Ready 内容是否一致。此夹具不同 AppId，只允许编译期绑定的临时目录，服务为文件状态，不操作真实 SCM。

真实 UAC、Program Files/ProgramData ACL、LocalSystem、重启、双机和人工视觉验收继续 Pending。本人验收时只使用隔离的 lgmon 与演示目标，不能用 LFEN、lgplayblog、Nebula、PostgreSQL、cloudflared、Tailscale 或系统服务做故障注入。

## 内测授权（U15）

发布流程读取仓库变量 `LGMON_LICENSE_MODE`：设为 `enforce` 时打出的版本要求内测密钥，否则（包括 0.13.2）照常使用。要求密钥的版本第一次打开时显示“输入内测密钥”的页面，激活后这台电脑记住它，每 30 分钟和授权服务核对一次；密钥到期、被收回或这台电脑被移除后，网页、API、MCP 和波西米亚只回“需要内测密钥”，采集和告警照常进行。三击左上角（或小窗口里）的 lgmon 标志打开隐藏的“内测管理”：管理员登录后生成密钥（名字、有效期、可用电脑数，密钥只显示一次）、延长、收回和恢复、移除单台电脑，以及把自己的电脑设为管理员设备。授权服务跑在 Cloudflare Worker 上，代码和部署步骤在私密仓库 LFenX/lgmon-license；lgmon 里只有验签用的公钥。
