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
