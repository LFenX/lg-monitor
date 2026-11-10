import { useState } from 'react'
import { SectionTitle, ago, boardIcon } from './kit'
import { bytes, clockWarning, hostLink } from './model'
import { AddMonitorDialog, MonitorCard, bySeverity } from './Monitors'
import { AlertRows } from './Overview'
import { ServiceSwitchImport } from './ServiceSwitchImport'
import { useApp } from './state'
import type { Collector, Summary } from './types'
import { Icon, Panel } from './ui'

const stepLinks: Record<string, string> = {
  agent: '#/connect',
  monitor: '#/explore',
  dashboard: '#/boards',
  notify: '#/notifications/setup',
}

function Ring({ value, label }: { value?: number; label: string }) {
  const known = typeof value === 'number' && Number.isFinite(value)
  const percent = known ? Math.max(0, Math.min(100, value)) : 0
  const color = percent >= 90 ? 'var(--red)' : percent >= 75 ? 'var(--amber)' : 'var(--primary)'
  const length = 2 * Math.PI * 26
  return (
    <div>
      <div
        className="ring"
        role="img"
        aria-label={label + (known ? ' ' + percent.toFixed(0) + '%' : ' 暂无数据')}
      >
        <svg viewBox="0 0 62 62" width="62" height="62">
          <circle cx="31" cy="31" r="26" fill="none" stroke="var(--track)" strokeWidth="6" />
          <circle
            cx="31"
            cy="31"
            r="26"
            fill="none"
            stroke={color}
            strokeWidth="6"
            strokeLinecap="round"
            strokeDasharray={length}
            strokeDashoffset={length * (1 - percent / 100)}
            style={{ transition: 'stroke-dashoffset .6s' }}
          />
        </svg>
        <b>{known ? percent.toFixed(1) + '%' : '—'}</b>
      </div>
      <small
        className="muted"
        style={{ display: 'block', marginTop: 4, fontSize: 11.5, textAlign: 'center' }}
      >
        {label}
      </small>
    </div>
  )
}

const configLabels = {
  applied: '已应用',
  pending: '等待确认',
  rejected: '被采集器拒收',
  outdated: '采集器需要升级',
}

function Vital({ host, summary }: { host: Collector; summary?: Summary }) {
  const warning = clockWarning(host.clockOffsetMs)
  const cache = host.cache
  const short = cache?.projectedHours != null && cache.projectedHours < 24
  return (
    <article className="vital-card host-card">
      <div>
        <h3>
          <i
            className={'status-dot ' + (host.online ? 'on' : 'bad')}
            style={{ display: 'inline-block', marginRight: 9 }}
          />
          <a href={hostLink(host.id)}>{host.hostname || host.id}</a>
        </h3>
        <p className="mono small">{host.id}</p>
        <p>
          {host.online ? '在线' : '离线'}
          {summary
            ? ' · 内存 ' + bytes(summary.memory_used) + ' / ' + bytes(summary.memory_total)
            : ' · 等待系统指标'}
        </p>
        {host.config && (
          <p style={host.config.state === 'applied' ? undefined : { color: 'var(--amber)' }}>
            <span>配置：{configLabels[host.config.state]}</span>
            {host.config.state !== 'applied' && host.config.message && <span>；{host.config.message}</span>}
          </p>
        )}
        {cache && (
          <p className="vital-cache">
            <span>
              缓存用量：{(cache.bytes / 1000000).toFixed(1)} / {(cache.maxBytes / 1000000).toFixed(1)} MB
            </span>
            <span>
              写入速度：
              {cache.bytesPerHour === null
                ? '估算中'
                : (cache.bytesPerHour / 1000000).toFixed(1) + ' MB/小时'}
            </span>
            <span>
              预计可缓存：
              {cache.projectedHours === null
                ? '估算中（需至少 5 分钟采样）'
                : cache.projectedHours.toFixed(1) + ' 小时'}
            </span>
          </p>
        )}
        {short && (
          <p style={{ color: 'var(--amber)' }} role="note">
            按当前数据量，断网时只能缓存约 {cache!.projectedHours!.toFixed(1)} 小时。可以减少目标，或者调大
            cache.maxSize。
          </p>
        )}
        {warning && (
          <p style={{ color: 'var(--amber)' }} role="note">
            {warning}
          </p>
        )}
      </div>
      <Ring value={summary?.cpu} label="CPU" />
      <Ring value={summary?.memory_percent} label="内存" />
      <footer className="vital-foot">
        <ServiceSwitchImport host={host} />
        <a className="text-link" href={hostLink(host.id)}>
          服务、启动项与历史 <Icon name="arrow" size={15} />
        </a>
      </footer>
    </article>
  )
}

