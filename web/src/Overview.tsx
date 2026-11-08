import { useApp } from './state'
import { bytes, clockWarning, hostLink, percent, targetLink, timestamp, title } from './model'
import { Badge, Empty, Icon, Panel } from './ui'
import type { Alert, Collector } from './types'

export function AlertRows({ alerts, limit }: { alerts: Alert[]; limit?: number }) {
  const { data } = useApp()
  return (
    <div className="alert-rows">
      {alerts.slice(0, limit).map((alert) => (
        <article className="alert-row" key={alert.id}>
          <span className={'alert-symbol ' + alert.severity}>
            <Icon name="alert" />
          </span>
          <div className="alert-copy">
            <div className="inline">
              <Badge state={alert.severity} />
              <strong>{alert.summary || '监控条件触发'}</strong>
              {alert.acknowledgedAt ? <span className="muted small">已确认</span> : null}
            </div>
            <p>
              <a href={hostLink(alert.collectorId)}>
                {data?.collectors.find((host) => host.id === alert.collectorId)?.hostname ||
                  alert.collectorId}
              </a>
              <span className="separator">/</span>
              <a href={targetLink(alert.collectorId, alert.targetId)}>
                {title(data?.definitions || [], alert.targetId)}
              </a>
              <span className="separator">·</span>
              {timestamp(alert.startedAt)}
            </p>
          </div>
          <Badge state={alert.state} />
        </article>
      ))}
    </div>
  )
}

function Meter({ label, value }: { label: string; value?: number }) {
  return (
    <div className="meter">
      <div>
        <span>{label}</span>
        <strong>{percent(value)}</strong>
      </div>
      <div className="meter-track" aria-hidden="true">
        <span
          className={value != null && value >= 85 ? 'high' : ''}
          style={{ width: Math.max(0, Math.min(100, value || 0)) + '%' }}
        />
      </div>
    </div>
  )
}

export function HostCard({ host }: { host: Collector }) {
  const { data } = useApp(),
    summary = data?.summaries.find((row) => row.collector_id === host.id)
  const targets = data?.targets.filter((row) => row.collectorId === host.id) || [],
    healthy = targets.filter((row) => row.state === 'up').length
  const warning = clockWarning(host.clockOffsetMs)
  const configuration = host.config
  const configLabels = {
    applied: '已应用',
    pending: '等待确认',
    rejected: '被采集器拒收',
    outdated: '采集器需要升级',
  }
  return (
    <article className="host-card">
      <div className="host-heading">
        <span className="host-icon">
          <Icon name="server" size={23} />
        </span>
        <div>
          <h3>
            <a href={hostLink(host.id)}>{host.hostname || host.id}</a>
          </h3>
          <span className="muted small mono">{host.id}</span>
        </div>
        <Badge state={host.online ? 'online' : 'offline'}>{host.online ? '在线' : '离线'}</Badge>
      </div>
      {warning && (
        <div className="notice notice-warning" role="note">
          {warning}
        </div>
      )}
      {configuration && (
        <p className="muted small">
          配置：{configLabels[configuration.state]}
          {configuration.state !== 'applied' && <span>；{configuration.message}</span>}
        </p>
      )}
      <Meter label="CPU 使用率" value={summary?.cpu} />
      <Meter label="内存使用率" value={summary?.memory_percent} />
      <div className="host-facts">
        <span>
          内存{' '}
          <strong>
            {summary ? bytes(summary.memory_used) + ' / ' + bytes(summary.memory_total) : '暂无数据'}
          </strong>
        </span>
        <span>
          正常目标{' '}
          <strong>
            <span className={healthy === targets.length && targets.length ? 'positive' : ''}>{healthy}</span>{' '}
            / {targets.length}
          </strong>
        </span>
      </div>
      <div className="host-footer">
        <span className="muted small">
          {summary ? (host.online ? '采样于 ' : '上次采样 ') + timestamp(summary.ts) : '尚未收到系统指标'}
        </span>
        <a className="text-link" href={hostLink(host.id)}>
          查看主机 <Icon name="arrow" size={16} />
        </a>
      </div>
    </article>
  )
}

