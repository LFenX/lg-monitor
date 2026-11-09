import { useMemo, useState } from 'react'
import { quoteSQL } from './api'
import { Chart } from './Chart'
import { Inventory } from './Inventory'
import { ServiceOperations } from './ServiceOperations'
import {
  bytes,
  gapFor,
  hostLink,
  insertGaps,
  period,
  percent,
  queryPeriod,
  series,
  targetLink,
  timestamp,
  title,
} from './model'
import { AlertRows } from './Overview'
import { useApp, useResource } from './state'
import { Badge, Empty, ErrorNotice, Icon, Panel, Range } from './ui'
import type { Alert, Gap, History, Metric, Service } from './types'

export function HostDetail({ collector }: { collector: string }) {
  const { api, data, version, now } = useApp(),
    [hours, setHours] = useState(1),
    [metric, setMetric] = useState('system.cpu_percent')
  const host = data?.collectors.find((item) => item.id === collector),
    summary = data?.summaries.find((row) => row.collector_id === collector)
  const targets = data?.targets.filter((item) => item.collectorId === collector) || []
  const resource = useResource(
    async (signal) => {
      const range = period(hours, now())
      const [points, services, gaps] = await Promise.all([
        api.request<{ metrics: Metric[] }>(
          '/metrics?collector=' +
            encodeURIComponent(collector) +
            '&name=' +
            encodeURIComponent(metric) +
            '&' +
            queryPeriod(range),
          { signal },
        ),
        api.query<Service>(
          'SELECT s.name,s.ts,s.state,s.start_type,s.pid,s.detail FROM service_status s WHERE s.collector_id=' +
            quoteSQL(collector) +
            ' AND NOT EXISTS (SELECT 1 FROM service_status n WHERE n.collector_id=s.collector_id AND n.name=s.name AND n.ts>s.ts) ORDER BY s.name',
          signal,
        ),
        api.query<Gap>(
          'SELECT ts,kind,message,dropped FROM collector_events WHERE collector_id=' +
            quoteSQL(collector) +
            ' AND ts>=' +
            range.from +
            ' ORDER BY ts DESC LIMIT 100',
          signal,
        ),
      ])
      return { points: points.metrics, services, gaps, step: range.step }
    },
    [api, collector, hours, metric, version],
    collector + ':' + hours + ':' + metric,
  )
  const chart = useMemo(
    () => series(resource.value?.points || [], gapFor(resource.value?.step)),
    [resource.value?.points, resource.value?.step],
  )
  if (!host)
    return (
      <Empty title="主机不存在或已被移除">
        <a href="#/">返回总览</a>
      </Empty>
    )
  const metricLabels: Record<string, string> = {
    'system.cpu_percent': 'CPU 使用率',
    'system.memory_percent': '内存使用率',
    'disk.used_percent': '磁盘使用率',
    'network.send_bytes_per_sec': '网络发送速率',
    'network.receive_bytes_per_sec': '网络接收速率',
  }
  return (
    <>
      <div className="breadcrumbs">
        <a href="#/">总览</a>
        <span>/</span>
        <span>{host.hostname || collector}</span>
      </div>
      <div className="page-heading">
        <div>
          <h1>
            {host.hostname || collector}{' '}
            <Badge state={host.online ? 'online' : 'offline'}>{host.online ? '在线' : '离线'}</Badge>
          </h1>
          <p className="muted">
            {collector} · {host.version || '尚未上报版本'} · 最近连接 {timestamp(host.lastSeen)}
          </p>
        </div>
        <Range hours={hours} change={setHours} />
      </div>
      {!host.online && (
        <div className="notice notice-warning">
          <Icon name="alert" />
          <span>采集器已离线，以下为最近收到的数据。</span>
        </div>
      )}
      <div className="stat-grid compact-stats">
        <DetailStat label="CPU 使用率" value={percent(summary?.cpu)} />
        <DetailStat label="内存使用率" value={percent(summary?.memory_percent)} />
        <DetailStat label="内存使用量" value={bytes(summary?.memory_used)} />
        <DetailStat
          label="正常目标"
          value={targets.filter((item) => item.state === 'up').length + ' / ' + targets.length}
        />
      </div>
      <ErrorNotice message={resource.error} />
      <Panel
        title="系统指标"
        extra={
          <label>
            <span className="sr-only">系统指标</span>
            <select value={metric} onChange={(event) => setMetric(event.target.value)}>
              {Object.entries(metricLabels).map(([value, label]) => (
                <option value={value} key={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
        }
      >
        <Chart
          title={metricLabels[metric]}
          series={chart}
          unit={metric.startsWith('network.') ? 'bytes/s' : '%'}
        />
      </Panel>
      <Panel title="监控目标" extra={<span className="count">{targets.length}</span>}>
        {targets.length ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>目标</th>
                  <th>状态</th>
                  <th>说明</th>
                  <th>最近采样</th>
                </tr>
              </thead>
              <tbody>
                {targets.map((target) => (
                  <tr key={target.targetId}>
                    <td>
                      <a className="strong" href={targetLink(collector, target.targetId)}>
                        {title(data!.definitions, target.targetId)}
                      </a>
                      <p className="muted small mono">{target.targetId}</p>
                    </td>
                    <td>
                      <Badge state={target.state} />
                    </td>
                    <td>{target.detail?.reason || '—'}</td>
                    <td className="muted small">{timestamp(target.ts)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty title="这台主机还没有监控目标" />
        )}
      </Panel>
      <Panel title="Windows 服务" extra={<span className="muted small">最近观察状态</span>}>
        {resource.value?.services.length ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>服务</th>
                  <th>状态</th>
                  <th>启动类型</th>
                  <th>PID</th>
                </tr>
              </thead>
              <tbody>
                {resource.value.services.map((service) => (
                  <tr key={service.name}>
                    <td>
                      <strong>{serviceName(service)}</strong>
                      <p className="muted small mono">{service.name}</p>
                    </td>
                    <td>
                      <Badge
                        state={
                          service.state === 'running'
                            ? 'up'
                            : service.state === 'stopped'
                              ? 'down'
                              : 'unknown'
                        }
                      >
                        {(
                          {
                            running: '运行中',
                            stopped: '已停止',
                            start_pending: '正在启动',
                            stop_pending: '正在停止',
                            pause_pending: '正在暂停',
                            continue_pending: '正在继续',
                            missing: '不存在',
                            deleted: '已删除',
                            paused: '已暂停',
                          } as Record<string, string>
                        )[service.state] || '未知'}
                      </Badge>
                    </td>
                    <td>
                      {(
                        {
                          auto: '自动',
                          automatic: '自动',
                          automatic_delayed: '自动（延迟）',
                          manual: '手动',
                          disabled: '已禁用',
                          boot: '引导',
                          system: '系统',
                        } as Record<string, string>
                      )[service.start_type] || service.start_type}
                    </td>
                    <td className="mono">{service.pid || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty title={resource.loading ? '正在读取服务状态' : '尚未收到 Windows 服务数据'} />
        )}
      </Panel>
      <Inventory collector={collector} />
      <ServiceOperations collector={collector} hostname={host.hostname || host.id} />
      {!!resource.value?.gaps.length && (
        <Panel title="数据缺口">
          <div className="gap-list">
            {resource.value.gaps.map((gap, index) => (
              <article key={index}>
                <Badge state="warning">{gap.kind === 'late_dropped' ? '迟到采样丢弃' : '数据缺口'}</Badge>
                <span>{gap.message}</span>
                <time>{timestamp(gap.ts)}</time>
              </article>
            ))}
          </div>
        </Panel>
      )}
    </>
  )
}

function serviceName(service: Service) {
  try {
    const value = JSON.parse(service.detail)
    return typeof value.displayName === 'string' ? value.displayName : service.name
  } catch {
    return service.name
  }
}
function DetailStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat-card">
      <div>
        <p>{label}</p>
        <strong>{value}</strong>
      </div>
    </div>
  )
}

export function TargetDetail({ collector, target }: { collector: string; target: string }) {
  const { api, data, version, now } = useApp(),
    [hours, setHours] = useState(1)
  const resource = useResource(
    async (signal) => {
      const range = period(hours, now())
      const [history, alerts] = await Promise.all([
        api.request<History>(
          '/targets/' +
            encodeURIComponent(collector) +
            '/' +
            encodeURIComponent(target) +
            '/history?' +
            queryPeriod(range),
          { signal },
        ),
        api.request<Alert[]>('/alerts?state=all&since=' + range.from, { signal }),
      ])
      return {
        history,
        alerts: alerts.filter((item) => item.collectorId === collector && item.targetId === target),
      }
    },
    [api, collector, target, hours, version],
    collector + ':' + target + ':' + hours,
  )
  const graphs = useMemo(() => {
    const history = resource.value?.history
    const states: [number, number | null][] = (history?.states || []).map((item) => [
      item.ts,
      item.state === 'up' ? 2 : item.state === 'down' ? 0 : 1,
    ])
    const probes = new Map<number, { name: string; data: [number, number | null][] }>()
    for (const point of history?.probes || []) {
      if (!probes.has(point.probe)) probes.set(point.probe, { name: '探测 ' + (point.probe + 1), data: [] })
      probes.get(point.probe)!.data.push([point.ts, point.averageLatencyMs])
    }
    return {
      state: [{ name: '目标状态', data: insertGaps(states, 30000) }],
      probes: [...probes.values()].map((item) => ({
        ...item,
        data: insertGaps(item.data, gapFor(history?.step)),
      })),
      cpu: series(
        (history?.processes || []).filter((item) => item.name === 'process.cpu_percent'),
        gapFor(history?.step),
      ),
      memory: series(
        (history?.processes || []).filter((item) => item.name === 'process.rss_bytes'),
        gapFor(history?.step),
      ),
    }
  }, [resource.value?.history])
  const current = data?.targets.find((item) => item.collectorId === collector && item.targetId === target)
  return (
    <>
      <div className="breadcrumbs">
        <a href="#/">总览</a>
        <span>/</span>
        <a href={hostLink(collector)}>
          {data?.collectors.find((host) => host.id === collector)?.hostname || collector}
        </a>
        <span>/</span>
        <span>{title(data?.definitions || [], target)}</span>
      </div>
      <div className="page-heading">
        <div>
          <h1>
            {title(data?.definitions || [], target)} <Badge state={current?.state || 'unknown'} />
          </h1>
          <p className="muted">{current?.detail?.reason || '查看状态变化、探测与进程资源。'}</p>
        </div>
        <Range hours={hours} change={setHours} />
      </div>
      <ErrorNotice message={resource.error} />
      {resource.value?.history.notes.map((note) => (
        <div className="notice notice-warning" key={note}>
          <Icon name="clock" />
          <span>{note}</span>
        </div>
      ))}
      {!!resource.value?.history.certificates?.length && (
        <Panel title="HTTPS 证书">
          <div className="gap-list">
            {resource.value.history.certificates.map((certificate) => (
              <article key={certificate.probe}>
                <Badge state={certificate.tlsNotAfter <= now() ? 'down' : 'up'}>
                  探测 {certificate.probe + 1}
                </Badge>
                <span>
                  证书到期：{timestamp(certificate.tlsNotAfter)}（
                  {certificate.tlsNotAfter <= now()
                    ? '已过期'
                    : '还剩 ' + Math.ceil((certificate.tlsNotAfter - now()) / 86400000) + ' 天'}
                  ）{certificate.tlsSubject && <small className="muted"> · {certificate.tlsSubject}</small>}
                </span>
                <time>观测于 {timestamp(certificate.ts)}</time>
              </article>
            ))}
          </div>
        </Panel>
      )}
      <Panel title="状态时间线" extra={<span className="muted small">无采样区间不推断状态</span>}>
        <Chart title="目标状态时间线" series={graphs.state} state />
      </Panel>
      <div className="chart-grid">
        <Panel title="探测耗时">
          <Chart title="探测耗时" series={graphs.probes} unit="ms" />
        </Panel>
        <Panel title="进程 CPU">
          <Chart title="进程 CPU 使用率" series={graphs.cpu} unit="%" />
        </Panel>
      </div>
      <Panel title="进程内存">
        <Chart title="进程内存使用量" series={graphs.memory} unit="bytes" />
      </Panel>
      <Panel
        title="最近告警"
        extra={
          <a className="text-link" href="#/alerts">
            全部告警 <Icon name="arrow" size={16} />
          </a>
        }
      >
        {resource.value?.alerts.length ? (
          <AlertRows alerts={resource.value.alerts} />
        ) : (
          <Empty title={resource.loading ? '正在读取告警' : '这段时间没有相关告警'} />
        )}
      </Panel>
    </>
  )
}
