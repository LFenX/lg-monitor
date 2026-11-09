# PRD 逐项工程核验（M3-04）

本表直接从 PRD 原文建立；工程结论与本人验收分别记录。覆盖 54 条编号需求及全部未编号产品/非功能/技术/协议/里程碑要求；工程证据在本项最终验证记录集中复验。存在明确修订与真人 Pending，不宣称原 PRD 已真实验收闭环。

- 基线：0335747d1c55bf1dfbbf3da57357f0d18dc6c62d。
- PRD Windows 工作区原始字节 SHA-256：`82b8b7fa6da2bcf730c82e8a9cfec5e90d5c00d35e8fe628fcc6a43c4a8ed8ff`。
- PRD 规范化 LF 字节 SHA-256：`c8a7e98d2107e0ffe225c348685daf7b50876c20f7795cd0cb32d2dc1a34df75`（用于跨换行格式核对）。
- 依据：PRD 第 1～10 节；明确的 REVIEW 修订须单列，不以计划勾选替代。
- 本人验收：Pending；不安装真实服务、不重启、不外发。

原文优先级：

> 优先级：P0 = 第一版必须有，P1 = 第二版，P2 = 以后。

## 编号需求

### C1

原文（PRD.md 第 55 行）：

> | C1 | 系统指标 | CPU、内存、磁盘、网络、开机时长 | P0 |

- 工程结论：已实现。系统 CPU、物理内存、磁盘容量、网卡累计量/速率和 uptime 使用原生源及计数差分。
- 实现/行为证据：`internal/collector/metrics.go`、`metrics_native.go`；`TestSystemMetricsUseCPUAndNetworkCounterDeltas` 和 `TestNativeAdditionalCollectionIsReadOnly`；原始与分级汇总进入 `system_metrics/disk_metrics/network_metrics`。
- 条件、反例与未覆盖范围：CPU 按全机容量归一；计数重置、首次样本及接口变化有分支。真实 24h 资源验收另列 NFR-01，不能用短窗口代替。
- 本人验收：Pending。

### C2

原文（PRD.md 第 56 行）：

> | C2 | 进程表 | PID、名称、命令行、父进程、CPU、内存、线程数、启动时间 | P0 |

- 工程结论：按明确的隐私修订实现。完整字段保存在采集器私有快照，远端仅返回允许的进程字段。
- 实现/行为证据：`internal/collector/source.go`、`process_windows.go`、`snapshot.go`；`TestProcessSnapshotFields`、`TestProcessSourceFindsOwnPing`；`TestHTTPProcessesReuseSnapshotKeepCommandsPrivateAndShareRateLimit` 与 MCP 全工具测试。
- 条件、反例与未覆盖范围：REVIEW R2-03 和 M1-16 明确命令行只用于本机匹配，不进入传输、数据库或远端列表；本地 targets test 可诊断。父进程/线程/启动时间保留；PID 必须和创建时间组合，不能复用缓存身份。
- 本人验收：Pending。

### C3

原文（PRD.md 第 57 行）：

> | C3 | 监控目标 | 用规则识别“这是我的博客”：按名称、命令行正则、监听端口、所在目录、Windows 服务名匹配，跨重启保持同一身份 | P0 |

- 工程结论：已实现。稳定 target ID 与一次采样的 PID 分开；多条件 AND，支持名称/正则/端口/程序目录/工作目录/服务。
- 实现/行为证据：`internal/collector/match/`；`TestTargetIdentitySurvivesProcessRestart`、`TestServiceTargetsUseOnlyTheServiceProcess`、`TestNativeWorkingDirectoriesSeparateOwnedPingProcesses`；`cmd/lgmon/targets.go` 的一次性试匹配。
- 条件、反例与未覆盖范围：目录大小写、边界/子目录、UNC、PID 重用及无权限均核验；工作目录仅对候选按身份缓存，不能把读取失败解释为匹配成功。没有改动真实被监控服务。
- 本人验收：Pending。

### C4

原文（PRD.md 第 58 行）：

> | C4 | Windows 服务 | 状态、启动类型、意外停止 | P0 |

- 工程结论：已实现。读取 SCM 状态、启动类型、进程身份和读取错误，状态变化触发服务规则。
- 实现/行为证据：`internal/collector/service.go`、`service_windows.go`；`TestServiceSourceKeepsUnreadableAndMissingFactsAndLookupAvoidsEnumeration`、`TestNativeAdditionalCollectionIsReadOnly`；C9 补充意外终止事件。
- 条件、反例与未覆盖范围：无权限/未知/不存在分别保留；测试只读已有 SCM，不启停真实服务。真实服务停止/恢复仍 Pending。
- 本人验收：Pending。

### C5

原文（PRD.md 第 59 行）：

> | C5 | 端口 | 谁在监听哪个端口，端口被占用、端口消失 | P0 |

- 工程结论：已实现。TCP listener 与 owner PID 进入目标匹配和端口规则。
- 实现/行为证据：`internal/collector/source.go`、`match/`；`TestPortSourceFindsOwnListener`、`TestSystemOwnedPortWithoutProcessSnapshot`、原生端到端目标切换夹具。
- 条件、反例与未覆盖范围：自建 listener 的消失、恢复和被另一个 PID 占用均有对应行为；系统 PID/进程表暂缺不能直接丢弃端口事实。未占用或关闭用户服务端口。
- 本人验收：Pending。

### C6

原文（PRD.md 第 60 行）：

> | C6 | 探测 | HTTP（状态码、关键字、耗时、证书到期）、TCP 连通 | P0 |

- 工程结论：已实现。HTTP 状态、关键词、耗时、TLS 最早证书到期和 TCP 连通均有真实回环探测。
- 实现/行为证据：`internal/collector/probe.go`、`http.go`、`probe.go`、`http_tls_test.go`；`TestHTTPKeywordsOnLocalServer`、`TestHTTPKeywordReadAndAllocationRemainBounded`、`TestHTTPSRecordsEarliestCertificateIncludingVerificationFailures`、`TestHTTPSRenewalUsesANewConnectionImmediately`、`TestTCPProbeOnOwnListener`。
- 条件、反例与未覆盖范围：不跟重定向、不使用环境代理；有超时/截断/连接失败/证书验证失败/链最早到期分支。关键词匹配读取有界，响应正文不传输或存库。证书告警依赖采集器能力，旧端显示 rule_errors。
- 本人验收：Pending。

### C7

原文（PRD.md 第 61 行）：

> | C7 | 脚本探测 | 定时运行任意脚本，脚本输出约定格式的 JSON 就成为指标和状态。Agent 扩展采集能力主要靠它 | P0 |

- 工程结论：按 REVIEW R3-04 的安全修订实现，不执行监控器下发的任意命令。
- 实现/行为证据：`internal/config/script.go`、`internal/collector/script*.go`；`TestNativeScriptProbeBranches`、`TestNativeScriptPowerShellAndBatchArguments`、`TestNativeScriptMissingAndUnsafePathsNeverExecute`、`TestNativeScriptCancellationCleansOwnDescendants`。
- 条件、反例与未覆盖范围：仅采集器本机受保护 scripts 目录中的允许扩展名，参数字符集受限；拒绝路径穿越、重解析点、元字符/展开、不安全 ACL。超时/取消只清理本测试的子进程树。脚本 JSON 有上限和格式校验。
- 本人验收：Pending。

### C8

原文（PRD.md 第 62 行）：

> | C8 | 开机项与计划任务 | 计划任务上次运行结果、启动项变化 | P1 |

- 工程结论：已实现。计划任务结果、运行时间和启动项元数据有当前态、来源覆盖与变化历史。
- 实现/行为证据：`internal/collector/inventory*.go`、`internal/store/inventory*.go`；`TestInventorySourcesRetainCoverageFailuresAndValidateBoundaries`、`TestNativeInventoryOnlyReadsExistingTasksRegistryAndDirectoryMetadata`、`TestInventoryReadHTTPAndMCPQueryUseSameExistingReadPermission`。
- 条件、反例与未覆盖范围：仅 Task Scheduler/注册表/启动目录的允许元数据，不读文件正文或任务命令。失败来源保留 last_success，不能将读取失败当作删除；非零任务返回码按原始值展示。
- 本人验收：Pending。

### C9

原文（PRD.md 第 63 行）：

> | C9 | 事件日志 | 应用崩溃（Application 1000/1002）、服务意外终止（System 7031/7034）、意外关机 | P1 |

- 工程结论：已实现。Windows Event Log 拉取、书签、默认订阅和自定义匹配进入统一可靠上报与告警。
- 实现/行为证据：`internal/collector/eventlog*.go`、`internal/app/eventlog*.go`、`internal/store/eventlog*.go`；`TestNativeEventLogPullAndBookmarkWithoutRegisteringSource`、`TestEventLogXMLExtractsSubjectsAndFallsBackToEventData`、`TestHTTPAlertEventsReadPermissionWindowPaginationAndPrecision`。
- 条件、反例与未覆盖范围：Application 1000/1002、System 7031/7034 及意外关机；限定通道、消息与归属有界，书签失败/日志清空有缺口记录。subject/subscription 双归属去重，最多 32 目标按稳定顺序保留；不注册事件源或制造真实系统故障。
- 本人验收：Pending。

### C10

原文（PRD.md 第 64 行）：

> | C10 | 即时进程事件 | 用 ETW/WMI 事件代替轮询，进程退出一秒内感知 | P2 |

- 工程结论：已实现。原生 ETW 进程启动/退出提示唤醒后立即核对真实事实，正常轮询保留为回退。
- 实现/行为证据：`internal/collector/process_events_etw_windows.go`、`internal/app/process_events.go`；`TestNativeProcessEventsObserveOwnChildAndRelease`、原生端到端进程事件及多监控器测试。
- 条件、反例与未覆盖范围：只有事件提示不直接断言退出；验证 PID+创建时间、事件突发合并、丢事件/权限失败/重连回退和取消释放。1 秒工程证据来自自建子进程，不代表所有机器或真实服务验收。
- 本人验收：Pending。

### C11

原文（PRD.md 第 65 行）：

> | C11 | 导入服务开关配置 | 读取 `config/services.json`，自动把已登记的网站、Tunnel、数据库变成监控目标 | P1 |

