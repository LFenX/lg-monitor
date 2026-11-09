# 团队共享面板

负责人把声明式面板的全部或部分卡片分享给团队或指定设备成员，并限定主机、目标。成员直接读取负责人监控器上的实时结果，只能看获授权的部分。HTML 面板不支持分享。

## 开始使用

没有自建或加入团队时，日常面板不显示团队分组，成员 HTTPS 端口也不打开。首次负责人通过 CLI 建团队；随后在“面板 → 团队共享 → 团队与分享”管理。新成员可直接打开自己的本机面板 `/#/teams/join`，粘贴完整邀请，或使用 CLI。加入、管理和修改地址需要 admin；只查看已加入团队及卡片需要 read。

CLI 连接这个 home 中已运行的 monitor/both，使用既有受保护的本机管理证书，不生成临时 HTTP admin 令牌。证书目录与密钥库的访问权限继续生效。以下只是示例命令，路径与名称均为开发示例；不安装服务、注册开机启动或改防火墙。

负责人：

```powershell
$ownerHome = '.\dev-home\team-owner'
$team = .\bin\lgmon.exe team create '示例团队' --owner '示例负责人' --home $ownerHome --json | ConvertFrom-Json
$invite = .\bin\lgmon.exe team invite $team.id --name '示例成员设备' --home $ownerHome --json | ConvertFrom-Json
```

把完整邀请通过可信渠道交给指定设备的使用者，并另行核对显示的 16 位 CA 指纹。邀请默认 24 小时有效、只用一次，负责人库里只存 SHA-256 摘要。网页只在本次生成时显示邀请；不要把真实邀请放进命令参数、仓库、截图或日志。

成员在自己的软件中先预览并核对，再加入。以下 `$invite` 表示已通过可信渠道取得的邀请对象，不由程序替用户发送：

```powershell
$memberHome = '.\dev-home\team-member'
$invite.invitation | .\bin\lgmon.exe team join --invite-stdin --preview --home $memberHome --json
$invite.invitation | .\bin\lgmon.exe team join --invite-stdin --fingerprint $invite.fingerprint --home $memberHome --json
.\bin\lgmon.exe team joined --home $memberHome --json
```

网页加入也分为“检查地址与指纹”和“确认指纹并加入”，核对复选框未选中时不能提交。设备生成 P-256 私钥，仅保存在本机受保护密钥库。先取得公开 CA 并核对指纹，第二次使用已固定 CA、校验主机名的 TLS 连接发送邀请码和 CSR。不会把邀请码发给未核对的服务器。

同一个 CA/团队不重复保存本机加入身份；不同团队和负责人分别保存身份。换设备须重新邀请。负责人地址变化时可修改本机加入记录的 HTTPS 地址，CA pin、证书和成员 ID 保持。

## 分享范围

负责人选团队、声明式面板、卡片、主机、目标和成员，界面在保存前显示实际授权范围。卡片可选整页或具体卡片；整页分享会随有效面板配置增加新卡片，指定列表只保留原先选定且仍存在的卡片。配置有错误时不继续对外返回旧定义。

collectors 使用采集器 ID，targets 使用目标 ID。列表内部满足任一项，不同维度同时满足。API 省略某维度表示不限制；显式空列表、null、重复项或通配符报错。members 省略为全团队，包括后续加入的成员；指定列表只接受该团队未吊销的成员。网页选择“指定”但没有勾选任何项目时不发送保存请求。

CLI 创建时省略筛选表示整页、全部数据、全团队。更新时未指定的字段保持原样；只有显式 `--all-collectors`、`--all-targets`、`--all-panels`、`--all-members` 才解除相应限制。更新携带读取到的 revision，冲突须刷新后重试。

