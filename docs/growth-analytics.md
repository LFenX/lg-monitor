# 应用发展与转化漏斗接口规范（`license.lfen.ink` 扩展）

从 `v0.14.4` 起，lgmon 客户端会在每 30 分钟一次的授权心跳中，向授权服务（`https://license.lfen.ink`）附加**匿名能力里程碑**（绝不包含服务名称、网址、主机敏感信息或任何密钥），供管理员设备上的 **「应用发展 / Growth」** 看板（`#/admin/growth`）统计全链路转化漏斗。

## 1. 客户端上报格式

### 方式 A：复用现有 `POST /v1/check`（已兼容旧服务端）
`POST /v1/check` 的请求体新增可选字段 `usage`：

```json
{
  "token": "<signed-license-token>",
  "device": { "id": "dev-...", "name": "MyPC", "version": "0.14.4" },
  "usage": {
    "boardsCount": 2,
    "monitorsCount": 8,
    "hasFirstBoard": true,
    "hasFirstMonitor": true,
    "features": ["agent_mcp", "alerts", "bohemia", "deps", "docs", "feishu", "lan_web"]
  }
}
```

### 方式 B：独立上报端点 `POST /v1/telemetry`（可选）
客户端在 `/v1/check` 返回 `200` 后，会附带发送 `POST /v1/telemetry`（与上面相同 JSON 体）。若旧服务端未实现该接口（返回 `404`），客户端会静默忽略。

### 能力标识符（`usage.features`）
| 标识 | 含义 |
| --- | --- |
| `agent_mcp` | 已通过 MCP 接入 AI Agent |
| `docs` | 至少有一个服务编写了活文档 |
| `deps` | 至少配置了一条服务依赖关系 |
| `alerts` | 已配置至少一条告警规则 |
| `feishu` | 已接入飞书应用或飞书通知 |
| `bohemia` | 已配置波西米亚 AI 助手 |
| `lan_share` | 已开启内网配对共享 |
| `lan_web` | 已开启免安装内网只读网页 |
| `departments` | 已创建/托管部门 |
| `batch_jobs` | 已配置批处理任务监控 |

## 2. 管理员查询端点：`GET /v1/admin/analytics`

请求头携带 `X-Admin-Session: <session>`（与 `/v1/admin/keys` 相同认证）。

返回 JSON：

```json
{
  "telemetryReady": true,
  "devices": [
    {
      "deviceId": "dev-abc123",
      "keyId": "key-xyz",
      "label": "小王的笔记本",
      "version": "0.14.4",
      "firstSeen": 1763700000000,
      "lastSeen": 1763786400000,
      "usage": {
        "boardsCount": 2,
        "monitorsCount": 6,
        "hasFirstBoard": true,
        "hasFirstMonitor": true,
        "features": ["agent_mcp", "docs", "feishu"]
      }
    }
  ]
}
```

> 服务端只需在 `devices` 表增加一个 `usage_json TEXT` 字段，在 `/v1/check` 或 `/v1/telemetry` 时更新该字段，并在 `GET /v1/admin/analytics` 返回即可；客户端看板会自动按激活密钥分组计算从 **GitHub 下载 → 密钥激活 → 首个面板 → 首个服务 → 各进阶功能** 的全链路转化漏斗。

## v0.16.0 下载渠道与内测页

GitHub 的文件级计数按主安装包、旧版引擎安装包、SHA256SUMS、安装信息和其他附件分别汇总。本地按钮点击不增加 GitHub 计数。GitHub 的直下没有渠道、地区或激活身份可供回填。

README 主下载链接、访客面板、网页更新和原生小窗口更新使用授权服务的分发入口。入口收到 GitHub 安装包响应后按渠道、版本和 Cloudflare 提供的国家与地区记录一条发出记录。数据表不保存原始 IP。发出后仍可能断线或取消。直下、请求 HEAD 或声明 DNT / GPC 均无分发回执。可继续从 GitHub Releases 直接下载原文件。

安装包字节与 GitHub 一致。项目分发入口在下载文件名中附加随机回执，安装器保留首次安装的回执；可用原文件名对应的 SHA256SUMS 验证。全新安装若改名、转发或从多个电脑运行，回执与使用者便不能一一对应。原生更新仍验证 GitHub 的摘要，分发服务出错时回退 GitHub。

仅需密钥的正式安装版在本机实际显示缺少密钥的页面时上报版本、随机安装 ID 及可用的回执。后台检查、访客、开发运行和激活失败的输入内容都不生成到达记录。报告无需等待，暂不可用时可在页面可见后重试。浏览器启用请勿跟踪或全球隐私控制时跳过页面报告。服务端对安装 ID 散列去重，普通激活和管理员认领都结束等待。管理员认领从密钥页统计中排除。待激活数表示近 30 天首次到达且仍无成功激活回执的安装实例；等待至少 24 小时是其中一个子集。离线、旧版和采集器单角色不产生可见页面记录。

授权服务的管理员端点 `GET /v1/admin/growth` 返回近 30 天分发及密钥页的渠道、地区汇总，仅持有管理员会话可读。面板分开显示下载出口地区与安装端地区，未知位置单列。明细按 60 天保留策略清理。授权服务的实现和建表脚本在私有仓库 lgmon-license。