- 工程结论：已实现。只读取采集器本机明确配置的服务开关 services.json，预览后按指纹/版本导入。
- 实现/行为证据：`internal/config/service_switch*.go`、`internal/app/service_switch*.go`、`cmd/lgmon/import.go`；服务开关配置与原生临时 mTLS 导入测试、MCP 全工具矩阵。
- 条件、反例与未覆盖范围：不是任意远端路径读取器；网站/Tunnel/数据库映射、去重、缺失条目与公网探测均按 PLAN 约定，导入不自动加规则；预览过期/配置冲突必须重读，未确认不写。
- 本人验收：Pending。

### T1

原文（PRD.md 第 73 行）：

> | T1 | 采集器主动连接监控器，穿过 NAT 和防火墙时不需要在被监控电脑上开端口 | P0 |

- 工程结论：工程链路已实现。collector 主动建立到 monitor 的 gRPC 连接，不创建 collector 入站监听。
- 实现/行为证据：`internal/app/remote.go`、`internal/transport/delivery.go`、`link.go`；原生双节点、多监控器及离线补传夹具。
- 条件、反例与未覆盖范围：工程证明出站拓扑；真实 NAT、防火墙、跨机 Tailscale 属于 Pending，未修改本机网络。monitor 的 HTTP/团队端口与 collector 入站要求分开。
- 本人验收：Pending。

### T2

原文（PRD.md 第 74 行）：

> | T2 | 双向加密且双向认证；配对一次，以后自动连接 | P0 |

- 工程结论：已实现。私有 CA、独立设备证书、TLS 1.3 mTLS、一次性配对和独立吊销。
- 实现/行为证据：`internal/transport/pki.go`、`pairclient.go`、`pairing.go`；`TestPairingCorrectCodeWrongExpiredReusedAndFingerprint`、`TestAnonymousAndCollectorCannotManageAndRevokeInterruptsStream` 及原生 PKI/配对测试。
- 条件、反例与未覆盖范围：REVIEW R3-05 将配对核对改成 16 位十六进制 CA 指纹；错误/缺失指纹时不发送码、不落私钥。身份不覆盖，服务证书和管理员证书用途分离；不能以 Tailscale 登录代替证书验证。
- 本人验收：Pending。

### T3

原文（PRD.md 第 75 行）：

> | T3 | 断网期间缓存在本地，恢复后按顺序补发，不丢不重 | P0 |

- 工程结论：已实现有界可靠补传。递增序号、先持久缓存、提交后确认、确认后删前缀，接收去重。
- 实现/行为证据：`internal/transport/outbox.go`、`delivery.go`、`link.go`；`TestOutboxPersistsAllocationAndOnlyDeletesAcknowledgedPrefix`、`TestDeliveryRecoversLostAckAndUnappliedInboxWithoutDuplicateRows`、`TestOfflineTwoMinutesPreservesThreeNativeTargetChanges`。
- 条件、反例与未覆盖范围：批量最多 256 条/一个接收事务；丢 ack、重启/重放、部分重叠均测试。容量满/超龄按已批准策略丢最旧并持久 gap，不承诺无限离线无丢失；24h 容量和真实断网分别列 NFR-04。
- 本人验收：Pending。

### T4

原文（PRD.md 第 76 行）：

> | T4 | 监控器能把配置（监控目标、探测）下发给采集器，Agent 在监控器上改一次就生效 | P0 |

- 工程结论：已实现。中心修改目标和探测后，按采集器选择配置、持久缓存、校验和确认版本。
- 实现/行为证据：`internal/transport/config.go`、`config_state.go`、`internal/app/remote.go`；`TestCachedTargetsPublishOnlyAfterDurableValidSaveAndRestoreOffline`、`TestConfigurationPushWhileDataAckIsPendingRetriesAndReconnects`、原生配置浏览器测试。
- 条件、反例与未覆盖范围：写缓存/校验失败不激活新配置；离线仍用最后成功版本。密钥、本机操作授权及脚本正文不下发；UI 展示保存、发送、应用进度而不是将保存当生效。
- 本人验收：Pending。

### T5

原文（PRD.md 第 77 行）：

> | T5 | 采集器失联本身就是告警 | P0 |

- 工程结论：已实现。10 秒独立心跳，超过 30 秒离线；离线状态和通知与目标告警分开。
- 实现/行为证据：`internal/transport/heartbeat.go`、`internal/app/presence.go`；`TestHeartbeatRunsWithoutSamplesAndDoesNotConsumeRecordSequence`、presence 默认渠道/原生断联测试。
- 条件、反例与未覆盖范围：没有指标变化也发送心跳；仅收到旧补传数据不冒充在线，未配通知渠道继续保留待通知状态。真实拔线/双机仍 Pending。
- 本人验收：Pending。

### T6

原文（PRD.md 第 78 行）：

> | T6 | 监控器能让采集器执行受限操作（重启某个服务），默认关闭，要逐条授权 | P1 |

- 工程结论：已实现。默认拒绝，本机管理员按精确 SCM 服务逐条授权，远端 admin 每次确认并使用稳定 requestId 对账。
- 实现/行为证据：`internal/collector/operation_policy.go`、`internal/transport/operations.go`、`operation_journal.go`、`internal/app/operations.go`；`TestNativeServiceOperationMTLSDefaultsHotPolicyAndPersistentNoRepeat`、`TestServiceOperationHTTPPermissionsConfirmationAndImmutableRequest`。
- 条件、反例与未覆盖范围：本机受保护路径及服务身份、策略版本、确认/理由校验；持久去重跨重启，不确定结果只查询原 ID。执行器为假服务，未给任何真实服务授权或执行重启。
- 本人验收：Pending。

### T7

原文（PRD.md 第 79 行）：

> | T7 | 一个采集器同时上报给多个监控器（比如家里和公司各一个） | P2 |

- 工程结论：已实现。最多 8 个独立监控器槽位，共享原生事实，隔离身份/配置/序号/确认/缓存及本机操作开关。
- 实现/行为证据：`internal/config/monitors.go`、`internal/app/collector_sources.go`、`remote.go`、`internal/transport/monitors_test.go`；`TestNativeMultiMonitorIsolationReplayRevocationAndResources` 和 `TestHTTPSettingsCannotExpandLocalMonitorTrustOrOperationGrants`。
- 条件、反例与未覆盖范围：单节点断线、吊销、错误身份、缺少默认配对不影响健康节点；额外节点默认无操作权。CPU/RSS 最终证据见 NFR-01，原生 24h/跨机仍 Pending。
- 本人验收：Pending。

### A1

原文（PRD.md 第 85 行）：

> | A1 | 内置规则类型：进程不在、服务停止、端口关闭、探测失败、证书即将到期、指标超阈值、采集器失联、脚本探测报错。事件日志匹配依赖 C9，和 C9 一起放在 P1（见第 10 节） | P0 |

- 工程结论：已实现全部列出的规则类型；事件日志匹配按 PRD 第 10 节归 M2。
- 实现/行为证据：`internal/rules/engine.go`、`additional.go`、`eventlog.go`、证书/目录规则；引擎、系统/脚本/服务阈值、事件日志、证书能力与错误恢复测试；网页 rule_errors 与 CLI 同源。
- 条件、反例与未覆盖范围：探测状态、证书能力缺失、SQL/来源读取错误均不能伪造恢复；内置类型按允许条件和比较符验证。真实服务/真实飞书仍 Pending。
- 本人验收：Pending。

### A2

原文（PRD.md 第 86 行）：

> | A2 | SQL 规则：一条只读 SQL 查询有结果就算触发，Agent 可以写出任意组合条件 | P0 |

- 工程结论：按 PLAN 的稳定实例 key 契约实现只读 SQL 规则，有结果行即触发对应实例。
- 实现/行为证据：`internal/rules/additional.go`、`internal/store/rule_query.go`；`TestSQLInstancesIntervalFailureAndRecovery`、`TestSQLAlertKeysPersistAsSeparateActiveInstances`、authorizer/CTE/时间边界测试。
- 条件、反例与未覆盖范围：必须返回非空 key，可选 summary；最多 100 实例、重复 key 合并。SQL 只读连接、有超时及行数/表白名单；查询失败记录 rule_errors，不把失败当零行恢复。
- 本人验收：Pending。

### A3

原文（PRD.md 第 87 行）：

> | A3 | 生命周期：待定 → 触发 → 恢复；持续 N 秒才触发；同一问题只发一次，恢复时再发一次 | P0 |

- 工程结论：已实现持久生命周期、for 持续条件、同事件去重和恢复通知。
- 实现/行为证据：`internal/rules/engine.go`；`TestPendingFlapThenFiringAndRecovery`、`TestZeroDurationAndRestartDoNotRepeatFiring`、`TestPersistenceFailureRetriesSameIDWithoutDuplicateNotification`、`TestFailedEnqueueRetriesBeforeRecovery`。
- 条件、反例与未覆盖范围：采集时间驱动旧数据追平，补传期不误发过时故障；未知状态重置 pending、保持 firing。通知网络幂等受外部 provider 时限约束，不承诺外部永久 exactly-once。
- 本人验收：Pending。

### A4

原文（PRD.md 第 88 行）：

> | A4 | 静音、维护时段、手动确认；抖动时自动合并 | P0 |

- 工程结论：已实现规则/目标静音、维护窗口、确认、提醒抑制与十分钟抖动合并。
- 实现/行为证据：`internal/store/controls.go`、`internal/app/notification_policy_test.go`；`TestAcknowledgementPersistsStopsRemindersButKeepsRecovery`、`TestSilenceKeepsStatesAndSurvivesRestartWithoutReplay`、`TestFourthTriggerProducesOneDigestUntilTenMinutesStable`、`TestQueuedFourthTriggerRendersOneDigestWithItsEventIdentity`。
- 条件、反例与未覆盖范围：排队后新增静音仍在发送前复查；状态继续推进、恢复仍可通知；全局暂停是不同的持久补发机制，按 REVIEW M1-17 不提供 MCP 工具。
- 本人验收：Pending。

### A5

原文（PRD.md 第 89 行）：

> | A5 | 试运行：用过去 24 小时的数据回放一条规则，告诉你会触发几次 | P1 |

