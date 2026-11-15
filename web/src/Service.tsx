import { useState } from 'react'
import { AddService } from './AddService'
import { ServiceRow, monitorHref, parentOptions } from './Board'
import { Glyph, kindGlyph, kindName } from './glyphs'
import { ago, canConfigure } from './kit'
import { ConnectSteps, CopyButton, Dialog, Menu, PromptBox, changePrompt, childPrompt } from './parts'
import {
  Beats,
  Dot,
  StateBadge,
  LatencyChart,
  dayText,
  healthText,
  latencyText,
  moment,
  percentText,
} from './status'
import { useApp, useResource } from './state'
import type { MonitorDetail } from './types'

const slotSpan = 15 * 60 * 1000
const daySpan = 24 * 60 * 60 * 1000

function certificateDays(expires: number, now: number) {
  return Math.floor((expires - now) / daySpan)
}

function RemoveService({ detail, close }: { detail: MonitorDetail; close: () => void }) {
  const { api, refresh } = useApp()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const children = detail.children.length
  return (
    <Dialog
      title={'删除「' + detail.monitor.name + '」？'}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            取消
          </button>
          <button
            className="lm-btn danger solid"
            disabled={busy}
            onClick={() => {
              setBusy(true)
              const suffix = children ? '?children=true' : ''
              api
                .request('/monitors/' + encodeURIComponent(detail.monitor.id) + suffix, { method: 'DELETE' })
                .then(() => {
                  refresh()
                  const parent = detail.lineage[detail.lineage.length - 1]
                  window.location.hash = parent
                    ? monitorHref(parent.id)
                    : detail.boards[0]
                      ? '#/boards/' + encodeURIComponent(detail.boards[0].id)
                      : '#/'
                })
                .catch((err: unknown) => setError(err instanceof Error ? err.message : '删除失败'))
                .finally(() => setBusy(false))
            }}
          >
            删除
          </button>
        </>
      }
    >
      <p style={{ margin: 0 }}>
        会同时删除它的告警规则，不会动这台电脑上的程序本身。
        {children > 0 && <strong> 它下面的 {children} 个子服务也会一起删除。</strong>}
      </p>
    </Dialog>
  )
}

