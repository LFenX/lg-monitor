# lg-monitor

Windows 服务监控：采集器盯住电脑上的进程、服务、端口和网站，监控器汇总多台电脑的数据、出面板、通过飞书发告警。所有能力都开放给编程 Agent。

状态：需求阶段，尚无代码。需求和技术栈见 [docs/PRD.md](docs/PRD.md)。

- 语言：Go
- 采集器到监控器：gRPC + 双向 TLS，采集器主动连出
- 存储：SQLite
- 面板：React + ECharts，嵌入在程序里