- 工程结论：已实现历史时钟下的规则回放，默认过去 24h，返回触发次数/窗口与覆盖说明，不保存或通知。
- 实现/行为证据：`internal/rules/replay.go`、`replay_sql_test.go`、`cmd/lgmon/rules.go`；`TestReplayUsesHistoricalClockAndNeverMutatesTheSource`、`TestReplayMergesSameMillisecondFactsAndReportsNoData`、HTTP/MCP test_rule。
- 条件、反例与未覆盖范围：数据不足/已清理/无数据显式提示；SQL 使用历史态和已结束汇总桶，不能读当前态或系统现在时间。collector_offline 因没有历史心跳按 PLAN 明确拒绝，不能伪造回放结果。
- 本人验收：Pending。

### A6

原文（PRD.md 第 90 行）：

> | A6 | 告警说人话，并附带“交给 Agent 排查”的诊断包：相关进程、日志末尾、最近的变化 | P1 |

- 工程结论：已实现中文故障摘要和单次告警只读诊断包，网页复制/下载、CLI 和 MCP 同源。
- 实现/行为证据：`internal/store/diagnostics.go`、`internal/app/diagnosis.go`、`docs/diagnostics.md`；`TestHTTPDiagnosticReadScopeOpenAPIPrivacyAndMissingID` 与诊断/原生浏览器测试。
- 条件、反例与未覆盖范围：相关进程、事件日志元数据尾部、指标/探测及启动变化有时间窗/覆盖/截断说明；不读任意文件、命令行、密码或原始消息到诊断包。事件完整 ±60s 通过单独分页 API，卡片 15s 时限不等未来窗口。
- 本人验收：Pending。

### N1

原文（PRD.md 第 96 行）：

> | N1 | 飞书应用 | 用户提供自建应用的 App ID 和 App Secret，程序直接调用飞书开放平台接口发送卡片消息；按告警编号加状态去重，避免重复发送。接入过程见 5.7 | P0 |

- 工程结论：已实现官方 SDK 自有应用凭据发送卡片、按告警/状态/群的稳定 UUID 和持久通知审计去重。
- 实现/行为证据：`internal/feishu/client.go`、`internal/notify/sender.go`；`TestSendAndCreateUseOfficialSDKAndStableUUID`、`TestRetriesUseConfiguredDelaysAndRecordEveryAttempt`、运行时补发/失败恢复测试。
- 条件、反例与未覆盖范围：运行不调用 lark-cli；成功审计不是本人收件。SDK/HTTP 假飞书响应覆盖协议与重试，真实创建应用/授权/卡片到达仍 Pending。
- 本人验收：Pending。

### N2

原文（PRD.md 第 97 行）：

> | N2 | 飞书自定义机器人 Webhook | 不需要创建应用，只填一个 Webhook 地址；不能自动列出群和检查权限 | P1 |

- 工程结论：已实现飞书自定义机器人 Webhook，独立加密 URL/签名密钥，支持模板与重试审计。
- 实现/行为证据：`internal/notify/webhook.go`、`internal/app/notifications_webhook.go`、配置/HTTP/通知测试。
- 条件、反例与未覆盖范围：不提供群发现和应用权限检查；不跟重定向，错误不回显地址/响应正文；发送和收件是两个结论，真实群 Pending。
- 本人验收：Pending。

### N3

原文（PRD.md 第 98 行）：

> | N3 | Windows 通知 | 监控器所在电脑弹出系统通知 | P0 |

- 工程结论：已实现 Windows Toast 渠道和可见的成功/跳过/失败结果。
- 实现/行为证据：`internal/notify/toast*.go`、`internal/app/notifications_toast.go`；`TestToastXMLIsTextAndUsesBoundedDisplay`、`TestToastSkipIsLoggedAndFailureRemainsVisible`、`TestToastHTTPTestUsesConfigScopeAndReportsSkip`。
- 条件、反例与未覆盖范围：非交互/无可用桌面不虚报送达；原生实现与注入接口分别验证。本人在实际桌面看见通知仍 Pending，测试不弹真实用户通知。
- 本人验收：Pending。

### N4

原文（PRD.md 第 99 行）：

> | N4 | 通用 Webhook | 对接其他系统 | P1 |

- 工程结论：已实现通用 JSON Webhook、版本化 payload、Standard Webhooks 签名及稳定事件 ID。
- 实现/行为证据：`internal/notify/webhook.go`、`webhook_sender.go`、`schemas/webhook-payload.schema.json`；`TestStandardWebhooksPublicReferenceVectorAndReceiver`、`TestJSONWebhookStatusRetryMatrixAndNoRedirect`、payload/大小/取消测试。
- 条件、反例与未覆盖范围：重试刷新签名时间但保持事件身份；有超时、响应读上限和重定向禁用，原始响应不写日志。只向本地测试接收端发送，真实外部接收端 Pending。
- 本人验收：Pending。

### N5

原文（PRD.md 第 100 行）：

> | N5 | 飞书双向 | 卡片上的“确认”“静音 1 小时”按钮直接生效，需要接收飞书的卡片回调 | P2 |

- 工程结论：已实现官方长连接接收固定“确认”“静音1小时”动作，绑定实际发送记录、群/成员授权和持久去重。
- 实现/行为证据：`internal/app/card_actions.go`、`card_action_stream.go`；`TestNativeCardActionsMonitorLifecycleAndCommittedEffect`、`TestCardActionsActualNotificationFallbackLateUpdateAndKeyFailure`、动作鉴权/重放/撤销测试。
- 条件、反例与未覆盖范围：按钮只针对卡片代表的精确告警实例，模板降级仍保留；过期/伪造/未授权/撤销拒绝。M2-06 明确群成员授权不能经 MCP 任意授予。真实飞书点击、本人群授权和订阅配置 Pending。
- 本人验收：Pending。

### N6

原文（PRD.md 第 101 行）：

> | N6 | 路由 | 按严重程度、主机、标签发给不同的群或人 | P1 |

- 工程结论：已实现渠道 filter 按严重程度、采集器 ID/主机名及目标标签路由，可对多群/个人 open_id 渠道。
- 实现/行为证据：`internal/config/notifier_filter.go`、`internal/app/notification_delivery.go`；filter 配置与 `TestNotificationFiltersUseCurrentConfigBeforeSendAndReplay`、`TestPersonalCardsUseOfficialOpenIDAndKeepChatUUIDCompatible`、`TestPersonalNotifierHTTPMCPShareDestinationLimitAndReadScope`、`TestPersonalRuntimeRoutesAndSuppressesAReassignedRecipient`。
- 条件、反例与未覆盖范围：发送前按当前配置复查，排队后更改路由不向旧目的地发送；空/错误 filter 验证与排除分支有覆盖。openId/chatId 二选一，群 UUID 保持兼容，个人增加类型隔离；个人不能启用群动作。目的地必须本人确认，不自动查通讯录或真实发信；重分配收件人后抑制旧排队消息。
- 本人验收：Pending。

### D1

原文（PRD.md 第 107 行）：

> | D1 | 内置总览：所有主机、在线状态、正在触发的告警、各监控目标是否健康 | P0 |

- 工程结论：已实现总览主机、在线/离线、告警和目标健康，并显示无数据/错误而非伪造正常。
- 实现/行为证据：`web/src/Overview.tsx`、`internal/api/routes.go`、SSE；`TestNativeWebPanelBrowser` 及前端总览/实时更新测试。
- 条件、反例与未覆盖范围：首次加载、断线后重读快照、慢订阅者断开均覆盖；浏览器只读自动会话不能改配置。真实手机/跨机连通仍 Pending。
- 本人验收：Pending。

### D2

原文（PRD.md 第 108 行）：

> | D2 | **声明式面板**：一个 JSON 文件描述一页面板，包括数值卡、折线图、状态格、表格、日志流、告警列表，每块配一条查询。Agent 写文件，面板热更新 | P0 |

- 工程结论：已实现声明式面板与独立 SQL，包含原文六种卡片，并额外支持 bar。
- 实现/行为证据：`internal/config/dashboard.go`、`internal/app/api_dashboards.go`、`web/src/DashboardPanel.tsx`；`TestDashboardReloadAndSnapshotsRetainTheLastValidDefinition`、`TestNativeDashboardBrowser`、各卡片前端用例。
- 条件、反例与未覆盖范围：REVIEW M1-15 明确固定 dashboards/<id>.yaml 文件名；内容可以是 JSON 对象，YAML 解析器兼容 JSON，不能把 .json 扩展名当可加载文件。每卡错误隔离，坏文件保留前一成功版本；查询只读/有界，不能执行任意 SQL 写入。
- 本人验收：Pending。

### D3

原文（PRD.md 第 109 行）：

> | D3 | **自定义 HTML 面板**：Agent 直接写 HTML/JS，通过内置的 `lgmon.js` 调用查询和实时订阅，可以做出任何形式（大屏、拓扑图、时间线）。在隔离的 iframe 里运行，只能读数据 | P1 |

- 工程结论：已实现独立 HTML 文件、内置 lgmon.js 的只读 query/subscribe，通过父页代理在隔离 iframe 中运行。
- 实现/行为证据：`internal/config/dashboard_html*.go`、`web/src/HTMLDashboard.tsx`、`html-dashboard.ts`；`TestNativeHTMLDashboardBrowser` 和 SDK/沙箱前端用例。
- 条件、反例与未覆盖范围：sandbox 不含 same-origin；父页校验 source/origin/request ID，令牌不进 iframe；禁网、导航、表单/弹窗，取消/并发/重载清理。源码以认证 JSON 读取，不作为同源 HTML 发布；不能把 UI 隔离描述成允许任意本机文件读取。
- 本人验收：Pending。

### D4

原文（PRD.md 第 110 行）：

> | D4 | 手机浏览器可看，支持深浅色 | P0 |

- 工程结论：工程实现与浏览器夹具已覆盖手机宽度及深浅主题。
- 实现/行为证据：`web/src/theme.css`、`style.css`、各页面响应式样式；原生 Edge 总览、面板、团队、通知模板、配置/诊断浏览器用例与页面截图。
- 条件、反例与未覆盖范围：工程截图使用本机浏览器的移动 viewport；本人手机、具体设备和 Tailscale 访问仍 Pending。不能以截图代替真人可用性结论。
- 本人验收：Pending。