export function Overview() {
  const { data } = useApp()
  if (!data) return null
  const alerts = [...data.alerts].sort(
    (a, b) =>
      (({ critical: 0, warning: 1, info: 2 })[a.severity as 'critical'] ?? 3) -
        ({ critical: 0, warning: 1, info: 2 }[b.severity as 'critical'] ?? 3) || b.startedAt - a.startedAt,
  )
  const channels = data.notifiers.notifiers || [],
    online = data.collectors.filter((host) => host.online).length,
    healthy = data.targets.filter((target) => target.state === 'up').length
  return (
    <>
      {!channels.length && (
        <a className="notice setup-banner" href="#/notifications/setup">
          <Icon name="bell" />
          <span>
            <strong>还没有设置告警通知</strong>
            <span>接入飞书，及时收到故障与恢复消息。</span>
          </span>
          <span className="setup-action">
            接入飞书 <Icon name="arrow" size={16} />
          </span>
        </a>
      )}
      <div className="page-heading">
        <div>
          <p className="eyebrow">OVERVIEW</p>
          <h1>运行概览</h1>
          <p className="muted">主机、监控目标与告警，实时更新。</p>
        </div>
        <span className="soft-label">
          <Icon name="server" size={16} />
          {data.collectors.length} 台主机
        </span>
      </div>
      <Panel
        title="正在触发的告警"
        extra={
          <a className="text-link" href="#/alerts">
            查看全部 <Icon name="arrow" size={16} />
          </a>
        }
        className={alerts.length ? 'active-alerts' : ''}
      >
        {alerts.length ? (
          <AlertRows alerts={alerts} limit={4} />
        ) : (
          <div className="all-clear">
            <span className="success-icon">
              <Icon name="check" />
            </span>
            <div>
              <strong>当前没有触发中的告警</strong>
              <p className="muted small">
                {data.collectors.length ? '监控将继续检查已配置的规则。' : '连接采集器后开始监控。'}
              </p>
            </div>
          </div>
        )}
      </Panel>
      <div className="stat-grid">
        <Stat label="在线主机" value={online} total={data.collectors.length} icon="server" color="blue" />
        <Stat label="正常目标" value={healthy} total={data.targets.length} icon="check" color="green" />
        <Stat label="触发中告警" value={alerts.length} icon="alert" color={alerts.length ? 'red' : 'blue'} />
        <Stat label="通知渠道" value={channels.length} icon="bell" color="violet" />
      </div>
      <div className="section-heading">
        <h2>
          监控主机 <span className="count">{data.collectors.length}</span>
        </h2>
        <span className="muted small">
          {online} 台在线 · {data.collectors.length - online} 台离线
        </span>
      </div>
      {data.collectors.length ? (
        <div className="host-grid">
          {data.collectors.map((host) => (
            <HostCard key={host.id} host={host} />
          ))}
        </div>
      ) : (
        <Panel title="等待采集器连接">
          <Empty title="还没有连接的主机">
            在被监控电脑运行 lgmon pair join 完成配对，连接后主机会出现在这里。
          </Empty>
        </Panel>
      )}
    </>
  )
}

function Stat({
  label,
  value,
  total,
  icon,
  color,
}: {
  label: string
  value: number
  total?: number
  icon: 'server' | 'check' | 'alert' | 'bell'
  color: string
}) {
  return (
    <div className="stat-card">
      <div>
        <p>{label}</p>
        <strong>
          {value}
          {total != null && <span> / {total}</span>}
        </strong>
      </div>
      <span className={'stat-icon ' + color}>
        <Icon name={icon} size={23} />
      </span>
    </div>
  )
}
