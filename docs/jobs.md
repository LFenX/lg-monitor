# 批处理任务监控

有些工作不是一直运行的服务，而是每天跑一次的任务，例如 AICS 每日数据上传：取生产数据、校验、打包、备份、分别上传到国内 OSS 和海外 S3、核对清单。进程退出码为 0、端口能连上，都说明不了数据真的传完了。批处理任务监控只看任务自己报来的业务事件，按业务日期一天一条记录。

## 建一个任务

任务是 `targets.yaml` 里带 `job` 的目标，所以它和其他服务一样可以放进面板、挂在主服务下面、写说明和文档。它不需要 `match`、探测或规则。

```yaml
targets:
  - id: aics-upload
    name: AICS 每日上传
    role: scheduler
    parent: aics
    description: 每天 02:00 把前一天的邮件数据分别传到国内 OSS 和海外 S3
    job:
      schedule:
        at: "02:00"          # 北京时间；省略表示不判定漏跑
        grace: 2h            # 计划时间过后多久还没有任何运行算漏跑，默认 2h
        days: [mon, tue, wed, thu, fri]   # 省略表示每天
        from: "2026-11-20"   # 从这天起启用，之前不算漏跑
        skip: ["2026-12-25"] # 不运行的日期，不算漏跑
      maxDuration: 6h        # 一次运行超过它还没有新事件也没结束，算结果未知
      production: [aics-db, aics-relay]   # 生产通路上已有的监控
      source: {kind: api}    # api：任务自己上报；file：采集器读取 JSON Lines 文件
```

`stages` 省略时用预设的七个阶段：

| ID | 名称 | 说明 |
|---|---|---|
| fetch | 生产数据获取 | 关键：失败则整次运行失败 |
| verify | 中转校验 | 关键 |
| package | 打包 | 关键 |
| backup | 备份 | |
| upload_cn | 国内 OSS 上传 | 成功必须带回执 |
| upload_overseas | 海外 S3 上传 | 成功必须带回执 |
| manifest | 最终清单核对 | |

也可以自己列阶段：`stages: [{id: fetch}, {id: export, name: 导出, critical: true}, {id: upload_cn}]`。预设阶段只写 ID 就会带上它的名称和规则。

## 上报事件

每个任务有一把自己的上报密钥。在监控器电脑上用管理员终端运行：

```powershell
lgmon job key aics-upload
```

密钥只显示这一次，以 `lgjob_` 开头。它只能给这个任务上报事件，不能读取或修改任何别的内容；放进任务的运行环境，例如环境变量 `LGMON_JOB_KEY`。再运行一次会换一把新的，旧的立即失效。持有 config 权限的 API 令牌也能上报。

```http
POST /api/v1/jobs/aics-upload/events
Authorization: Bearer lgjob_...
Content-Type: application/json

{"events": [
  {"event_id": "20261118-a1-start", "attempt_id": "20261118-a1", "kind": "attempt", "state": "running", "trigger": "scheduled", "business_date": "2026-11-18", "ts": 1794938400000},
  {"event_id": "20261118-a1-fetch-ok", "attempt_id": "20261118-a1", "kind": "stage", "stage": "fetch", "state": "success", "started_at": 1794938400000, "finished_at": 1794938700000, "ts": 1794938700000},
  {"event_id": "20261118-a1-cn-08", "attempt_id": "20261118-a1", "kind": "partition", "region": "cn", "hour": 1794873600000, "unique_recipients": 812, "unique_senders": 95, "received_rows": 10233, "sent_rows": 1408, "gz_files": 1, "compressed_bytes": 3355443, "target_dir": "oss://bucket/2026/11/17/08/", "verify": "ok", "upload": "ok", "ts": 1794939000000},
  {"event_id": "20261118-a1-upcn", "attempt_id": "20261118-a1", "kind": "stage", "stage": "upload_cn", "state": "success", "receipt": "etag 9b2c...", "ts": 1794939600000}
]}
```

一次 1～500 条。返回 `accepted`（新收下的）、`duplicates`（收过的 event_id，没有再记）、`conflicts`（event_id 收过但内容不同，保留第一次的）和 `rejected`（第几条、哪里不对）。有问题的事件单独拒收，不影响同一批里的其他事件；全部都不能收时返回 400。

**重试只重发事件，不重跑业务。** 上报失败（断网、监控器重启）时，把事件留在本地，之后原样重发：event_id 不变，lgmon 去重，重发多少次都只记一次。不要因为上报失败去重新上传数据。

### 事件字段