### D5

原文（PRD.md 第 111 行）：

> | D5 | 对外导出：Prometheus 格式和 OTLP，方便接入 Grafana 等现有工具 | P2 |

- 工程结论：已实现受鉴权的 Prometheus text 0.0.4 与默认关闭的 OTLP Metrics/HTTP protobuf 导出。
- 实现/行为证据：`internal/app/exports.go`、`exports_otlp*.go`、`docs/exports.md`；持久指标映射、Prometheus 解析及 OTLP 真实回环接收/TLS/mTLS/重试/取消测试。
- 条件、反例与未覆盖范围：导出来源为持久当前事实，不暴露凭据/命令行/任意文件；过期数据不伪装当前。OTLP 发送有超时、重试/退避与队列上限，配置切换取消旧请求；真实第三方平台接入 Pending。
- 本人验收：Pending。

### D6

原文（PRD.md 第 112 行）：

> | D6 | **团队共享面板**：在自己的监控器上建团队，用邀请码邀请内网里同样装了 lgmon 的人加入。负责人把声明式面板分享给团队或团队里的指定成员，可以只分享部分卡片，并限定只显示部分主机或监控目标。成员实时查看负责人监控器上的数据，不能修改，也不能再分享。不加入团队的人照常单机使用，可以建自己的面板 | P1 |

- 工程结论：已实现负责人自建团队、设备邀请、成员实时读取及卡片/主机/目标/成员范围；CLI/HTTP/MCP 同权限。
- 实现/行为证据：`internal/app/teams.go`、`internal/transport/team.go`、`internal/store/teams*.go`、`web/src/Teams.tsx`、`SharedTeams.tsx`；`TestTeamHTTPFixtureLifecycleScopesAndIdentityBoundaries`、`TestTeamRevocationCancelsInFlightRows`、`TestNativeTeamsBrowser`、MCP 双节点全工具测试。
- 条件、反例与未覆盖范围：邀请默认 24h、一次兑换、核对 16 位指纹；设备 mTLS 与负责人 API 权限分离。成员不能提交 SQL、改/再分享；范围/成员/分享撤销立即取消在途读，revision 冲突不覆盖。未加入者仍可本地看板；真实本人分享仍 Pending。
- 本人验收：Pending。

### G1

原文（PRD.md 第 120 行）：

> | G1 | 配置即文件 | 监控目标、探测、规则、面板、通知都是独立的 YAML/JSON 文件，带 JSON Schema。Agent 可以直接写文件，监控器校验后热加载，失败时报出具体哪行错了 | P0 |

- 工程结论：已实现配置文件、生成 Schema、逐行错误、校验后热加载及失败保留旧版本。
- 实现/行为证据：`internal/config/load.go`、`manager.go`、`watch.go`、`documents.go`、`schemas/`；`TestReloadRetainsLastValidConfiguration`、`TestWatchRejectsInvalidThenRecovers`、`TestDocumentEditorValidatesBeforeWritingAndPublishesWholeConfig`。
- 条件、反例与未覆盖范围：固定 YAML 文件路径允许 JSON 文本；不改变现有结构或命名。settings 中本机信任/授权及采集周期的重启语义分别遵守约定，不宣称所有字段都可远程热改；密钥仅引用。
- 本人验收：Pending。

### G2

原文（PRD.md 第 121 行）：

> | G2 | CLI | `lgmon` 命令全部支持 `--json`，覆盖查询、增删配置、试运行规则、发测试告警 | P0 |

- 工程结论：已补齐 CLI --json 与配置 get/put/delete；查询、试运行、通知测试和既有管理命令均可机器读取。
- 实现/行为证据：`cmd/lgmon/prd_test.go` 的 `TestEveryCommandSupportsJSON`、`TestCLIJSONActualPayloadsAndErrors`；`config_documents_test.go` 实际 HTTP CRUD、无关字段保留、无效/超限输入拒绝和错误行号；真实 stdio/安装/团队 CLI 测试。
- 条件、反例与未覆盖范围：version/帮助/模板/托盘/Schema 使用各自 JSON 对象，run notice 为 NDJSON、日志在 stderr；MCP stdout 保留 JSON-RPC。所有 runnable 命令（含 help）穷举；已报告错误不再叠加文本或第二个 JSON。config put 整体替换，无关字段须先读保留，继承既有 API 并发语义。
- 本人验收：Pending。

### G3

原文（PRD.md 第 122 行）：

> | G3 | HTTP API | REST + OpenAPI 描述，实时数据走 SSE | P0 |

- 工程结论：已实现 REST、从同一路由模型生成的 OpenAPI 3.1 和 SSE。
- 实现/行为证据：`internal/api/routes.go`、`server.go`、`stream.go`；`TestEveryRouteHasCorrectPermissionAndOpenAPIOperation`、`TestSSEPublishesAllEventKindsAndReleasesCancelledSubscriber`、`TestSSEStopsWhenPersistentTokenIsRevoked`。
- 条件、反例与未覆盖范围：校验未知字段/请求大小、Host/Origin 和 Bearer 请求头；无令牌拒绝、不从 URL 收令牌，撤销后停止流。HTTP 管理和浏览器链路按 PLAN 网络安全约定，不误称所有浏览器连接均为 mTLS。
- 本人验收：Pending。

### G4

原文（PRD.md 第 123 行）：

> | G4 | 只读 SQL | 开放 SQL 查询，提供表结构说明。这是自由度最高的部分：任何“我想看 X”都可以先变成一条查询 | P0 |

- 工程结论：已实现单条只读 SQL、表/视图/列说明、带绑定参数的查询及 CLI/API/MCP 同源结果。
- 实现/行为证据：`internal/store/query.go`、`rule_query.go`、`internal/api/routes.go`；只读写入/多语句/超时取消、authorizer/CTE/内部表拒绝测试，MCP get_schema/query_sql 和浏览器查询用例。
- 条件、反例与未覆盖范围：查询限时 5s、最多 10000 行；授权视图/保留范围与实时/历史时钟分别处理。不能访问密钥、内部认证表、文件系统或加载扩展；可移植数据类型与空值保留。
- 本人验收：Pending。

### G5

原文（PRD.md 第 124 行）：

> | G5 | MCP 服务 | 把以上能力包装成 MCP 工具，Claude Code、Codex 等可以直接调用 | P0 |

- 工程结论：已实现官方 MCP SDK 的 stdio 和 Streamable HTTP；本次补为 56 工具并逐个调用真实 HTTP 后端。
- 实现/行为证据：`internal/api/mcp.go`、`mcp_completion.go`、`cmd/lgmon/mcp.go`；`TestMCPAllDefinedToolsUseRealHTTPBackend`、`TestMCPScopesValidationAndSharedNotificationLimit`、`TestMCPCommandRunsRealStdioAndUsesOnlyHTTPToken`。
- 条件、反例与未覆盖范围：同一 API token/scopes，不提升本机 read 会话；App Secret、Webhook 凭据、本机操作授权、REVIEW M1-17 全局暂停、M2-06 群成员授权为明确人工边界；配置面板由 Agent 写受校验文件，SSE/HTML 订阅走原 HTTP。测试/服务重启/团队分享工具明示影响和所需本人意图。
- 本人验收：Pending。

### G6

原文（PRD.md 第 125 行）：

> | G6 | 说明文档与技能 | `AGENTS.md`、一键安装到 Claude Code 和 Codex 的技能，内含常见任务配方 | P0 |

- 工程结论：已实现使用说明、Codex/Claude Code 一键安装技能与自包含 prompt，涵盖常见监控/规则/通知/模板/面板/诊断配方。
- 实现/行为证据：`docs/agent-guide.md`、`cmd/lgmon/agent.go`、`agent-skill.md`；`TestAgentInstallBothTargetsAndRefreshesOnlyItsOwnFile`、`TestAgentInstallPreflightsAllTargetsAndPreservesForeignSkill`、`TestAgentPromptIsSelfContainedPreservesWishAndDoesNotInstall`。
- 条件、反例与未覆盖范围：仓库 AGENTS 面向开发，产品 Agent 使用 guide，分工写明；技能只改自己的文件，冲突/部分失败可见。工程测试写 TEMP 下虚构用户目录；本人全新 Agent 实际完成任务仍 Pending。
- 本人验收：Pending。

### G7

原文（PRD.md 第 126 行）：

> | G7 | 权限 | API 令牌分为只读、改配置、执行操作三档 | P0 |

- 工程结论：按 PLAN 三档 read/config/admin 实现；执行操作属于 admin，仍需采集器本机逐条授权。
- 实现/行为证据：`internal/api/auth.go`、`token.go`、路由权限表；`TestDeclaredScopeHierarchyAndHeaderOnlyAuthentication`、`TestLocalSessionCannotMutateAnyRoute`、MCP read/config 拒绝执行及团队 admin 权限测试。
- 条件、反例与未覆盖范围：本机回环不等于管理员；自动会话仅 read，撤销立即生效。admin 不能替代本机服务授权、不能通过 settings 扩大监控器信任或远程操作权；任何新路由未声明权限即注册失败。
- 本人验收：Pending。

### F1

原文（PRD.md 第 134 行）：

> | F1 | 填写凭证 | 验证凭证，把 Secret 加密保存；没有应用时给出创建步骤 | 粘贴 App ID 和 App Secret | P0 |

- 工程结论：已实现本人输入、在线验证成功后加密保存凭证及无应用时创建引导，CLI/Web 共用机器流程。
- 实现/行为证据：`internal/feishu/setup/machine.go`、`internal/secrets/`、`web/src/FeishuSetup.tsx`；`TestCredentialsValidationDoesNotPersistFailures`、`TestHTTPBackendFeishuCredentialsAreWriteOnlyAndTestRepliesArePrivate` 和原生秘密存储测试。
- 条件、反例与未覆盖范围：DPAPI/受保护文件只存引用；失败验证不落凭据，HTTP Secret 只写、页面提交后清空，不进入 Agent prompt/日志。真实 App 凭据和首次接入仍本人 Pending。
- 本人验收：Pending。