export function Home() {
  const { data, guide, monitors, boards, now } = useApp()
  const [adding, setAdding] = useState(false)
  const [hidden, setHidden] = useState(() => {
    try {
      return localStorage.getItem('lgmon-journey') === 'hidden'
    } catch {
      return false
    }
  })
  if (!data) return null
  const down = monitors.filter((item) => item.state === 'down'),
    up = monitors.filter((item) => item.state === 'up'),
    unknown = monitors.filter((item) => item.state === 'unknown')
  const offline = data.collectors.filter((host) => !host.online)
  const firing = data.alerts.length
  const tone =
    firing || down.length || offline.length
      ? 'bad'
      : unknown.length
        ? 'warn'
        : monitors.length
          ? 'ok'
          : 'idle'
  const headline =
    tone === 'bad'
      ? [
          down.length && down.length + ' 个服务异常',
          firing && firing + ' 条告警触发中',
          offline.length && offline.length + ' 台电脑离线',
        ]
          .filter(Boolean)
          .join(' · ')
      : tone === 'warn'
        ? unknown.length + ' 个服务状态未知'
        : tone === 'ok'
          ? '一切正常'
          : '准备就绪，还没有监控'
  const detail =
    tone === 'bad'
      ? '下面标红的是需要处理的，点开能看到原因和前后发生了什么。'
      : tone === 'warn'
        ? '可能刚添加、采集器离线或权限不足，稍后会自动更新。'
        : tone === 'ok'
          ? monitors.length + ' 个服务都在正常运行，有变化会第一时间出现在这里。'
          : '让 Agent 看看这台电脑上在跑什么，挑几个重要的加上监控。'
  const steps = guide?.steps || []
  const finished = steps.filter((step) => step.done).length
  const next = steps.find((step) => !step.done)
  const showJourney = !!steps.length && finished < steps.length && !hidden
  const alerts = [...data.alerts].sort(
    (a, b) =>
      (({ critical: 0, warning: 1, info: 2 })[a.severity as 'critical'] ?? 3) -
        ({ critical: 0, warning: 1, info: 2 }[b.severity as 'critical'] ?? 3) || b.startedAt - a.startedAt,
  )
  const ordered = [...monitors].sort(bySeverity)
  const uptimes = monitors.map((item) => item.uptime).filter((value): value is number => value != null)
  const average = uptimes.length ? uptimes.reduce((sum, value) => sum + value, 0) / uptimes.length : null
  const channels = data.notifiers.notifiers || []
  return (
    <div className="fade-in overview">
      <section className="ov-head" data-tone={tone} aria-label="当前状态">
        <div className="ov-statement">
          <h1 className="sr-only">运行概览</h1>
          <p className="eyebrow">
            <i className="ov-dot" />
            {new Date(now()).toLocaleString('zh-CN', {
              month: 'long',
              day: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
              hour12: false,
            })}
          </p>
          <h2 className="hero-headline">{headline}</h2>
          <p>{detail}</p>
        </div>
        <dl className="ov-kpis">
          <div>
            <dt>服务正常</dt>
            <dd>
              {up.length}
              <small> / {monitors.length}</small>
            </dd>
          </div>
          <div data-alert={down.length > 0 || undefined}>
            <dt>异常</dt>
            <dd>{down.length}</dd>
          </div>
          <div data-alert={firing > 0 || undefined}>
            <dt>触发中告警</dt>
            <dd>{firing}</dd>
          </div>
          <div>
            <dt>24 小时可用率</dt>
            <dd>{average == null ? '—' : average.toFixed(average >= 99 ? 2 : 1) + '%'}</dd>
          </div>
          <div data-alert={offline.length > 0 || undefined}>
            <dt>电脑在线</dt>
            <dd>
              {data.collectors.length - offline.length}
              <small> / {data.collectors.length}</small>
            </dd>
          </div>
        </dl>
      </section>

      {!channels.length && (
        <a className="notice setup-banner" href="#/notifications/setup">
          <Icon name="bell" />
          <span>
            <strong>还没有设置告警通知</strong>
            <span>添加监控时会自动启用本机 Windows 通知；接入飞书后，故障和恢复会发到群里。</span>
          </span>
          <span className="setup-action">
            接入飞书 <Icon name="arrow" size={16} />
          </span>
        </a>
      )}

      {showJourney && (
        <section className="journey" aria-label="开始使用">
          <div className="journey-head">
            <h2>开始使用</h2>
            <span className="muted small">
              {finished} / {steps.length}
            </span>
            <div className="progress" aria-hidden="true">
              <i style={{ width: (finished / steps.length) * 100 + '%' }} />
            </div>
            <button
              className="button ghost small-button"
              onClick={() => {
                setHidden(true)
                try {
                  localStorage.setItem('lgmon-journey', 'hidden')
                } catch {
                  /* The banner simply returns on the next visit. */
                }
              }}
            >
              不再显示
            </button>
          </div>
          <ol className="journey-steps">
            {steps.map((step, index) => {
              const state = step.done ? ' done' : step === next ? ' next' : ''
              const body = (
                <>
                  <span className="num">{step.done ? <Icon name="check" size={13} /> : index + 1}</span>
                  <span>
                    <strong>{step.title}</strong>
                    <span className="how">
                      {step.done
                        ? '已完成'
                        : step.id === 'agent'
                          ? '一条命令接进你常用的 AI'
                          : step.id === 'monitor'
                            ? '在“探索”里点一下就加上'
                            : step.id === 'dashboard'
                              ? '按类别一键生成'
                              : step.id === 'notify'
                                ? '故障和恢复发到群里'
                                : step.how}
                    </span>
                  </span>
                </>
              )
              return (
                <li key={step.id}>
                  {stepLinks[step.id] && !step.done ? (
                    <a className={'journey-step' + state} href={stepLinks[step.id]}>
                      {body}
                    </a>
                  ) : (
                    <div className={'journey-step' + state}>{body}</div>
                  )}
                </li>
              )
            })}
          </ol>
        </section>
      )}

      {!!alerts.length && (
        <Panel
          title="正在触发的告警"
          className="active-alerts"
          extra={
            <a className="text-link" href="#/alerts">
              查看全部 <Icon name="arrow" size={16} />
            </a>
          }
        >
          <AlertRows alerts={alerts} limit={4} />
        </Panel>
      )}

      <SectionTitle title="服务" count={monitors.length ? monitors.length + ' 个' : undefined}>
        {!!monitors.length && <a href="#/monitors">全部服务 · 列表视图</a>}
      </SectionTitle>
      <div className="mon-grid">
        {ordered.slice(0, 11).map((item) => (
          <MonitorCard key={item.id} monitor={item} />
        ))}
        <button className="mon-card add" onClick={() => setAdding(true)}>
          <Icon name="plus" size={20} />
          <strong>添加监控</strong>
          <span>端口 · 网址 · Windows 服务 · 进程</span>
        </button>
      </div>

      <div className="ov-columns">
        <section>
          <SectionTitle
            title="电脑"
            count={data.collectors.length ? data.collectors.length + ' 台' : undefined}
          >
            <a href="#/explore">看看上面在运行什么</a>
          </SectionTitle>
          {data.collectors.length ? (
            <div className="vitals">
              {data.collectors.map((host) => (
                <Vital
                  key={host.id}
                  host={host}
                  summary={data.summaries.find((row) => row.collector_id === host.id)}
                />
              ))}
            </div>
          ) : (
            <p className="muted">还没有电脑连接。监控器以 both 角色运行时会自带本机采集器。</p>
          )}
        </section>
        <section>
          <SectionTitle title="面板" count={boards.length ? boards.length + ' 页' : undefined}>
            <a href="#/boards">面板库</a>
          </SectionTitle>
          {boards.length ? (
            <div className="board-list">
              {boards.slice(0, 6).map((page) => (
                <a className="board-row" href={'#/boards/' + encodeURIComponent(page.id)} key={page.id}>
                  <span className="board-icon">
                    <Icon name={boardIcon(page.icon)} size={17} />
                  </span>
                  <span>
                    <strong>{page.title}</strong>
                    <small>{page.description || page.panelCount + ' 张卡片'}</small>
                  </span>
                  <Icon name="arrow" size={15} />
                </a>
              ))}
            </div>
          ) : (
            <a className="mon-card add" href="#/boards" style={{ minHeight: 96 }}>
              <Icon name="board" size={20} />
              <strong>还没有面板</strong>
              <span>按类别一键生成：本机、服务、网站、告警、Windows 系统</span>
            </a>
          )}
        </section>
      </div>
      <p className="ov-foot">
        lg-monitor {guide?.version || ''} · 数据只留在这台电脑上
        {guide?.state.agentSeenAt ? ' · Agent 最近调用 ' + ago(guide.state.agentSeenAt, now()) : ''}
      </p>
      {adding && <AddMonitorDialog close={() => setAdding(false)} />}
    </div>
  )
}
