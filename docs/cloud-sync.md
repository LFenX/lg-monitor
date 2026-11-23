# 云端账号与多设备同步部署指南（`sync.lfen.ink`）

`lg-monitor` 保持**本地优先（Local-First）**：不登录即可 100% 正常使用本机监控与内网共享。登录邮箱账号后，多台设备（家里电脑、公司主机、云服务器等）会自动通过极简增量压缩协议同步面板结构、实时状态、24 小时心跳条与服务活文档，并在侧边栏按设备分组展示。

## 账号：先注册，再登录

- 第一次使用在「注册」页拿注册验证码，注册邮件和登录邮件是两封不同的信；注册时可以顺便设置密码，不设就只用验证码登录。
- 已注册的邮箱在「登录」页用验证码（默认）或密码登录。登录邮件会写明是哪台电脑在请求，陌生请求一眼能看出来。
- 在登录页填了还没注册的邮箱，会自动切到注册页并保留填好的邮箱；反过来，已注册的邮箱在注册页会被带回登录页。
- 三种验证码（注册、登录、重置密码）互不通用，邮件跟随界面语言发中文或英文。

## 把其他设备的面板开放给内网访客

内网浏览器访客默认只看得到本机开放的面板，同一账号下其他电脑的面板一块都看不到。要开放时，在本机「内网 → 内网浏览器访问 → 我的其他设备」里逐块勾选，设置保存在本机 `settings.yaml` 的 `lan.webDeviceBoards`（设备 ID 对应面板 ID 列表）。访客的侧边栏会多出那台设备的卡片，只能只读查看勾选的面板，以及面板上服务的详情、活文档和批处理记录；指向未开放服务的依赖只显示名称。数据仍由本机从同步服务按需拉取，访客不会直接连接同步服务或那台电脑。

## 1. 流量与存储优化设计

| 层级 | 内容 | 触发时机 | 典型体积（gzip 后） |
| --- | --- | --- | --- |
| 保活心跳 | `metaHash` + `stateHash` | 没有要发的内容时每 60 秒一次 | **~85 Bytes** |
| Layer 1 元信息 | 面板结构、服务名称/层级/简介/依赖/文档哈希、本机昵称和头像 | 增删改面板或服务、改昵称头像时 | **~0.8–1.5 KB**（平时 0 B） |
| Layer 2 实时状态 | 状态码、48 字符 24h 心跳串、万分比可用率、延迟、简短异常原因 | 正常/异常翻转、告警数或异常原因变化：防抖 2.5 秒后立即推送；只有可用率、延迟这类漂移时最多 5 分钟带一次 | **~0.4–1.0 KB**（20 个服务） |
| Layer 3 活文档 | 服务 Markdown 文档 | 仅服务端返回 `needDocs` 时上传，打开远程服务详情时按需 ETag 拉取 | 按需（命中 `304` 为 0 B） |
| Layer 3 批处理记录 | 批处理任务最近 30 天的每日记录（运行、阶段、小时分区） | 状态帧只带最新一天的简报和 16 位内容哈希；哈希变了、服务端在 `needJobs` 里点名时才上传（单个任务压到 460 KB 以内，先去掉旧日子的小时分区）；打开远程任务详情时按需 ETag 拉取 | 按需（命中 `304` 为 0 B） |

- 延迟几乎每次探测都不一样，所以它不会单独触发推送；真正需要人处理的变化（服务挂了、恢复了、告警触发了）不受这个限制。
- 拉取跟着人走：有页面在看其他设备时最多每 20 秒拉一次（多数是 `304`），没人看时每 5 分钟一次。上传的头像可能是 64 KB 的图片，只在元信息变化时随快照下发一次。
- 云端 D1 **不存历史时序表**，每台设备仅覆盖写（UPSERT）最新快照，单设备占用恒定 **< 10 KB**。
- 每个账号有一个只增不减的同步序号（`users.sync_seq`），解绑设备或多台设备同时推送都不会让其他设备漏掉更新；客户端发现增量里引用了自己没有的数据时，会自动重新拉一次全量。
- 云端拒绝本机的登录凭证（过期、在别的设备上被解绑）时，本机自动退回未登录状态并在「设置 → 账号与同步」里说明原因，不会拿失效凭证反复重试。