### F2

原文（PRD.md 第 135 行）：

> | F2 | 机器人能力 | 检查应用是否启用了机器人，并读出机器人名称 | 没启用时按提示在开发者后台开启 | P0 |

- 工程结论：已实现机器人能力检查和机器人名称读取，缺能力时给开发者后台指引。
- 实现/行为证据：`internal/feishu/client.go` 的 BotInfo 和 setup machine；`TestDisabledBotStatusAndStructuredMissingScope`、`TestBotPermissionsAndLateChatBranches`、原生向导浏览器用例。
- 条件、反例与未覆盖范围：实际失败/未发布/权限缺失不当作已开启；使用假 SDK 响应验证分支。真实后台启用和发布由本人完成，不在本轮修改应用。
- 本人验收：Pending。

### F3

原文（PRD.md 第 136 行）：

> | F3 | 权限 | 读出已开通的权限，对比所需权限，生成飞书官方的一键开通链接，并自动检测开通结果 | 点开链接，一键开通；需要时发布新版本 | P0 |

- 工程结论：已实现读取实际 scopes、差异/官方申请链接、重复检测和需发布版本的提示。
- 实现/行为证据：`internal/feishu/client.go`、`internal/feishu/setup/`；`TestScopeParsingAndApplyURL`、`TestReadOnlyChecksAndUnknownChatCount`，CLI/Web 同流程及结构化失败测试。
- 条件、反例与未覆盖范围：不把“点击链接”当权限生效，不自动修改应用；真实读取事实见 PLAN“已核实的飞书事实”，现有假应用 ID 只用于测试。本人授权/发布和真实自动检测 Pending。
- 本人验收：Pending。

### F4

原文（PRD.md 第 137 行）：

> | F4 | 告警群 | 列出机器人所在的群，自动拿到群 ID；机器人还不在群里时给出拉群步骤，并自动发现新加入的群；可选“帮我新建告警群” | 把机器人拉进群，选一个群 | P0 |

- 工程结论：群列表、翻页、拉机器人指引和新群发现已实现；可选“帮我新建群”按 PLAN M0-11 的明确退路未提供。
- 实现/行为证据：`internal/feishu/client.go`、`setup/machine.go`；`TestChatPaginationAndEmptyRealCLIData`、`TestBotPermissionsAndLateChatBranches`、`TestReadOnlyChecksAndUnknownChatCount`；PLAN M0-11 执行记录“人数与建群的处理”。
- 条件、反例与未覆盖范围：缺少当前操作者身份，CreateChat 需要人的 open_id；现行计划允许只保留拉机器人路径，未用机器人 ID 冒充人或擅加身份/通讯录权限。列表显示名称、ID 不需用户填写；人数未知不伪造为 0。真实拉群/新群发现 Pending。
- 本人验收：Pending。

### F5

原文（PRD.md 第 138 行）：

> | F5 | 模板与测试 | 选默认模板，发测试卡片 | 确认群里收到 | P0 |

- 工程结论：已实现模板选择、已有或未保存模板测试、明确“本人收到后保存”的流程。
- 实现/行为证据：`internal/api/routes.go` 的 /feishu/test 和 /feishu/notifiers、`internal/app/api_feishu_test.go`、模板/向导浏览器用例；MCP feishu_test_chat/feishu_save_chat 同合同。
- 条件、反例与未覆盖范围：同一目的地 10s 限频；未保存草稿不落盘，格式错不发送，收件确认来自人而不是 API 成功。测试只用假客户端，真实收件仍 Pending。
- 本人验收：Pending。

> **卡片模板**：

### K1

原文（PRD.md 第 146 行）：

> | K1 | 四套预设：标准、简洁、排查（带进程、日志末尾、诊断文本）、汇总（多条告警合并），各有触发和恢复两种样子 | P0 |

- 工程结论：四套预设及触发/恢复格式已实现；digest 渲染多项列表，最多 20 项后显示余量。
- 实现/行为证据：`internal/notify/cards/presets/`、`render.go`、`layout.go`；`TestPresetGoldens`、`TestPresetSemantics`、多项/超限渲染与 `TestQueuedFourthTriggerRendersOneDigestWithItsEventIdentity`；本轮 `TestDigestContainsOnlyRealRecentGroupOccurrencesAndExactTotal`、`TestSelectedDigestSendsRealOccurrencesBeforeFlappingThreshold`、`TestBoundedRealDigestUsesExactTotalForRemainingRows`。
- 条件、反例与未覆盖范围：本轮补齐真实同组最近10分钟发生记录，最多20条并显示精确剩余数量；抖动第四次触发以及明确选 digest 均有实际生产投递的多项证据。组按规则/主机/目标/SQL实例隔离，不扩大收件范围；读取失败保留当前真实告警，不丢通知。发送仍按当前单事件身份，无新增延迟，不把样例卡片当收件。
- 本人验收：Pending。

### K2

原文（PRD.md 第 147 行）：

> | K2 | 在预设基础上自定义：改标题、颜色、字段及顺序、附加字段、@ 谁、按钮、底部备注 | P0 |

- 工程结论：已实现基础模板继承、标题/颜色/字段顺序/附加字段、白名单 mentions、链接按钮和底部备注。
- 实现/行为证据：`internal/config/template_json.go`、`internal/notify/cards/layout.go`、`template.go`；`TestEscapingAndOnlyConfiguredMentions`、继承/热加载/字段/助手函数测试和基础编辑器用例。
- 条件、反例与未覆盖范围：正则/模板大小/执行成本、未知字段、嵌套 range 与递归受限；动态文本转义，只有配置的真实 mentions 生效。M2-06 固定回调按钮由运行时附加，不允许模板伪造动作授权。
- 本人验收：Pending。

### K3

原文（PRD.md 第 148 行）：

> | K3 | 完全自定义：直接写卡片 JSON，可以先在飞书卡片搭建工具里设计好再导入 | P0 |

- 工程结论：已实现 raw 卡片 JSON 导入、模板变量、对象/JSON 字符串转换和高级编辑预览。
- 实现/行为证据：`internal/config/template_json.go`、`internal/notify/cards/`、`web/src/Notifications.tsx`；`TestRawObjectStringAndStateFallback`、高级编辑/导入前端与原生通知浏览器测试。
- 条件、反例与未覆盖范围：JSON 结构/输出大小及安全模板语法必须通过；状态缺失走约定回退，原始 JSON 仍接受固定回调的运行时附加。未访问或修改真实飞书卡片搭建应用。
- 本人验收：Pending。

### K4

原文（PRD.md 第 149 行）：

> | K4 | 每个群可以有默认模板，单条规则可以指定模板 | P0 |

- 工程结论：已实现渠道默认模板和单条规则 override，规则优先。
- 实现/行为证据：`internal/config/notifier.go`、`rule_notifications.go`、卡片 Renderer 的模板选择；`TestHotReloadAndInheritance`、通知队列/模板试发/渠道保存测试。
- 条件、反例与未覆盖范围：渠道或规则引用不存在模板时校验拒绝；删除仍被引用的模板保留旧文件。队列捕获对应配置，发送前按当前路由与暂停策略复查。
- 本人验收：Pending。

### K5

原文（PRD.md 第 150 行）：

> | K5 | 自定义模板出错时自动退回标准模板，告警绝不能因为模板发不出去 | P0 |

- 工程结论：已实现渲染失败、缺模板、超限及飞书明确卡片格式错误的降级。
- 实现/行为证据：`internal/notify/cards/render.go`、`internal/notify/sender.go`、运行时通知代码；`TestTemplateFailureFallbackAndStrictValidation`、`TestSizeReduction`、服务器拒绝后标准模板回退测试。
- 条件、反例与未覆盖范围：先 standard、超长 compact，保留同一事件幂等键/审计与固定回调；网络/凭据错误不会错误地换模板宣称成功。尽管模板能降级，外部平台不可达仍保留失败，不能承诺消息绝对送达。
- 本人验收：Pending。

### K6

原文（PRD.md 第 151 行）：

> | K6 | 网页上的模板库、预览和可视化编辑 | P1 |

- 工程结论：已实现网页模板库、样例预览、基础可视化编辑和高级 JSON/YAML 编辑。
- 实现/行为证据：`web/src/Notifications.tsx` 与模板编辑组件；前端通知/模板测试、`TestNativeFeishuWizardBrowser` 和原生通知模板浏览器用例。
- 条件、反例与未覆盖范围：未保存模板可预览/试发，测试和保存状态分开；只读/配置/admin 权限各有限制，错误不覆盖草稿或上次有效文件。本人确认真实卡片渲染和收件仍 Pending。
- 本人验收：Pending。

## 未编号需求、非功能与已定事项

### U-01 目标、用户与使用场景

> ## 1. 一句话
>
> 一个装在 Windows 上的服务监控软件：**采集器**盯住电脑上的进程、服务、端口和网站，**监控器**汇总多台电脑的数据、出面板、发告警。告警走飞书。所有能力都开放给编程 Agent，让 Agent 能按需求搭出不同形式的监控面板和告警规则。
>
> ## 2. 为什么做
>
> - 现成工具各管一块：Uptime Kuma 只管网页能不能访问，Netdata 只看系统资源，Autoruns 只看启动项，1Panel 和宝塔主要面向 Linux。没有一个在 Windows 上把进程、服务、端口、网站、开机项和告警串起来。
> - 现成工具的面板是固定的。用户真正需要的是“我的博客和它依赖的数据库放一屏”“三台电脑的 Tunnel 放一屏”，这种组合应该能让 Agent 现场搭出来，而不是在 UI 里一个个拖。
> - 用户已经有多台电脑，通过 Tailscale 组网，也已经有飞书，缺的是把它们连起来的那一层。
>
> ## 3. 用户
>
> | 用户 | 场景 | 最在意 |
> |---|---|---|
> | 你（主力用户） | 一台电脑常驻跑网站、Tunnel 和数据库，在另一台电脑或手机上关心它们是否活着 | 挂了马上知道；告诉 Agent 一句话就能加监控、改面板 |
> | 小白（自己也会用 Agent） | 自己电脑上跑着几个服务，不懂运维 | 装上就能用；告警说人话；出问题能一键交给 Agent 排查 |
> | 团队成员（M2） | 在内网里查看别人分享的监控面板，自己不一定配置监控 | 输入邀请就能看；只看到被分享的那部分 |
>