export function ServicePage({ id }: { id: string }) {
  const { api, pulse, scope, now } = useApp()
  const resource = useResource(
    (signal) => api.request<MonitorDetail>('/monitors/' + encodeURIComponent(id), { signal }),
    [pulse],
    id,
  )
  const [dialog, setDialog] = useState<'' | 'child' | 'change' | 'remove' | 'agent-child'>('')
  const detail = resource.value
  if (!detail)
    return (
      <div className="lm-page">
        {resource.error ? (
          <div className="lm-note" data-tone="down">
            {resource.error}
            <a className="lm-btn small" href="#/">
              回到面板
            </a>
          </div>
        ) : (
          <p className="lm-muted">正在读取服务…</p>
        )}
      </div>
    )
  const monitor = detail.monitor
  const overall = monitor.overall || monitor.state
  const writable = canConfigure(scope)
  const current = now()
  const hosts = monitor.hosts
  const reasons = hosts.filter((host) => host.reason)
  const probed = detail.latency.length > 0 || detail.latencyNow != null
  const lastCheck = Math.max(0, ...hosts.map((host) => host.ts))
  const tiles: { label: string; value: string; tone?: string }[] = []
  if (probed) {
    tiles.push({ label: '当前响应', value: latencyText(detail.latencyNow) })
    tiles.push({ label: '平均响应（24 小时）', value: latencyText(detail.latencyAvg) })
  }
  tiles.push({ label: '可用率（24 小时）', value: percentText(detail.uptime24h) })
  if (!detail.certExpiresAt) tiles.push({ label: '可用率（7 天）', value: percentText(detail.uptime7d) })
  tiles.push({ label: '可用率（30 天）', value: percentText(detail.uptime30d) })
  if (detail.certExpiresAt) {
    const days = certificateDays(detail.certExpiresAt, current)
    tiles.push({ label: '证书剩余', value: days + ' 天', tone: days < 14 ? 'down' : undefined })
  }
  if (!probed) tiles.push({ label: '最近检查', value: lastCheck ? ago(lastCheck, current) : '—' })
  const days = detail.days
  return (
    <div className="lm-page">
      <nav className="lm-crumbs" aria-label="位置">
        {detail.boards[0] ? (
          <a href={'#/boards/' + encodeURIComponent(detail.boards[0].id)}>{detail.boards[0].name}</a>
        ) : (
          <a href="#/">面板</a>
        )}
        {detail.lineage.map((parent) => (
          <span key={parent.id} style={{ display: 'contents' }}>
            <Glyph name="chevron" size={12} />
            <a href={monitorHref(parent.id)}>{parent.name}</a>
          </span>
        ))}
        <Glyph name="chevron" size={12} />
        <span>{monitor.name}</span>
      </nav>
      <header className="lm-svc-head">
        <div style={{ minWidth: 0 }}>
          <div className="lm-svc-title">
            <StateBadge state={overall} />
            <h1 className="lm-title">{monitor.name}</h1>
          </div>
          <p className="watch">
            <span>
              <Glyph name={kindGlyph[monitor.kind] || 'other'} size={15} />
              {monitor.watching.join(' · ') || kindName[monitor.kind]}
            </span>
            {hosts.length > 0 && (
              <span>
                <Glyph name="computer" size={15} />
                {hosts.map((host) => host.hostname || host.collectorId).join('、')}
              </span>
            )}
            {lastCheck > 0 && <span>{ago(lastCheck, current)}检查</span>}
          </p>
        </div>
        <div className="lm-actions">
          <button className="lm-btn" onClick={() => setDialog(writable ? 'child' : 'agent-child')}>
            <Glyph name="plus" size={16} />
            添加子服务
          </button>
          <Menu>
            <button onClick={() => setDialog('agent-child')}>
              <Glyph name="agent" size={16} />让 Agent 加子服务
            </button>
            <button onClick={() => setDialog('change')}>
              <Glyph name="edit" size={16} />让 Agent 修改这个监控
            </button>
            {writable && (
              <button className="danger" onClick={() => setDialog('remove')}>
                <Glyph name="trash" size={16} />
                删除
              </button>
            )}
          </Menu>
        </div>
      </header>

      <section className="lm-section" aria-label="最近 24 小时">
        <header>
          <h2 className="lm-h2">最近 24 小时</h2>
          <small>每格 15 分钟</small>
        </header>
        <Beats
          slots={detail.slots.map((slot) => ({ ...slot, span: slotSpan }))}
          size="large"
          label="最近 24 小时状态"
        />
        <div className="lm-axis">
          <span>24 小时前</span>
          <span>现在</span>
        </div>
        {monitor.issue && (
          <p className="lm-reason" data-state="down">
            {monitor.issue}
          </p>
        )}
        {reasons.map((host) => (
          <p className="lm-reason" data-state={host.state} key={host.collectorId}>
            {hosts.length > 1 && <strong>{host.hostname || host.collectorId}：</strong>}
            {healthText[host.state] || host.state} · {host.reason}
          </p>
        ))}
      </section>

      <dl
        className="lm-stats"
        style={{ marginTop: 16, gridTemplateColumns: 'repeat(' + tiles.length + ', minmax(0, 1fr))' }}
      >
        {tiles.map((tile) => (
          <div className="lm-stat" key={tile.label} data-tone={tile.tone}>
            <dt>{tile.label}</dt>
            <dd>{tile.value}</dd>
          </div>
        ))}
      </dl>

      <section className="lm-section" aria-label="最近 30 天">
        <header>
          <h2 className="lm-h2">最近 30 天</h2>
          <small>每天一格，悬停看当天可用率</small>
        </header>
        <Beats slots={days.map((day) => ({ ...day, span: daySpan }))} size="day" label="最近 30 天状态" />
        <div className="lm-axis">
          <span>{days[0] ? dayText(days[0].from) : ''}</span>
          <span>今天</span>
        </div>
      </section>

      {probed && (
        <section className="lm-section" aria-label="响应时间">
          <header>
            <h2 className="lm-h2">响应时间</h2>
            <small>最近 24 小时，成功探测的平均耗时</small>
          </header>
          <LatencyChart points={detail.latency} from={current - 24 * 3600 * 1000} to={current} />
        </section>
      )}

      {detail.children.length > 0 && (
        <section className="lm-section" aria-label="子服务" style={{ padding: 0, overflow: 'hidden' }}>
          <header style={{ padding: '16px 20px 0', marginBottom: 10 }}>
            <h2 className="lm-h2">子服务</h2>
            <small>任一子服务异常，「{monitor.name}」整体就算异常</small>
          </header>
          <div
            className="lm-list"
            role="list"
            style={{ border: 0, borderRadius: 0, borderTop: '1px solid var(--line)' }}
          >
            {detail.children.map((child) => (
              <ServiceRow key={child.id} item={child} depth={Math.max(0, (child.depth || 1) - 1)} />
            ))}
          </div>
        </section>
      )}

      <div className="lm-two">
        <section className="lm-section" aria-label="最近事件">
          <header>
            <h2 className="lm-h2">最近事件</h2>
            <small>状态变化和告警</small>
          </header>
          {detail.events.length ? (
            <ul className="lm-events">
              {detail.events.map((event, index) => (
                <li key={index}>
                  <Dot
                    state={event.kind === 'alert' ? 'down' : event.kind === 'resolved' ? 'up' : event.kind}
                  />
                  <span>
                    {event.text}
                    {event.host && hosts.length > 1 && <small> · {event.host}</small>}
                  </span>
                  <time dateTime={new Date(event.ts).toISOString()}>{moment(event.ts, current)}</time>
                </li>
              ))}
            </ul>
          ) : (
            <p className="lm-muted lm-small" style={{ margin: 0 }}>
              最近 24 小时状态没有变化。
            </p>
          )}
        </section>
        <section className="lm-section" aria-label="告警规则">
          <header>
            <h2 className="lm-h2">什么时候告警</h2>
          </header>
          {detail.rules.length ? (
            <ul className="lm-rules">
              {detail.rules.map((rule) => (
                <li key={rule.id}>
                  <span>{rule.text}</span>
                  <small>通知：{rule.notify.join('、') || '没有通知渠道'}</small>
                </li>
              ))}
            </ul>
          ) : (
            <p className="lm-muted lm-small" style={{ margin: 0 }}>
              没有告警规则，只记录状态。
            </p>
          )}
        </section>
      </div>

      {dialog === 'child' && (
        <AddService
          board={detail.boards[0]}
          parents={[
            { id: monitor.id, name: monitor.name, depth: detail.lineage.length },
            ...parentOptions(detail.children).map((item) => ({
              ...item,
              depth: item.depth + detail.lineage.length,
            })),
          ]}
          parent={monitor.id}
          close={() => setDialog('')}
        />
      )}
      {(dialog === 'agent-child' || dialog === 'change') && (
        <Dialog
          title={dialog === 'change' ? '让 Agent 修改这个监控' : '让 Agent 加子服务'}
          close={() => setDialog('')}
        >
          <PromptBox text={dialog === 'change' ? changePrompt(monitor) : childPrompt(monitor)} />
          <div className="lm-prompt-bar">
            <span className="lm-muted lm-small">把【】里换成你想要的。</span>
            <CopyButton
              text={dialog === 'change' ? changePrompt(monitor) : childPrompt(monitor)}
              primary
              label="复制这段话"
            />
          </div>
          <ConnectSteps compact />
        </Dialog>
      )}
      {dialog === 'remove' && <RemoveService detail={detail} close={() => setDialog('')} />}
    </div>
  )
}
