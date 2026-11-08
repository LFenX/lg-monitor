import { useState } from 'react'
import { timestamp } from './model'
import { useApp, useResource } from './state'
import { Badge, Empty, ErrorNotice, Icon, Panel } from './ui'
import './diagnostic.css'

type Process = { pid: number; name: string; exe: string; cpuPercent: number; rss: number }
type Snapshot = {
  role: string
  ts: number
  state: string
  process: string
  port: string
  service: string
  processes: Process[]
  processesTruncated: boolean
}
type Coverage = {
  section: string
  rows: number
  limit: number
  truncated: boolean
  available: boolean
  rawCompleteFrom: number
  reason: string
}
type DiagnosticData = {
  schemaVersion: number
  generatedAt: number
  alert: {
    id: string
    ruleId: string
    collectorId: string
    targetId: string
    state: string
    severity: string
  }
  host: { name: string; lastSeen: number; clockOffsetMs: number } | null
  window: { anchor: number; from: number; to: number; source: string; pending: boolean }
  summary: string
  snapshots: Snapshot[]
  probes: { ts: number; probe: string; type: string; ok: boolean; latencyMs: number }[]
  metrics: { ts: number; cpuPercent: number; memoryPercent: number; uptimeSeconds: number }[]
  events: {
    ts: number
    channel: string
    provider: string
    eventId: number
    recordId: string
    subject: string
    summary: string
  }[]
  changes: { ts: number; scope: string; name: string; change: string }[]
  collectorEvents: { ts: number; kind: string; dropped: number; sourceTable: string }[]
  coverage: Coverage[]
  notes: string[]
  nextSteps: string[]
  agentPrompt: string
}
const sections: Record<string, string> = {
  targetSnapshots: '目标与进程',
  probeResults: '探测记录',
  systemMetrics: '系统指标',
  eventLogs: '事件日志尾部',
  startupChanges: '启动项变化',
  collectorEvents: '缺口与时钟',
}
export function diagnosticFilename(id: string) {
  return 'lgmon-diagnostic-' + (id.replace(/[^a-zA-Z0-9_-]/g, '_').slice(0, 80) || 'alert') + '.json'
}