- 工程结论：产品提供统一采集、故障判断、通知和 Agent 配置入口，支持单机、远端主机和团队成员。
- 实现/行为证据：本表 C/T/A/N/D/G/F/K 全部编号；README、docs/agent-guide.md 及网页总览/团队/诊断流程；安装/飞书向导和 Agent prompt 使用自建数据验证。
- 条件、反例与未覆盖范围：“装上就能用”和“马上知道”的工程路径与时限分别落到安装、配对、15s 和通知项；真实新手使用/新 Agent 独立完成任务不能由代码或假客户端推定。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### U-02 一个安装包、单 exe、两角色与默认单机

> ## 4. 产品形态
>
> **一个软件，两种角色。** 只有一个安装包和一个 `lgmon.exe`，安装时选角色，也可以在同一台机器上同时开启两个角色。
>
> ```
>  电脑 A（被监控）                         电脑 B（看面板、收告警）
> ┌───────────────────────┐             ┌──────────────────────────────┐
> │ 采集器（Windows 服务） │  gRPC+mTLS  │ 监控器                         │
> │  · 进程 / 服务 / 端口  │ ──────────▶ │  · 存储（SQLite 时序）         │
> │  · HTTP / TCP 探测    │  采集器主动  │  · 告警规则引擎                │
> │  · 自定义脚本探测      │  连出去      │  · Web 面板 + 托盘             │
> │  · 离线缓存 24h       │ ◀────────── │  · HTTP API / CLI / MCP        │
> └───────────────────────┘  下发配置    │  · 通知：飞书应用 / Webhook     │
>                                       └──────────────┬───────────────┘
> 电脑 C、D …… 同样装采集器                              │ 飞书开放平台接口
>                                                       ▼
>                                                    飞书群 / 私聊
> ```
>
> - **采集器**：以 Windows 服务运行，开机就启动，不用等登录。只负责采集、探测、缓存和上报，不存长期数据，也不做判断。资源占用要小到可以忽略。
> - **监控器**：常驻程序加托盘图标，内置 Web 面板，局域网或 Tailscale 内的其他设备（包括手机浏览器）也能打开。负责存储、规则判断、告警、对外 API。
> - **单机模式**：一台电脑同时跑两个角色，走本机回环，用同一套协议。小白默认用这个模式。
>

- 工程结论：同一个 lgmon.exe 支持 collector/monitor/both，安装包默认 both；both 仍使用本机同一 gRPC+mTLS 通道。
- 实现/行为证据：cmd/lgmon/run.go、install.go；internal/app/local.go、remote.go；原生 both/双节点/多监控器测试；scripts/build-installer.ps1 与同源 Inno TEMP 服务夹具（docs/m2-09-installer-results.json）。
- 条件、反例与未覆盖范围：collector 不启动 HTTP/团队监听、规则或长期监控库；outbox 为有限缓存。monitor 托盘使用系统浏览器；服务无交互桌面时用独立托盘进程。真实服务安装/开机未登录和托盘仍 Pending，本轮未执行。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### U-03 采集频率与变化量

> 采集频率：监控目标 2 秒一次，完整进程表 15 秒一次，系统指标 5 秒一次，都可以配置。数据按变化量上报，没有变化就不重复发送。

- 工程结论：本轮补齐本机 collection 四周期；默认 target/process/system 为原文 2s/15s/5s，服务全表 10s。
- 实现/行为证据：internal/config/collection.go、schemas/settings.schema.json；TestCollectionIntervalsDefaultsConfigurationAndFieldLocations、TestConfiguredTargetPeriodAndIndependentPrivateProcessRefresh；原生真实生产子进程 target=500ms、system=1s 经 mTLS/SQLite 验证。
- 条件、反例与未覆盖范围：本机重启生效，目标 500ms～5s，其余 1s～1m，整毫秒。目标状态变化量上报并保留 10s 恢复全量；数值指标是时序采样，不把相同数值当作无数据；全进程私有缓存可被目标/ETW 提前刷新，不周期远传未匹配全表或命令行。历史 interval_ms 记录配置周期，ETW 可额外即时采样。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### U-04 Agent 能力目标

> 目标：**Agent 能做的事不少于人在界面上能做的事**，而且第一次就能做对。

- 工程结论：文件/CLI/HTTP/MCP 覆盖已开放产品能力，本轮补齐配置 CRUD 和同权限团队工具。
- 实现/行为证据：G1～G7、MCP 56 工具双通道逐个实调、CLI runnable 命令 --json 穷举和实际输出/错误验证。
- 条件、反例与未覆盖范围：明确的人类边界为 Secret/凭据输入、本机服务逐条授权、REVIEW M1-17 全局暂停和 M2-06 群动作授权。Agent 能交付面板文件并用只读工具校验；没有本机文件权限时不得宣称已保存。本人意图与 API 权限不是同一概念，不自行创建真实分享/重启/发信。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### U-05 同一飞书接入流程与本人密钥输入

> **接入**：用户只需要提供飞书自建应用的 App ID 和 App Secret，剩下的由程序带着走完。
> 用户全程不需要知道“群 ID”“scope”这些概念。命令行（M0）和网页（M1）走同一套逻辑，Agent 也能通过 CLI 或 MCP 带着用户走完，但 App Secret 只能由用户本人填写。

- 工程结论：CLI 与 Web 调用同一 setup 机器流程；Agent 只读检查/引导并在明确授权后试发。
- 实现/行为证据：internal/feishu/setup/machine.go、cmd/lgmon/feishu.go、web/src/FeishuSetup.tsx；F1～F5 及向导分支/浏览器测试。
- 条件、反例与未覆盖范围：界面以群名称和模板选择为主，不要求用户手写 chat ID 或 scope。当前按 M0-11 退路只支持拉机器人，不提供建群；F4 原句差异已明示。App Secret 由人本人填写，不索取到聊天/Agent 工具。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

> | 项目 | 目标 |

### NFR-01 资源

> | 采集器资源 | 平均 CPU < 1%，内存 < 40 MB |

- 工程结论：短期生产子进程夹具达到 CPU<1%/RSS<40MiB，完整 24h 尚 Pending。
- 实现/行为证据：M3-03 既有 CI；本轮聚焦 30.0299647s 平均 CPU 0.7978%、最大 RSS 37748736、private commit 78311424；internal/app/collector_resources_windows.go、TestNativeMultiMonitorIsolationReplayRevocationAndResources。
- 条件、反例与未覆盖范围：首个 10s 窗口 1.0016% 曾失败，保留于 PLAN，未放宽阈值；30s 增加全部样本，仍不是 24h 证据。collector 角色有 36MiB 工作集预算/默认最多 2 Go P，工作集回收的换页成本另列于文档；私有提交不是 RSS，不能用较小专用集冒充。M1-19 用 A 机自监控原始含共享页 RSS/时间加权 CPU、覆盖和单身份口径。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-02 故障发现

> | 发现故障 | 监控目标退出后 5 秒内发现（P2 的事件模式下 1 秒内） |

- 工程结论：周期采集默认 2s；ETW 自建子进程退出到持久状态小于 1s。
- 实现/行为证据：原生 local/remote/process_events 与多监控器测试；本轮同源生产子进程退出落库 519.2118ms。
- 条件、反例与未覆盖范围：时限从测试自建进程的实际退出计时；事件只是提示，随后核对状态。ETW 失败有明确回退与 2s 轮询，不能保证回退仍为 1s；配置 target 最大 5s 且调度/提交有开销，5s 原文验收采用默认周期。真实服务验收 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-03 飞书15秒

> | 告警到达 | 故障发生后 15 秒内到达飞书 |

- 工程结论：自建原生故障链路到假飞书成功审计在 15s 内，真实到达尚 Pending。
- 实现/行为证据：internal/app/local_integration_windows_test.go、remote_integration_windows_test.go、docs/acceptance-m0.md、acceptance-m1.md 和两份只读准备脚本。
- 条件、反例与未覆盖范围：依赖正常网络/平台响应、配置 for 和重试策略；不能用 HTTP 200/假 SDK 成功当真人群内到达。真实群只在本人明确确认后试发，本轮未发。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-04 24h缓存

> | 离线缓存 | 采集器断网缓存至少 24 小时 |

- 工程结论：默认 500MB 在既定 50 目标/2s 标准形状下容量夹具有超过 24h 余量。
- 实现/行为证据：docs/acceptance-m1.md 的 M1-22 计量；真实 SQLite/WAL 10min 固定输入，累计确认/淘汰写入也计速率；剩余小时数与暖机/低速状态经 CLI/HTTP/网页核验。
- 条件、反例与未覆盖范围：24h 容量预测不是实际 24h 断网。更快采样、脚本输出/多监控器槽位会增加容量，按每槽 bytesPerHour/remainingHours 评估，预算总和单列；满/超龄按已定 gap 策略，不能承诺无限不丢。真实离线 24h 可选人工 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-05 20×50规模

> | 规模 | 一个监控器承载 20 个采集器、每台 50 个监控目标 |

- 工程结论：已有正式 30min 20×50 负载夹具，以及完整覆盖/提交时延/待处理量门禁。
- 实现/行为证据：docs/m1-22-scale.json、acceptance-m1.md；TestScaleTwentyCollectorsFiftyTargets、TestScaleCommitTimingPreservesFullCoverage、TestScaleBacklogMeasurementRejectsUndrainedSteadyMinute。
- 条件、反例与未覆盖范围：正式数据为 synthetic 采样、真实 monitor 子进程/mTLS/SQLite 和只读计量，约定稳态分钟需观察队列清空；不代表 20 台真实 Windows 主机或 50 任意脚本负载。基础 integration 标签不包含 scale；本轮另用 scale 标签单独运行90s快速规模和提交覆盖分支，原正式30min历史证据保留，不能混称同一次测试。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-06 保留

> | 数据保留 | 原始数据 24 小时，分钟级 14 天，小时级 1 年，可配置 |

