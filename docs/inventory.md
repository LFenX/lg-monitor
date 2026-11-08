# 启动项与计划任务

网页主机详情展示计划任务结果、启动登记项、最近启动项变化和来源覆盖。全部功能只读；程序不会运行、创建、启停或删除系统任务/启动项。

采集器本机 settings.yaml 可设置采样周期，默认一分钟，允许 10 秒到一小时，重启采集器生效；不随监控目标配置下发：

```yaml
inventory:
  interval: 1m
```

计划任务通过本机 Task Scheduler COM 枚举，包括隐藏任务。仅采集名称/任务路径、是否启用、状态、上次运行、下次运行、原始结果代码、遗漏次数，不读 Actions、XML、参数或凭据。时间由 Windows 本地时间按本机时区转换为 UTC Unix 毫秒；0 表示从未运行或没有下次安排。非零结果也包括调度器信息状态，网页保留十六进制原值，不统一判作故障。

启动项覆盖 HKLM 的 Run/RunOnce 32/64 位视图，以及已加载普通用户 HKU 的共享 Run/RunOnce 视图；覆盖公共和这些用户的本机默认 Startup 目录。HKU 的共享视图仅采一次，避免同一变化重复上报。未加载用户 hive 不会被加载；用户注销后旧来源标为暂不可读，不误报删除。该清单不覆盖所有 Autoruns 扩展点、重定向的用户 Startup 目录或 Task Manager 的 StartupApproved 禁用标记。

注册表命令原值只在本机计算 HMAC-SHA256，随机密钥存入既有本机加密密钥库；传输和数据库没有原命令、参数或密码。指纹版本改变后重新建立基线。目录只采常规文件的名称、大小和修改时间，不读取文件正文或解析快捷方式。目录的每级祖先检查重解析点，读取期间禁止删除共享以防被替换；网络/可移动盘和目录重解析点被拒绝。文件元数据相同的内容替换不在本功能的检测范围内。

每个来源成功时发送完整快照，平时只在变化时上报，十分钟后在下一采集轮重发。每个来源最多 2048 项，编码 JSON 最大 768 KiB，另有来源/目录枚举上限；超限或读取失败不会发布截断清单，不会清空旧值。来源失败显示 read_failed、limit_exceeded 或 source_unavailable；最近成功时间用于判断数据时效。

首次读取每个来源只建立基线，后续登记项新增、修改、移除写入变化表。基线跨采集器/监控器重启保留，离线快照按原序号补传；重复或旧序号不重复生成变化。当前清单和覆盖长期保留，历史快照和变化按 retention.raw 清理；水位前的迟到快照可以推进当前状态，但不补写已清理历史，并留下 late_dropped 缺口。

HTTP 复用 read 权限的 POST /api/v1/query，CLI 复用 lgmon query，MCP 复用 query_sql 和 get_schema；没有新增写入口或工具。网页至多显示 1000 项和最近 100 条变化，完整清单使用只读 SQL。

```powershell
lgmon query "SELECT kind,scope,name,ts,detail FROM inventory_current ORDER BY collector_id,kind,scope,id" --json
lgmon query "SELECT collector_id,ts,name,change FROM inventory_changes ORDER BY ts DESC LIMIT 100" --json
lgmon query "SELECT collector_id,kind,scope,last_success,error_code FROM inventory_scopes" --json
```

任务结果查询（HTTP/MCP 同一 SQL）：

```sql
SELECT collector_id,name,
       json_extract(detail,'$.enabled') AS enabled,
       json_extract(detail,'$.state') AS state,
       json_extract(detail,'$.lastRun') AS last_run,
       json_extract(detail,'$.lastResult') AS last_result
FROM inventory_current
WHERE kind='tasks' AND (:collector IS NULL OR collector_id=:collector)
ORDER BY collector_id,scope,id
```

inventory_snapshots 的 entries 是当时的完整白名单条目数组，可结合 json_each 查询历史；inventory_changes 保存启动项 added/changed/removed。SQL 规则允许这两张历史表，并自动限制到规则评估时间；inventory_current 和 inventory_scopes 属当前态，不能用于事件时间回放。程序不自动创建规则，不推断某个任务应该何时运行。

来源定义见 Microsoft 的 [TaskService](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskservice)、[RegisteredTask](https://learn.microsoft.com/en-us/windows/win32/taskschd/registeredtask)、[Run/RunOnce](https://learn.microsoft.com/en-us/windows/win32/setupapi/run-and-runonce-registry-keys) 与 [WOW64 注册表视图](https://learn.microsoft.com/en-us/windows/win32/winprog64/shared-registry-keys)。测试只枚举本机既有来源并操作临时普通目录，没有注册启动项或计划任务。真实用户使用验收与原有服务、重启、双机验收仍由本人完成。