| 命令（均支持 --json、--home） | 行为 |
|---|---|
| `lgmon team create <名称> --owner <显示名>` | 建立本机团队 |
| `lgmon team list` / `lgmon team members <团队ID>` | 列出团队、成员设备及访问记录 |
| `lgmon team invite <团队ID> --name <设备显示名> [--ttl 24h]` | 生成邀请，期限 1m～7d |
| `lgmon team remove <团队ID> <成员ID>` | 吊销成员设备并断开连接 |
| `lgmon share create <团队ID> --dashboard <页面ID>` | 可附 --panels/--collectors/--targets/--members，值为逗号分隔的 ID |
| `lgmon share list [团队ID]` | 查看已有分享与范围 |
| `lgmon share update <分享ID>` | 修改指定字段；--all-* 明确解除限制 |
| `lgmon share revoke <分享ID>` | 撤销分享 |
| `lgmon team join --invite-stdin --preview` | 不消耗邀请，预览地址与公开指纹 |
| `lgmon team join --invite-stdin --fingerprint <指纹>` | 核对后加入 |
| `lgmon team joined` / `lgmon team shared <本机加入ID>` | 本机加入记录、实时获授权分享 |
| `lgmon team view <本机加入ID> <分享ID> [卡片ID]` | 读取定义或结果，具体卡片支持 --range/--from/--to/--collector，不接受 SQL |
| `lgmon team address <本机加入ID> --address <HTTPS地址>` | 修改地址并保留原身份 |
| `lgmon team leave <本机加入ID>` | 仅移除本机加入记录；真正吊销仍需负责人移除成员 |

成员界面按负责人和团队分组，复用本地的声明式卡片渲染，支持相对/固定时间、分享范围内的主机选择及深浅色、手机布局。没有编辑、复制成本地面板或再次分享的操作。读取失败时清空旧结果并显示原因，不用旧结果充当在线数据。

## 连接和权限

`settings.teamListen` 默认 `127.0.0.1:7445`，只有本机自建团队后才打开。远程访问时由本人选择 Tailscale 监听和 `teamPublicUrl`，例如以下虚构地址：

```yaml
teamListen: 100.64.0.4:7445
teamPublicUrl: https://100.64.0.4:7445
```

换地址时先成功绑定新监听才关闭旧监听；失败保留旧监听。成员服务证书由原 CA 签发并覆盖当前主机/配置地址，CA 和成员身份不替换。程序不自动修改防火墙或系统证书信任。

独立端口只提供公开 `/team/v1/authority`、配对 `POST /team/v1/join`，以及需要成员 mTLS 的 `GET /team/v1/shares`、`GET /team/v1/shares/{id}`、`GET /team/v1/shares/{id}/panels/{panel}`。无成员证书不能读取；采集器、管理证书不充当成员，成员证书也不充当采集器或负责人 API 凭据。此端口不提供 `/api/v1`。

负责人在自己的 `/api/v1/teams`、`shares` 管理，全部 admin；本机 `team-joins` 负责保存设备身份与代理读取。完整路径和模型见 OpenAPI。CLI 的 TeamAdmin RPC 只接受这些团队操作，使用管理证书，分块返回，保留采集协议原有四 MiB 帧预算。MCP 提供同权限团队/分享工具，管理与本机加入/离开为 admin，共享读取为 read，详见 [MCP](mcp.md)。须先向本人展示卡片/主机/目标/成员范围并取得明确意图；不能自行扩大分享、替本人核对指纹或发送邀请。

## 数据隔离与撤销

分享查询由负责人按保存的卡片定义执行。成员只能提交 range/from/to/collector，不能提交 SQL。可读表和视图逐一登记过滤列，查询通过同名 CTE 和 SQLite 授权回调限制行；视图单独加过滤，禁止 main./temp. 和保留对象名遮蔽。缺少某过滤维度的数据不能读取。选主机还会额外收紧行范围，不依赖卡片作者使用 `:collector`。

每张卡片查询最多 5 秒、10000 行、16 MiB；每成员对同一 dashboard/panel 十秒限一次，换另一分享不能绕过。并发有界。返回结果没有 SQL、源文件或内部错误正文；成员端只在内存里使用结果，auth.db 只保存团队/身份/分享等元数据。访问审计只记录时间和次数。

撤销、缩小范围和吊销会取消在途读取并断开相关连接；响应前再次检查成员、分享 revision 和当前面板版本。下一次页面刷新失去权限后，清空已显示结果。移除本机加入记录不等于负责人吊销；密钥库中未被引用的受保护旧密钥不会自动授予访问权。

## 工程与人工验收

工程验证使用临时 home、假数据和测试证书，不安装真实服务或真实发信。真人双机/Tailscale、服务和实际分享使用仍为 Pending，见 [人工验收](acceptance-m1.md)。原始范围与工程完成标准以 [PLAN M2-05](PLAN.md) 为准。