- 工程结论：默认 raw=24h、minute=14d、hour=365d；可配置并在后台增量汇总和清理。
- 实现/行为证据：internal/store/maintenance.go、migrations/007_rollups.sql、internal/config/settings.go、retention 配置；TestAggregationWaitsForBucketEndAndPreservesRawBoundaryBucket、TestTenThousandWritesDuringAggregationCommitWithinTwoSeconds、TestRuntimeAppliesInitialAndReloadedRetention。
- 条件、反例与未覆盖范围：raw≥24h 且 raw≤minute≤hour（Claude 明确补充）；桶存 count/sum/max，小时从分钟加权合成；清理截止对齐整点、只增水位，原始已清理的迟到数据丢弃并记 late_dropped，不毁已有汇总。未等待真实一年，边界用注入时间和真 SQLite 验证。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-07 单exe安装

> | 安装 | 一个 exe，一条命令或双击完成安装和配对，不需要 Docker、数据库或运行时 |

- 工程结论：同一正式安装包内含独立 Go/嵌入前端 exe，可命令或双击安装/配对。
- 实现/行为证据：scripts/build.ps1、build-installer.ps1、scripts/lgmon.iss、docs/installer.md；同源 Inno 安装/卸载 TEMP 假服务夹具和最终 CI 产物清单。
- 条件、反例与未覆盖范围：程序复制到受保护 Program Files，home ACL 收紧，安装前确认角色/home/指纹，失败回滚且不删除源 home；重复安装不覆盖不匹配身份。工程夹具不安装真实服务；本机无签名证书的安装包标记 unsigned，真实安装仍 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-08 系统

> | 系统 | Windows 10/11 x64；监控器后续可以跑在 Linux 和 macOS 上 |

- 工程结论：Windows x64 构建与 Windows 11 原生夹具通过；平台边界以 build tags/接口分离。
- 实现/行为证据：Go CGO_ENABLED=0、windows amd64 build、原生 collection/SCM/ETW/DPAPI/浏览器与同源安装包测试。
- 条件、反例与未覆盖范围：Win10 实机未在本环境运行，真实 Windows 10 安装/旧 API 行为仍 Pending；Linux/macOS 是“后续”目标，保留实现接口与明确 unsupported fallback，不宣称已交付其他系统安装包。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### NFR-09 安全

> | 安全 | 传输全程 mTLS；不收集文件内容和密码；HTML 面板只读且隔离；远程操作默认关闭 |

- 工程结论：采集/团队数据链路 mTLS、凭据受保护、诊断/SQL/HTML 只读隔离、远程操作默认关闭。
- 实现/行为证据：T2/T6、G3/G4/G7、C2/C7、D3/D6、F1；配对/吊销/ACL/路径/只读 authorizer/iframe/权限和命令行隐私测试。
- 条件、反例与未覆盖范围：“全程 mTLS”落实采集与设备团队链路；初次公开 CA/配对和浏览器 HTTP 管理按 PLAN 分别定义。浏览器默认回环，可走 Tailscale，非安全监听有明示，未擅自开防火墙。只采集声明的元数据/有界事件文本，HTTP正文只本地匹配；不自动读取密码或任意文件内容。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

> | 部分 | 选择 | 理由 |

### TECH-01 Go

> | 语言 | **Go** | 编译成单个 exe，不依赖运行时；网络、并发和服务开发是它的强项；`x/sys/windows` 可以直接调用 Windows API；交叉编译方便，监控器以后能跑在 Linux 上；学习曲线平缓，适合你上手 |

- 工程结论：go.mod 声明 Go 1.27；当前 C:\Program Files\Go\bin 的 1.27.3 按项目要求执行，单 exe 无额外运行时。
- 实现/行为证据：AGENTS 四项门禁与 CGO_ENABLED=0 构建。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-02 进程/系统源

> | 进程与系统指标 | `gopsutil/v4` | 进程、CPU、内存、网络连接，Windows 支持成熟 |

- 工程结论：gopsutil/v4 版本固定；为满足资源门槛，Windows 全进程源已按 M0/M3 执行记录采用批量 NT API，系统指标和对外语义不变。
- 实现/行为证据：internal/collector/process_windows.go、metrics_native.go、source.go；字段/CPU差分/PID重用和原生性能测试。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-03 Windows服务

> | Windows 服务 | `golang.org/x/sys/windows/svc` 和 `svc/mgr` | 采集器自身注册为服务，同时读取 SCM 服务状态 |

- 工程结论：使用 x/sys/windows/svc、svc/mgr 读取和注册自己服务。
- 实现/行为证据：internal/winsvc 与 collector/service_windows.go；只读 SCM 原生夹具/假控制器，真实注册 Pending。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-04 事件/ETW

> | 事件日志、ETW | 直接调用 Windows API（wevtapi）；ETW 用 `go-etw` 一类的库，P1 开发时再定 | |

- 工程结论：Event Log 用 wevtapi；ETW 实现按“开发时再定”采用原生 Windows API，不加需 cgo 的依赖。
- 实现/行为证据：C9/C10，ETW ABI、清理、重订阅、丢失事件和原生退出测试。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-05 传输

> | 传输 | **gRPC（`google.golang.org/grpc`）+ Protocol Buffers，TLS 1.3 双向认证** | 见 7.2 |

- 工程结论：grpc-go 与生成 protobuf、TLS 1.3 mTLS。
- 实现/行为证据：T1～T7 与 proto/lgmon/v1；配对、错误证书、吊销和重连测试。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-06 SQLite

> | 存储 | **SQLite**，驱动 `modernc.org/sqlite`（纯 Go，不需要 C 编译器） | 零部署，单文件，支持 SQL 查询；用分表加汇总表做时序数据 |

- 工程结论：modernc.org/sqlite 纯 Go，WAL/事务/汇总/独立只读连接。
- 实现/行为证据：internal/store 与 transport/outbox；CGO_ENABLED=0 实际构建及真实临时库测试。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-07 规则

> | 规则表达式 | 内置规则类型 + SQL 规则；需要表达式的地方用 `expr-lang/expr` | 不发明新语言，Agent 已经会写 SQL |

- 工程结论：使用内置类型和 SQLite 只读 SQL；目前不需要另加 expr 语言。
- 实现/行为证据：A1/A2/A5，go.mod 未杜撰或加入未用的表达式依赖。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-08 HTTP

> | HTTP 服务 | 标准库 `net/http`（Go 1.22 起自带路由），SSE 推送实时数据 | 少依赖 |

- 工程结论：标准 net/http 路由和 SSE；统一鉴权、限流、错误/OpenAPI。
- 实现/行为证据：G3、internal/api/stream.go 和服务热切换测试。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-09 MCP

> | MCP | 官方 Go SDK `modelcontextprotocol/go-sdk` | |

- 工程结论：官方 modelcontextprotocol/go-sdk，stdio 和 Streamable HTTP。
- 实现/行为证据：G5、真实 JSON-RPC/HTTP 两通道全工具测试。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-10 配置

> | 配置 | YAML/JSON + JSON Schema 校验，`fsnotify` 监听变更后热加载 | |

- 工程结论：YAML 解析兼容 JSON、jsonschema 校验、fsnotify 监听；错误有源文件和行号。
- 实现/行为证据：G1、schemas 与有效快照隔离/失败保留测试。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-11 前端

> | 前端 | **React + TypeScript + Vite**，图表用 **ECharts**，打包后用 `go:embed` 嵌入 exe | ECharts 图表种类全、中文生态好，Agent 也熟悉 |

- 工程结论：React/TypeScript/Vite/ECharts，正式 webui 构建由 go:embed 嵌入。
- 实现/行为证据：web/package.json、internal/app/web_*、脚本 build；前端 build/test 与真实 Edge 夹具，移动/主题见 D4。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-12 桌面

> | 桌面外壳 | 第一版：托盘图标（`fyne.io/systray`）加系统浏览器打开面板；以后视需要换成 Wails（Go 版的 Tauri） | 先不引入 WebView 打包的复杂度 |

- 工程结论：fyne.io/systray 加系统浏览器，不引入另一个 WebView 运行时。
- 实现/行为证据：internal/app/tray_windows.go 与原生不显示/假注册测试；本人托盘仍 Pending。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-13 飞书

> | 飞书 | 官方 Go SDK `larksuite/oapi-sdk-go/v3`，用应用自己的凭证调用开放平台接口；运行时不依赖 lark-cli | 以 Windows 服务运行也能发送；卡片和权限检查都要直接调接口 |

- 工程结论：官方 oapi-sdk-go/v3，用应用自有凭据，不调用 lark-cli 运行。
- 实现/行为证据：N1/F1～F5，SDK 固定依赖与 httptest 接口分支；只读开发事实和本轮无真实发信边界。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-14 安装

> | 安装 | `lgmon install --role collector|monitor|both` 自行注册服务；P1 再做 Inno Setup 安装包 | |

- 工程结论：install 命令和同源 Inno Setup 包；角色/安全配对/回滚/卸载保留数据。
- 实现/行为证据：NFR-07 与 docs/installer.md；真实服务尚未安装。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### TECH-15 构建

> | 构建 | Go 1.2x、`buf` 生成 protobuf 代码、pnpm 构建前端、`Makefile` 或 `mage` | |

- 工程结论：Go、固定 buf/protoc 插件和 pnpm，执行入口依项目 AGENTS 为 PowerShell scripts/build.ps1。
- 实现/行为证据：buf.gen.yaml、proto 兼容生成、本地 PS5.1 和 GitHub Actions；保留原项目结构，不为概念性 Makefile/mage 示例改名。
- 条件、反例与未覆盖范围：版本来自仓库锁定清单和实际构建，不声称是外部当前最新版；用户真实环境验收保留 Pending。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### PROTO-01 连接选择与全部连接细节