## 2. 部署 Cloudflare Worker + D1

服务端源码位于仓库 [cloud/sync-worker/](../cloud/sync-worker/)。

**现状（2026-11-22）**：`lgmon-sync` 已部署在 `https://sync.lfen.ink`，D1 数据库 `lgmon-sync`（APAC）已建表，Resend 发信密钥已配置，真实邮箱验证码登录已跑通。

仓库是公开的，所以提交的 `wrangler.toml` 里 `database_id` 是占位符；真实 ID 放在不进仓库的 `wrangler.local.toml`（`wrangler.toml` 的副本）里，部署和执行 SQL 都带 `-c wrangler.local.toml`。换一台电脑部署时，复制 `wrangler.toml` 为 `wrangler.local.toml`，把 `npx wrangler d1 list` 查到的 ID 填进去。

### 配置 Resend 发信（免费 3,000 封/月）

1. 登录 [Resend](https://resend.com)，在 **Domains** 中添加并验证 `lfen.ink`（按提示在 Cloudflare 的 lfen.ink 区域添加 DNS 记录）。
2. 在 **API Keys** 中新建一个只有发送权限的 API Key。
3. 把它存成 Worker 密钥（命令会提示你粘贴，不会写进任何文件）：

```powershell
cd cloud\sync-worker
npx wrangler secret put RESEND_API_KEY -c wrangler.local.toml
```

发件人在 `wrangler.toml` 的 `[vars]` 里（`MAIL_FROM`），必须是 Resend 里验证过的域名下的地址；改了要同步到 `wrangler.local.toml` 再部署。

### 改代码后重新部署

```powershell
cd cloud\sync-worker
node --test test\worker.test.ts test\mail.test.ts
npx wrangler deploy -c wrangler.local.toml
```

改了 `schema.sql` 时，先写一条只做增量变更的 SQL（例如 `ALTER TABLE`），用 `npx wrangler d1 execute lgmon-sync --remote -c wrangler.local.toml --file=<那个文件>` 执行；`schema.sql` 里的 `CREATE TABLE IF NOT EXISTS` 不会改已有的表。

验证服务健康：

```powershell
curl https://sync.lfen.ink/health
# 返回 {"ok":true,"service":"lgmon-sync",...}
```

## 3. 本地开发联调

### 不装 wrangler：用 Node 跑真实 Worker 代码

`cloud/sync-worker/dev/server.ts` 用 Node 22 自带的 `node:sqlite` 代替 D1，跑的是同一份 Worker 代码；验证码直接打印在终端里，不发邮件。

```powershell
# 终端 1：本地同步服务（--db 省略时数据只在内存里）
node cloud\sync-worker\dev\server.ts --port 8787

# 终端 2、3：两台“设备”，各用一个独立的数据目录
$env:LGMON_SYNC_SERVER = "http://127.0.0.1:8787"
.\bin\lgmon.exe run --role both --home dev-home\cloud-a --no-tray --no-window --listen 127.0.0.1:18443
```

Worker 的测试同样不需要 Cloudflare 账号：

```powershell
cd cloud\sync-worker
node --test test\worker.test.ts test\mail.test.ts
```

本地同步服务运行时，浏览器打开 `http://127.0.0.1:8787/dev/mail` 可以逐封查看注册、登录、重置密码邮件的中英文版本（只在 `dev/server.ts` 里，不随 Worker 部署）。

### 用 wrangler 联调

无需真实 Resend Key 即可本地调试完整验证码登录与多设备同步：

```powershell
# 终端 1：启动本地 Worker（验证码直接打印在终端）
cd cloud\sync-worker
npx wrangler d1 execute lgmon-sync --local --file=./schema.sql
npx wrangler dev --var DEV_MODE:true

# 终端 2：指向本地 Worker 启动 lgmon
$env:LGMON_SYNC_SERVER = "http://127.0.0.1:8787"
.\bin\lgmon.exe run --role both --home dev-home\ui-preview --no-tray --no-window
```

也可以在 lgmon 网页「设置 → 账号与同步」中直接修改同步服务器地址。