export function Diagnostic({ id }: { id: string }) {
  const { api } = useApp()
  const [revision, setRevision] = useState(0)
  const [message, setMessage] = useState('')
  const resource = useResource(
    (signal) => api.request<DiagnosticData>('/alerts/' + encodeURIComponent(id) + '/diagnostics', { signal }),
    [api, id, revision],
    id,
  )
  const value = resource.value
  const supported = value?.schemaVersion === 1
  const text = supported ? value.agentPrompt + '\n\n' + JSON.stringify(value, null, 2) : ''
  const copy = async () => {
    try {
      if (!navigator.clipboard) throw new Error('clipboard unavailable')
      await navigator.clipboard.writeText(text)
      setMessage('已复制交接文本和 JSON；请自行核对后提供给 Agent。')
    } catch {
      setMessage('复制不可用，请展开交接文本后手动选择复制。')
    }
  }
  const download = () => {
    if (!supported) return
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(value, null, 2)], { type: 'application/json;charset=utf-8' }),
    )
    const link = document.createElement('a')
    link.href = url
    link.download = diagnosticFilename(value.alert.id)
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <div className="diagnostic-page">
      <div className="breadcrumbs">
        <a href="#/alerts">告警</a>
        <span>/</span>
        <span>诊断包</span>
      </div>
      <div className="page-heading">
        <div>
          <p className="eyebrow">DIAGNOSTIC</p>
          <h1>告警诊断包</h1>
          <p className="muted">基于已采集证据排查，生成和查看均为只读操作。</p>
        </div>
        <button
          className="button"
          disabled={resource.loading}
          onClick={() => {
            setMessage('')
            setRevision((n) => n + 1)
          }}
        >
          <Icon name="refresh" size={16} />
          刷新诊断
        </button>
      </div>
      <ErrorNotice
        message={resource.error || (value && !supported ? '诊断包版本不支持，请更新客户端。' : '')}
      />
      {!value && !resource.error && <Empty title="正在生成诊断包" />}
      {supported && (
        <>
          <Panel title="现象与范围" className="diagnostic-panel">
            <div className="diagnostic-content">
              <p>
                <Badge state={value.alert.state} /> {value.summary}
              </p>
              <dl className="diagnostic-facts">
                <dt>告警</dt>
                <dd className="mono">{value.alert.id}</dd>
                <dt>主机 / 目标</dt>
                <dd>
                  {value.host?.name || value.alert.collectorId || '未绑定单一主机'} /{' '}
                  {value.alert.targetId || '未指定'}
                </dd>
                <dt>生成时间</dt>
                <dd>{timestamp(value.generatedAt)}</dd>
                <dt>最近心跳</dt>
                <dd>{value.host ? timestamp(value.host.lastSeen) : '没有已保留的主机信息'}</dd>
                <dt>故障窗口</dt>
                <dd>
                  {timestamp(value.window.from)} ～ {timestamp(value.window.to)}
                </dd>
                <dt>时钟偏移</dt>
                <dd>
                  {value.host ? value.host.clockOffsetMs + ' ms（监控器减采集器）' : '尚无主机时钟信息'}
                </dd>
              </dl>
              <div className="row-actions">
                <button className="button primary" onClick={() => void copy()}>
                  复制给 Agent
                </button>
                <button className="button" onClick={download}>
                  下载 JSON
                </button>
              </div>
              {message && <p role="status">{message}</p>}
              <details>
                <summary>交接文本与完整 JSON</summary>
                <textarea aria-label="诊断交接文本" readOnly value={text} rows={12} />
              </details>
            </div>
          </Panel>
          <Panel title="数据完整性" className="diagnostic-panel">
            <div className="diagnostic-content">
              <ul>
                {value.notes.map((note, i) => (
                  <li key={i}>{note}</li>
                ))}
              </ul>
              <div className="diagnostic-coverage">
                {value.coverage.map((entry) => (
                  <article key={entry.section}>
                    <strong>{sections[entry.section] || entry.section}</strong>
                    <span>
                      {entry.available ? '已有 ' + entry.rows + ' 条' : '暂无可用观测'}
                      {' · 上限 ' + entry.limit + ' 条'}
                      {entry.truncated ? ' · 已截断' : ''}
                    </span>
                    {entry.rawCompleteFrom > 0 && (
                      <small>完整保留起点 {timestamp(entry.rawCompleteFrom)}</small>
                    )}
                    {entry.reason && <p>{entry.reason}</p>}
                  </article>
                ))}
              </div>
            </div>
          </Panel>
          <Panel title="相关进程与目标状态" className="diagnostic-panel">
            <div className="diagnostic-content">
              {value.snapshots.length ? (
                value.snapshots.map((snapshot) => (
                  <article className="diagnostic-snapshot" key={snapshot.role}>
                    <h3>
                      {{
                        incident: '故障点前最近观测',
                        previousMatch: '故障前最近匹配进程',
                        latest: '最近一次已存观测',
                      }[snapshot.role] || snapshot.role}{' '}
                      · {timestamp(snapshot.ts)}
                    </h3>
                    <p>
                      <Badge state={snapshot.state} /> 进程{' '}
                      {{ found: '已匹配', missing: '未匹配', unknown: '未知' }[snapshot.process] || '未记录'}
                      {snapshot.port &&
                        ' · 端口 ' +
                          ({ listening: '监听中', closed: '未监听', unknown: '未知' }[snapshot.port] ||
                            '未记录')}
                      {snapshot.service &&
                        ' · 服务 ' +
                          ({ running: '运行中', stopped: '已停止', unknown: '未知' }[snapshot.service] ||
                            '未记录')}
                    </p>
                    {snapshot.processesTruncated && (
                      <p className="notice notice-warning">进程列表已截断，仅保留前 50 个。</p>
                    )}
                    {snapshot.processes.length ? (
                      <div className="diagnostic-processes">
                        {snapshot.processes.map((process, i) => (
                          <article key={i}>
                            <strong>
                              {process.name} · PID {process.pid}
                            </strong>
                            <p className="mono small muted">{process.exe}</p>
                            <span>
                              CPU {process.cpuPercent.toFixed(1)}% · 内存 {(process.rss / 1048576).toFixed(1)}{' '}
                              MiB
                            </span>
                          </article>
                        ))}
                      </div>
                    ) : (
                      <p className="muted">此观测没有已保留的匹配进程。</p>
                    )}
                  </article>
                ))
              ) : (
                <p className="muted">没有可用的具体目标进程证据；请查看完整性说明。</p>
              )}
            </div>
          </Panel>
          <Panel title="事件日志尾部" className="diagnostic-panel">
            <div className="diagnostic-content">
              {value.events.length ? (
                <div className="diagnostic-events">
                  {value.events.map((event) => (
                    <article key={event.channel + event.recordId + event.ts}>
                      <time>{timestamp(event.ts)}</time>
                      <strong>{event.summary}</strong>
                      <span>
                        {event.provider} · 事件 {event.eventId} · {event.subject}
                      </span>
                      <small className="mono">记录 {event.recordId}</small>
                    </article>
                  ))}
                </div>
              ) : (
                <p className="muted">没有窗口内已保留、归属匹配的事件元数据；不代表没有事件发生。</p>
              )}
            </div>
          </Panel>
          <Panel title="最近变化与采集缺口" className="diagnostic-panel">
            <div className="diagnostic-content">
              {value.changes.map((change, i) => (
                <p key={'change' + i}>
                  {timestamp(change.ts)} ·{' '}
                  {{ added: '新增', changed: '修改', removed: '移除' }[change.change] || change.change} ·{' '}
                  {change.name} <span className="muted small">{change.scope}</span>
                </p>
              ))}
              {value.collectorEvents.map((event, i) => (
                <p key={'gap' + i}>
                  {timestamp(event.ts)} · {event.kind} · 丢失 {event.dropped} 条{' '}
                  <span className="muted small">{event.sourceTable}</span>
                </p>
              ))}
              {!value.changes.length && !value.collectorEvents.length && (
                <p className="muted">没有已保留的窗口内变化记录；请同时核对数据完整性。</p>
              )}
            </div>
          </Panel>
          <Panel title="探测与系统观测" className="diagnostic-panel">
            <div className="diagnostic-content">
              {value.probes.map((probe, i) => (
                <p key={'probe' + i}>
                  {timestamp(probe.ts)} · {probe.type || '未记录类型'} #{probe.probe} ·{' '}
                  {probe.ok ? '成功' : '失败'} · {probe.latencyMs} ms
                </p>
              ))}
              {value.metrics.map((metric, i) => (
                <p key={'metric' + i}>
                  {timestamp(metric.ts)} · CPU {metric.cpuPercent.toFixed(1)}% · 内存{' '}
                  {metric.memoryPercent.toFixed(1)}%
                </p>
              ))}
              {!value.probes.length && !value.metrics.length && (
                <p className="muted">暂无已保留的探测或系统指标。</p>
              )}
            </div>
          </Panel>
          <Panel title="下一步只读检查" className="diagnostic-panel">
            <div className="diagnostic-content">
              <ol>
                {value.nextSteps.map((step, i) => (
                  <li key={i}>{step}</li>
                ))}
              </ol>
            </div>
          </Panel>
        </>
      )}
    </div>
  )
}