> ### 7.2 传输协议怎么选
>
> | 方案 | 优点 | 缺点 | 结论 |
> |---|---|---|---|
> | **gRPC 双向流 + mTLS** | 一条长连接里同时完成上报、心跳和配置下发；protobuf 体积小、有明确的数据结构，前后版本兼容；Go 官方支持 | 浏览器不能直接用，但浏览器本来就走监控器的 HTTP API | **采用** |
> | WebSocket + JSON | 简单，穿透代理好 | 要自己设计消息格式、版本兼容和流控 | 作为将来的后备通道 |
> | MQTT | 适合大量设备 | 需要额外部署消息服务器，对小白太重 | 不采用 |
> | HTTP 定时推送 | 最简单 | 无法实时下发配置和指令，延迟高 | 不采用 |
> | Prometheus 拉取 | 生态好 | 监控器要能主动连到每台采集器，跨 NAT 不行 | 只作为导出格式 |
> | OTLP（OpenTelemetry） | 行业标准 | 只能上报，不能下发配置 | 作为导出格式（P2） |
>
> 连接细节：
>
> - **方向**：采集器 → 监控器，默认端口 7443。采集器侧不开放任何端口。
> - **配对**：监控器生成一次性配对码（例如 `lgmon pair` 显示 `ABCD-1234`）。采集器带着配对码第一次连接，监控器签发它专属的客户端证书，采集器记下监控器证书的指纹。之后双方互相验证证书，任何一方被冒充都连不上。证书由监控器自建的私有 CA 签发，可以单独吊销某台采集器。
> - **跨网络**：推荐走 Tailscale，监控器地址填 Tailscale IP（你的机器是 `100.126.79.86`），不用做端口映射。不走 Tailscale 时，在局域网或公网也一样能用，因为加密和认证不依赖 Tailscale。
> - **可靠性**：每条数据带递增序号。采集器写本地缓存（SQLite WAL），收到确认后才删除；监控器按（采集器编号, 序号）去重。断线后指数退避重连，恢复后补发。
> - **心跳**：每 10 秒一次，超过 30 秒没有心跳就判定采集器失联并告警。
>

- 工程结论：采用原文选择的出站 7443 gRPC 双向流、mTLS、独立证书/吊销、序号 WAL 确认与 10s/30s 心跳。
- 实现/行为证据：T1～T7；internal/transport/pki.go 的 TLS1.3；TestDeliveryRetriesWithExponentialBackoffAndBoundedJitter、TestStreamWaitsForCommitAndUsesGzipBothWays、TestStreamResumesPersistedWatermarkAcceptsGapAndDirectlyAcksDuplicates；真实临时 mTLS/离线补传/撤销。
- 条件、反例与未覆盖范围：指数退避从 1s 到 60s 并有有界抖动。16 位指纹是 REVIEW R3-05 明确修订原先 8 位；先核对公开 CA 后才交配对码。不采用 MQTT/HTTP 定时推送作采集通道；Prometheus/OTLP 仅导出，WebSocket 是飞书回调 SDK 内部协议。Tailscale/真实 NAT 不在夹具内，本人待验。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### ORG-01 单仓职责与现有结构

> ### 7.3 代码组织（单仓库）
>
> ```
> lg-monitor/
>   cmd/lgmon/          入口：collector / monitor / cli / mcp 子命令
>   internal/collector/   各类采集与探测
>   internal/transport/   gRPC 客户端和服务端、配对、证书、离线缓存
>   internal/monitor/     存储、规则引擎、告警状态机、通知
>   internal/api/         HTTP API、SSE、MCP
>   internal/config/      配置文件、Schema、热加载
>   proto/                protobuf 定义
>   web/                  React 面板，构建产物嵌入 exe
>   schemas/              给 Agent 用的 JSON Schema
>   AGENTS.md
> ```
>

- 工程结论：既有单仓结构保留，职责已有 collector/transport/app/store/rules/notify/api/config 等包。
- 实现/行为证据：仓库目录、AGENTS 和 build；M0 以来按 PLAN 的标准包拆分。
- 条件、反例与未覆盖范围：PRD 图的概念性 internal/monitor 没有原样建立，职责拆在既有 app/store/rules/notify；用户明确禁止改变项目结构/命名，本轮不为对齐概念图重写目录。实际 workspace 是 D:\LFEN_project\dev_project\lgmon，后面的旧 PRD 本地路径不触发改名。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### MILESTONE-01 全部里程碑与真人完成标准

> ## 8. 里程碑
>
> | 阶段 | 内容 | 完成标准 |
> |---|---|---|
> | M0 原型 | 单机模式：采集进程和端口，SQLite 存储，一条“进程不在”规则，飞书开通向导，告警卡片 | 关掉 Nebula，15 秒内飞书收到告警，重新开启后收到恢复通知 |
> | M1 第一版 | 5.1–5.6 的全部 P0：双机 gRPC 加配对、离线补发、内置总览、声明式面板、CLI、API、MCP、Webhook | 两台电脑经 Tailscale 互联；拔网线 10 分钟后恢复，数据完整；Agent 只靠 `AGENTS.md` 能加一个监控目标、一条规则和一页面板 |
> | M2 | 全部 P1：HTML 面板、团队共享面板、事件日志、飞书双向、试运行、远程重启、导入服务开关配置、安装包 | |
> | M3 | P2：ETW 即时事件、OTLP 和 Prometheus 导出、一个采集器上报多个监控器 | |
>

- 工程结论：M0～M3 计划内工程实现与同范围夹具已经逐项交付；真实 M0/M1 完成标准仍 Pending。
- 实现/行为证据：docs/PLAN.md 每任务执行记录/提交/精确 CI、docs/acceptance-m0.md、acceptance-m1.md；本矩阵补齐所有编号与未编号原文。
- 条件、反例与未覆盖范围：不关闭 Nebula 或其他真实业务服务；用自建进程验证工程 15s，不据此签收真实飞书。真实双机 Tailscale、拔线 10min、未登录启动、真实 App/群和新 Agent 任务需本人执行。M2/M3 任务勾选和 CI 成功不是整体验收；人工 Pending 不阻止保存工程结果或后续独立工作。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### REL-01 服务开关关系

> ## 9. 和服务开关的关系
>
> 服务开关负责“开关和启动”，lg-monitor 负责“盯着和报警”，两者分开开发。M2 起 lg-monitor 读取服务开关的配置，自动生成监控目标；以后可以在告警里直接调用服务开关重启服务。
>

- 工程结论：保持两个产品独立；M2 导入注册配置以创建目标，受限重启由本产品独立授权操作实现。
- 实现/行为证据：C11、T6；固定 services.json 预览/指纹/版本和假执行器。
- 条件、反例与未覆盖范围：“以后可以直接调用服务开关”是未来设想，没有接入未定义的外部控制协议；不修改服务开关配置、不启停它的业务服务。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。

### DECISION-01 名称、范围、节奏与明确修订

> ## 10. 已定事项
>
> | 事项 | 决定 |
> |---|---|
> | 名称 | lg-monitor，程序和命令行叫 `lgmon` |
> | 仓库 | https://github.com/LFenX/lg-monitor ，本地在 `D:\LFen_project\dev_project\lg-monitor` |
> | 语言 | Go |
> | 平台 | 第一版只做 Windows x64；代码保持可移植，以后再出 Linux 版监控器 |
> | 节奏 | 先做 M0 单机原型，跑通“关掉进程后飞书收到告警”，再做 M1 双机 |
> | 飞书 | 用户在应用里填写自建应用的 App ID 和 App Secret，程序引导开通权限、把机器人拉进群并自动获取群 ID；告警默认发到一个专门的告警群；卡片有四套预设模板，可以在预设基础上自定义 |
> | 远程操作 | 不进第一版，放到 M2，并且要逐条授权 |
> | 事件日志规则（2026-11-08） | A1 原来把“事件日志匹配”列为 P0，和 C9 的 P1、M2 里程碑冲突。改为和 C9 一起放进 M2，设计见 PLAN 的“M2 之后”。C3 的目录匹配、C6 的关键字和证书到期仍是 P0，补成 PLAN 的 M1-20、M1-21 |
> | 团队共享面板（2026-11-08） | 放到 M2。团队建在负责人自己的监控器上；成员用邀请码加入，之后由设备记住身份，不用密码；成员实时读取负责人监控器上的数据，不复制；分享可以选卡片，也可以限定主机和监控目标。设计见 PLAN 的“M2 之后” |
>
> 执行计划见 [PLAN.md](PLAN.md)。

- 工程结论：名称/exe、Go/Windows x64、先 M0 后 M1、飞书自有应用/四模板、M2 远程逐条授权、事件日志/团队阶段均按当前 PLAN 执行。
- 实现/行为证据：AGENTS、PRD 第10节、PLAN 和 REVIEW；C3/C6 的 M1-20/M1-21、事件日志/团队 M2 证据及任务提交历史。
- 条件、反例与未覆盖范围：旧 PRD 本地路径 lg-monitor 与用户当前 workspace lgmon 不一致，按用户路径保留，不重命名。平台“以后”不等于本轮交付 Linux/macOS；真实服务/收件等仍 Pending；后续类似协议自行决定的授权不覆盖 Claude 明确审查的人工边界。
- 本人验收：Pending；证据不足时由执行者先补工程验证，真实操作由本人依原清单决定。


## 验证与尚未完成项

本次工程补齐与失败/重试、完整本地/原生/前端门禁以及精确提交 CI，均记录在 PLAN 的“M3-04”执行记录，避免在多处复制不同结论。PRD 原始 SHA-256 保持不变；引用需求原句不是修改 PRD 授权范围。

- 工程 Owner：Codex。提交前必须消除待核验占位并通过四项门禁、受影响原生和现有前端回归；最后以推送的精确 SHA 全部 CI/产物清单为交付证据。
- 人工 Owner：本人。完整步骤在 acceptance-m0.md、acceptance-m1.md，团队/卡片动作/服务操作/安装包各页保留对应操作。未发生的真实授权、服务安装、开机/重启、跨机/手机、飞书收件/点击、新 Agent 任务和 24h 自监控为 Pending。
- 明确原句差异：C2 的命令行不远传、C7 的受保护本机脚本、F4 的可选建群退路、A5 的无历史心跳拒绝回放、G5 的人工边界、固定 YAML 文件名及现有包结构均在对应条目写明，不以“全部已实现”掩盖。
- 对抗性审查：本人已经拒绝，本轮为 PLAN 要求的来源逐项核验，没有启动额外多轮对抗性审查，也不重复申请。
- user_decision：Pending。工程结果可保存/提交，整个持续目标只在真人原始验收证据齐备后完成；当前不得标 complete。