字段是固定的，多出来的字段（例如邮件正文、数据库连接串）会让整条事件被拒收。

| 字段 | 说明 |
|---|---|
| event_id | 必填。这个任务里稳定唯一的 ID，1～128 个字母、数字或 `. _ : -`；同一件事重发时不变 |
| attempt_id | 必填。一次运行的 ID；手动补跑用新的 attempt_id，历次结果都保留 |
| ts | 必填。事件发生时的 UTC 毫秒时间戳 |
| kind | 必填。`attempt`（一次运行开始或结束）、`stage`（一个阶段的进展）、`partition`（一个区域一个小时的数据） |
| business_date | 业务日期（北京时间 YYYY-MM-DD）。省略时取这次运行最早一条事件的北京日期 |
| job_id | 可省略；写了就必须和地址里的任务一致 |
| trigger | attempt：`scheduled` 或 `manual`。省略时，同一天的第一次运行算计划运行，之后的算手动补跑 |
| state | attempt：`running`、`finished`、`aborted`。stage：`pending`、`running`、`success`、`failed`、`unknown`、`skipped` |
| stage、progress、started_at、finished_at、error、receipt | stage 事件：阶段 ID、进度 0～100、开始和结束时间（UTC 毫秒）、错误摘要、上传回执（ETag、请求 ID 等） |
| region、hour | partition 事件：区域（`cn` 国内、`overseas` 海外，或自定义）和邮件发生时间所在小时开始的 UTC 毫秒时间戳（整点，不做时区偏移） |
| unique_recipients、unique_senders、received_rows、sent_rows、gz_files、compressed_bytes | partition 事件：独立收件数、独立发件数、收件数据条数、发件数据条数、gz 文件数、压缩后字节数 |
| target_dir、verify、upload | partition 事件：目标目录、校验结果和上传结果（`ok`、`failed`、`unknown`） |

错误、回执和目标目录里的邮箱地址、`password=…` 之类的赋值、云厂商访问密钥 ID 和很长的令牌会被打码，并截断到 500、200、300 个字。

同一阶段可以多次上报（开始、进度、结束）：状态取 ts 最新的那条；开始时间取第一次给出的（只报 running 时用它的 ts），结束时间和回执取最后一次给出的；阶段报成功时清掉之前的错误。

## 每天一条记录

记录按（任务，业务日期）归并，业务日期按北京时间。一天里的多次运行全部保留，这一天的状态取最近一次运行。

| 状态 | 什么时候 |
|---|---|
| 待运行 pending | 今天还没到计划时间加宽限 |
| 运行中 running | 运行还没结束，最近仍有事件 |
| 成功 success | 每个阶段都成功或跳过，上传阶段都带回执，没有失败或结果未知的分区 |
| 部分失败 partial | 有阶段或分区失败，但关键阶段成功、两端上传至少一端成功 |
| 失败 failed | 关键阶段（取数、校验、打包）失败，两端上传都失败，或运行被中止 |
| 漏跑 missed | 计划日期过了宽限还没有任何运行 |
| 结果未知 unknown | 超过最长运行时间没有新事件也没结束；或运行结束了但有阶段没上报、阶段或分区结果未知、上传没有回执。上传结果未知不会显示成成功 |
| 未启用 disabled | 计划之外的日期、`from` 之前、`skip` 里的日期，不算漏跑 |

小时分区显示时，国内目录按 UTC+8、海外目录按 UTC+0，都带年份，例如“2026/11/18 08 时（UTC+8）”。每次运行每区域每小时一行；补跑追加新的行，不覆盖之前的。

## 依赖与生产通路

`production` 列出生产通路上已有的监控。任务详情把两件事分开：

- **依赖现在是否正常**：那些监控当前的状态。
- **本次生产取数是否成功**：这次运行 fetch 阶段的结果。

依赖正常不代表这次取数成功，取数失败也不一定是依赖坏了。服务树不变，任务可以挂在 AICS 主服务下面。

## 查询

| 接口 | 用途 |
|---|---|
| GET /api/v1/jobs/{id}/days?days=14 | 最近几天（1～90，默认 14）每天一条记录，最新的在前 |
| GET /api/v1/jobs/{id}/days/{date} | 某个业务日期的明细：每次运行的阶段时间线、小时分区数据、依赖现状和本次取数结果 |

面板上任务一行显示最近一天有消息的记录：成功为正常；失败、部分失败、漏跑为故障；其余为未知。

## 保存多久

任务事件是业务记录，按收到的时间保存 400 天，不受原始采样保留期影响。
