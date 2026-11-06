# M0 原型验收

状态：工程检查已准备，真实应用流程、群收件和截图待用户执行。没有记录为通过的项目不能据此勾选 PLAN 的 M0-13。

## A. 飞书接入与四套模板

在仓库根目录使用 Windows PowerShell 5.1：

```powershell
.\scripts\build.ps1
.\bin\lgmon.exe feishu setup --home .\dev-home
```

1. 使用飞书自建应用。App ID 和 App Secret 由本人在向导输入；Secret 由 DPAPI 加密保存，不要粘贴到对话、命令参数、截图或 Git。
2. 按向导打开开发者后台并启用机器人能力，再打开一键权限链接。按实际页面申请应用身份权限并处理管理员审批；若页面要求发布版本，完成版本发布后返回向导。向导每 5 秒检查一次，60 秒后仍未生效会提示发布和审批。**实际按钮名称、发布顺序尚待本次记录，不将此段当作已经验证的飞书界面流程。**
3. 由本人在飞书新建专用测试群，将该应用的机器人加入群。向导每 3 秒检查并自动发现新群。选中这个测试群后才同意发送；程序不会自行建群。确认向导显示的群名和实际群相同。
4. 选择模板并核对群内测试卡片后保存。记录保存后的通知渠道名（下面以 `feishu-alerts` 为例；使用向导实际保存的名称）。中断后用同一 home 和应用配置续跑，已经成功发送的向导测试不会重复发送。
5. 再发送四套预设测试卡片。下面命令会实际发消息，只对已经确认的测试群执行：

```powershell
.\bin\lgmon.exe feishu test --home .\dev-home --notifier feishu-alerts --template standard
.\bin\lgmon.exe feishu test --home .\dev-home --notifier feishu-alerts --template compact
.\bin\lgmon.exe feishu test --home .\dev-home --notifier feishu-alerts --template detailed
.\bin\lgmon.exe feishu test --home .\dev-home --notifier feishu-alerts --template digest
```

| 模板 | 人工核对 | 截图路径（核对后保存） | 当前结果 |
| --- | --- | --- | --- |
| standard | 标题、颜色、字段和测试消息标记 | `docs/images/m0-standard.png` | 待验 |
| compact | 两行内容、测试消息标记 | `docs/images/m0-compact.png` | 待验 |
| detailed | 进程信息、日志折叠、排查内容、测试消息标记 | `docs/images/m0-detailed.png` | 待验 |
| digest | 汇总标题、列表、测试消息标记 | `docs/images/m0-digest.png` | 待验 |

截图只截卡片，遮去真实 App ID、chat ID、open_id 和凭据；新建 `docs/images/` 后保存。预览 JSON 和服务端返回消息 ID 不能代替真实显示验收。若 detailed 被服务器拒绝或显示异常，保留安全的错误码和现象后返修，不填写通过。

请回填以下事实，再据此更新 M0-11 的提示文字：机器人启用路径；权限页实际按钮；是否需要管理员审批；是否需要发布版本及发布步骤；向导发现测试群的结果；四张卡片显示结果。不要把凭据或真实群标识写进版本控制。

## B. 触发与恢复

先执行只读检查（不会读取密钥或发送消息）：

```powershell
.\scripts\m0-acceptance.ps1 -SourceHome .\dev-home -Notifier feishu-alerts -CheckOnly
```

本人确认所选渠道就是测试群后执行：

```powershell
.\scripts\m0-acceptance.ps1 -SourceHome .\dev-home -Notifier feishu-alerts -SendConfirmed
```

验证两个独立 lgmon 进程时，在上述命令加 `-SplitRoles`。脚本使用不同的临时 home 和空闲回环端口，先启动 monitor，生成临时配对码并使用本机取得的完整指纹配对这个自建采集器，再启动 collector；只把 targets.yaml 复制给 collector，飞书配置与密钥保留在 monitor，collector 自己保存 DPAPI 加密的 TLS 私钥。配对码不打印。其余 5 秒规则、15 秒触发/恢复成功审计、人工收件和清理标准相同。本脚本只测试自建本机资源；跨机操作另按 M1 验收，自动下发配置在 M1-04。单进程 both 同样使用空闲回环端口和 mTLS。

脚本构建临时 Go HTTP 演示程序，在系统临时目录建立独立 home。仅复制所选渠道和对应应用配置，密钥通过本机 DPAPI 密钥库重新加密并设置 ACL；源 home 保持不变。目标同时按演示程序名称、随机命令行标记和监听端口匹配，规则为 process_absent、持续 5 秒、detailed 模板。

脚本启动自己的演示服务和 lgmon，等待 up，终止自己的演示服务后开始计时；15 秒内要求 firing 和该状态、该渠道的成功 notifications 审计。重启同一演示服务后，15 秒内要求同一告警 resolved 及对应成功审计。不会用历史 lastNotifiedAt 代替当前状态的发送证据。每步打印耗时；最终只清理自己创建的进程对象和经绝对路径校验的临时目录，不修改真实服务、防火墙、开机项或源配置。临时监控进程在审计完成后终止，优雅退出另由运行集成测试验证。

需要 Go 标准安装与已构建的 lgmon。选取空闲端口后若被其他程序抢占，脚本报告失败并清理，可重新执行。网络或权限失败也按失败报告，不将成功退出码冒充收件成功。

## 验收记录

| 项目 | 通过条件 | 当前结果 |
| --- | --- | --- |
| 接入流程 | 本人完成凭据、机器人、权限和群发现，并回填实际页面步骤 | 待用户执行 |
| 四套卡片 | 四张真实卡片显示正确，截图已保存 | 待用户执行 |
| 自动链路 | 脚本 firing/resolved 和两次成功审计均在时限内 | 待确认测试群后执行 |
| 群内收件 | 本人确认同一告警触发、恢复两张卡片和时间 | 待用户确认 |

本地假客户端测试只验证链路和审计逻辑，不证明真实飞书收件。A、B 和人工收件全部满足后才能关闭 M0-13；期间按 AGENTS 的人工待验约定继续不依赖真实收件的任务。
