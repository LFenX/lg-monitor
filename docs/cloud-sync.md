# 云端账号与多设备同步部署指南（`sync.lfen.ink`）

`lg-monitor` 保持**本地优先（Local-First）**：不登录即可 100% 正常使用本机监控与内网共享。登录邮箱账号后，多台设备（家里电脑、公司主机、云服务器等）会自动通过极简增量压缩协议同步面板结构、实时状态、24 小时心跳条与服务活文档，并在侧边栏按设备分组展示。

## 1. 流量与存储优化设计

| 层级 | 内容 | 触发时机 | 典型体积（gzip 后） |
| --- | --- | --- | --- |
| 保活心跳 | `metaHash` + `stateHash` | 没有要发的内容时每 60 秒一次 | **~85 Bytes** |
| Layer 1 元信息 | 面板结构、服务名称/层级/简介/依赖/文档哈希、本机昵称和头像 | 增删改面板或服务、改昵称头像时 | **~0.8–1.5 KB**（平时 0 B） |
| Layer 2 实时状态 | 状态码、48 字符 24h 心跳串、万分比可用率、延迟、简短异常原因 | 正常/异常翻转、告警数或异常原因变化：防抖 2.5 秒后立即推送；只有可用率、延迟这类漂移时最多 5 分钟带一次 | **~0.4–1.0 KB**（20 个服务） |
| Layer 3 活文档 | 服务 Markdown 文档 | 仅服务端返回 `needDocs` 时上传，打开远程服务详情时按需 ETag 拉取 | 按需（命中 `304` 为 0 B） |

- 延迟几乎每次探测都不一样，所以它不会单独触发推送；真正需要人处理的变化（服务挂了、恢复了、告警触发了）不受这个限制。
- 拉取跟着人走：有页面在看其他设备时最多每 20 秒拉一次（多数是 `304`），没人看时每 5 分钟一次。上传的头像可能是 64 KB 的图片，只在元信息变化时随快照下发一次。
- 云端 D1 **不存历史时序表**，每台设备仅覆盖写（UPSERT）最新快照，单设备占用恒定 **< 10 KB**。
- 每个账号有一个只增不减的同步序号（`users.sync_seq`），解绑设备或多台设备同时推送都不会让其他设备漏掉更新；客户端发现增量里引用了自己没有的数据时，会自动重新拉一次全量。
- 云端拒绝本机的登录凭证（过期、在别的设备上被解绑）时，本机自动退回未登录状态并在「设置 → 账号与同步」里说明原因，不会拿失效凭证反复重试。

## 2. 部署 Cloudflare Worker + D1（约 5 分钟）

服务端源码位于仓库 [cloud/sync-worker/](../cloud/sync-worker/)。

### 第一步：准备 Resend 发信（免费 3,000 封/月）

1. 登录 [Resend](https://resend.com)，在 **Domains** 中添加并验证你的域名（例如 `lfen.ink`，按提示添加 2 条 DNS 记录）。
2. 在 **API Keys** 中新建一个具备发送权限的 API Key（记为 `re_...`）。

### 第二步：创建 D1 数据库并建表

```powershell
cd cloud\sync-worker
pnpm install
npx wrangler login
npx wrangler d1 create lgmon-sync
```

将控制台输出的 `database_id` 填入 `cloud/sync-worker/wrangler.toml` 的 `database_id` 字段，然后执行建表：

```powershell
npx wrangler d1 execute lgmon-sync --remote --file=./schema.sql
```

### 第三步：配置密钥并部署上线

```powershell
# 填入你的 Resend API Key（不会写入仓库）
npx wrangler secret put RESEND_API_KEY

# 如需自定义发件人，可在 wrangler.toml 的 [vars] 修改 MAIL_FROM
npx wrangler deploy
```

### 第四步：绑定自定义域名 `sync.lfen.ink`

在 Cloudflare Dashboard → **Workers & Pages** → `lgmon-sync` → **Settings** → **Domains & Routes** 中添加 Custom Domain：`sync.lfen.ink`（或在 `wrangler.toml` 中取消 `routes` 注释再 `wrangler deploy`）。

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
node --test test\worker.test.ts
```

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
