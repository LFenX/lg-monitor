import { useState } from 'react'
import { BoardArt, SectionTitle, ago, panelTypeNames } from './kit'
import { bytes, clockWarning, hostLink } from './model'
import { AddMonitorDialog, MonitorCard } from './Monitors'
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
    up = monitors.filter((item) => item.state === 'up')
  const offline = data.collectors.filter((host) => !host.online)
  const firing = data.alerts.length
  const tone = firing || down.length || offline.length ? 'bad' : monitors.length ? 'ok' : 'idle'
  const headline =
    tone === 'bad'
      ? [
          down.length && down.length + ' 个监控异常',
          firing && firing + ' 条告警触发中',
          offline.length && offline.length + ' 台电脑离线',
        ]
          .filter(Boolean)
          .join('，')
      : tone === 'ok'
        ? '一切正常'
        : '准备就绪，还没有监控'
  const detail =
    tone === 'bad'
      ? '先看下面标红的项目；点开可以看到原因和历史。'
      : tone === 'ok'
        ? monitors.length + ' 个监控都在正常运行，有变化会第一时间出现在这里。'
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
  const ordered = [...down, ...monitors.filter((item) => item.state === 'unknown'), ...up]
  const channels = data.notifiers.notifiers || []
  return (
    <div className="fade-in">
      {!channels.length && (
        <a className="notice setup-banner" href="#/notifications/setup" style={{ marginBottom: 16 }}>
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
      <section className="hero" data-tone={tone} aria-label="当前状态">
        <span className="hero-orb">
          <Icon name={tone === 'bad' ? 'alert' : tone === 'ok' ? 'check' : 'pulse'} size={30} />
        </span>
        <div>
          <h1 className="sr-only">运行概览</h1>
          <h2 className="hero-headline">{headline}</h2>
          <p>{detail}</p>
        </div>
        <div className="hero-stats">
          <div className="hero-stat">
            <strong>
              {up.length}
              <small> / {monitors.length}</small>
            </strong>
            <span>监控正常</span>
          </div>
          <div className="hero-stat">
            <strong style={firing ? { color: 'var(--red)' } : undefined}>{firing}</strong>
            <span>触发中告警</span>
          </div>
          <div className="hero-stat">
            <strong>
              {data.collectors.length - offline.length}
              <small> / {data.collectors.length}</small>
            </strong>
            <span>电脑在线</span>
          </div>
        </div>
      </section>

      {showJourney && (
        <section className="journey" aria-label="开始使用">
          <div className="journey-head">
            <h2>开始使用</h2>
            <div className="progress" aria-hidden="true">
              <i style={{ width: (finished / steps.length) * 100 + '%' }} />
            </div>
            <span className="muted small">
              {finished} / {steps.length}
            </span>
            <button
              className="button ghost small-button"
              style={{ marginLeft: 'auto' }}
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
          <div className="journey-steps">
            {steps.map((step, index) => {
              const state = step.done ? ' done' : step === next ? ' next' : ''
              const body = (
                <>
                  <span className="num">{step.done ? <Icon name="check" size={14} /> : index + 1}</span>
                  <strong>{step.title}</strong>
                  <span className="how">
                    {step.done
                      ? '已完成'
                      : step.id === 'agent'
                        ? '运行一条命令，把 lg-monitor 接进你常用的 AI Agent。'
                        : step.id === 'monitor'
                          ? '对 Agent 说要监控什么，或在“探索”里点一下就加上。'
                          : step.id === 'dashboard'
                            ? '从模板库挑一页，或让 Agent 按你的想法做一页。'
                            : step.id === 'notify'
                              ? '接入飞书，故障和恢复都会发到群里。'
                              : step.how}
                  </span>
                </>
              )
              return stepLinks[step.id] && !step.done ? (
                <a className={'journey-step' + state} href={stepLinks[step.id]} key={step.id}>
                  {body}
                </a>
              ) : (
                <div className={'journey-step' + state} key={step.id}>
                  {body}
                </div>
              )
            })}
          </div>
        </section>
      )}

      {!!alerts.length && (
        <div style={{ marginTop: 18 }}>
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
        </div>
      )}

      <SectionTitle title="监控" count={monitors.length ? monitors.length + ' 个' : undefined}>
        {!!monitors.length && <a href="#/monitors">全部监控</a>}
      </SectionTitle>
      <div className="monitor-grid">
        {ordered.slice(0, 11).map((item) => (
          <MonitorCard key={item.id} monitor={item} />
        ))}
        <button className="monitor-card add" onClick={() => setAdding(true)}>
          <Icon name="plus" size={22} />
          <strong>添加监控</strong>
          <span className="small">端口、进程、Windows 服务或网址</span>
        </button>
      </div>

      <SectionTitle title="电脑" count={data.collectors.length ? data.collectors.length + ' 台' : undefined}>
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

      <SectionTitle title="面板" count={boards.length ? boards.length + ' 页' : undefined}>
        <a href="#/boards">面板库与模板</a>
      </SectionTitle>
      {boards.length ? (
        <div className="board-gallery">
          {boards.slice(0, 4).map((page) => (
            <a
              className="board-card"
              href={'#/boards/' + encodeURIComponent(page.id)}
              key={page.id}
              data-board-theme={page.theme || 'aurora'}
            >
              <BoardArt id={page.id} theme={page.theme} icon={page.icon} />
              <div className="board-body">
                <h3>{page.title}</h3>
                {page.description && <p>{page.description}</p>}
                <div className="meta">
                  {(page.types || []).slice(0, 4).map((type) => (
                    <span className="tag" key={type}>
                      {panelTypeNames[type] || type}
                    </span>
                  ))}
                </div>
              </div>
            </a>
          ))}
        </div>
      ) : (
        <a className="monitor-card add" href="#/boards" style={{ minHeight: 96 }}>
          <Icon name="board" size={22} />
          <strong>还没有面板</strong>
          <span className="small">去模板库挑一页，一键生成</span>
        </a>
      )}
      <p className="muted small" style={{ marginTop: 28, textAlign: 'center' }}>
        lg-monitor {guide?.version || ''} · 数据留在这台电脑上
        {guide?.state.agentSeenAt ? ' · Agent 最近调用 ' + ago(guide.state.agentSeenAt, now()) : ''}
      </p>
      {adding && <AddMonitorDialog close={() => setAdding(false)} />}
    </div>
  )
}
