# Windows 安装包

安装包为 `lgmon-<版本>-windows-amd64-setup.exe`，内含一个嵌入网页的 lgmon.exe，适用于 Windows 10/11 x64。用户电脑不需要 Go、Node、Inno Setup、Docker 或外部数据库。构建记录与 exe 同名，扩展名为 json，包含源代码版本、平台和程序/安装包 SHA-256。

真实安装、UAC、LocalSystem、重启与双机验收保持 **Pending**。本轮工程使用临时目录、测试配对和假服务控制器，没有注册或操作本机真实服务。

## 使用向导

双击安装包后选择角色：

| 角色 | 用途 |
|---|---|
| both（默认） | 采集本机，并在本机查看面板和告警；启动时走已有本机配对协议 |
| monitor | 汇总采集器数据和发送告警；以后用 `lgmon pair new` 生成配对码 |
| collector | 只采集本机，上报另一台监控器 |

程序目录固定为 `%ProgramFiles%\lg-monitor`，不能用 `/DIR` 改到其他位置。数据默认 `%ProgramData%\lg-monitor`，向导允许选择已有的 lgmon home。既有程序目录和 home 的所有文件/目录必须具有受信任所有者，且普通用户不能写入；不符合时拒绝复用，不能靠安装时改 ACL 把未知来源的配置或脚本变成 SYSTEM 输入。已有可信配置、身份和数据会保留，只创建缺失的最小配置；原为关闭的远程操作仍关闭。

collector 填写监控器 host:port、一次性配对码和通过可信方式核对的 16/64 位 CA 指纹。选择复用时，这个 home 必须已持有属于同一地址的有效本机身份；安装器不会把身份覆盖到另一台监控器。首次配对失败不注册服务。配对码通过临时受保护文件传递，原位写入已经收紧所有者/ACL 的占位文件，读取后删除，不进入进程参数或日志。

Ready 页会列出角色、程序/数据目录和接收/上报地址。点击“安装”即同意注册并启动本产品的 LocalSystem 自动启动服务；只有实查为 Running 且回执已保存才报告成功。安装器不修改防火墙、不注册用户登录启动、不重启电脑、不关闭其他程序。

monitor/both 可在完成页选择打开面板。快捷方式地址来自实际 settings 的 HTTP 监听配置。默认本机会话只有 read；配置权限继续通过本人现有的令牌或 `tray setup` 授权获得，不自动发放 admin 会话。飞书 Secret 仍由本人在接入向导中填写。

## 本机 CLI

下面命令会操作真实的本产品服务，由本人另行决定执行；开发验证没有运行它们。

```powershell
& "$env:ProgramFiles\lg-monitor\lgmon.exe" setup check --home "$env:ProgramData\lg-monitor" --role both --json
& "$env:ProgramFiles\lg-monitor\lgmon.exe" setup install --home "$env:ProgramData\lg-monitor" --role both --confirm --json
& "$env:ProgramFiles\lg-monitor\lgmon.exe" setup remove --home "$env:ProgramData\lg-monitor" --confirm --json
```

`setup check` 只读检查服务、目标程序与现有配置；`--service-absent` 仅核对无服务，`--panel-url` 只返回面板地址，`--require-paired` 要求 collector 已配对。安装/卸载与向导共用互斥，不能并行覆盖或删除程序。`setup remove` 在程序路径和 home 不匹配时拒绝停止服务；服务不存在时为幂等成功，数据不删除。

collector 的 install 另需 `--monitor <host:port>`。配对输入通过 `--pair-stdin` 的 JSON 或 `--pair-input <绝对路径>` 提供，结构仅为 `{"code":"虚构配对码","fingerprint":"0123456789abcdef"}`。不得把真实码放进命令参数或脚本历史。用 `setup inbox --directory <新的绝对临时目录>` 创建受保护目录和空 pair.json，再原位写入该文件；没有 --confirm 不读取/消耗输入。额外字段、多段 JSON、超出 4096 字节、不安全 ACL 或路径均拒绝。

已有命令 `lgmon install/uninstall/service` 保持原行为；setup 提供初始化、配对、绑定校验与失败恢复的统一编排。服务权限与真实验收见 [Windows 服务](windows-service.md)。

## 卸载、重装与失败

安装包复制前发现已有 lgmon 服务或目标程序时会拒绝覆盖。升级流程为：备份完整 home；使用原卸载程序卸载；重新安装并选择相同 home/角色，collector 复用已有身份。没有原位替换运行程序的隐藏步骤。

卸载的实际服务移除发生在用户确认之后、文件删除之前。绑定不匹配、停止/移除失败或仍检测到服务时，终止文件删除。只删除安装器登记的程序文件，全部 home 保留，包括 `data/setup/installation.json` 安装回执。回执只有角色、规范路径、地址、版本和 UTC 安装时间，没有配对码或令牌。

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
