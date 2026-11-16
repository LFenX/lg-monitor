import { useState } from 'react'
import type { CSSProperties } from 'react'
import { AddService } from './AddService'
import { Incidents } from './Incidents'
import { ServiceRow, monitorHref, parentOptions } from './Board'
import { Glyph, kindGlyph, kindName } from './glyphs'
import { APIError } from './api'
import { ago } from './kit'
import { useGate } from './access'
import { AgentCard } from './AgentCard'
import { Dialog, Menu, changePrompt, useAgentSetup } from './parts'
import { Beats, StateBadge, LatencyChart, dayText, healthText, latencyText, percentText } from './status'
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

/*
 * What a person can change by hand: the name and where it hangs. Changing what is checked or when it alerts
 * goes to the agent, which rebuilds the monitor's checks and rules together.
 */
function EditService({ detail, close }: { detail: MonitorDetail; close: () => void }) {
  const { api, refresh, monitors } = useApp()
  const setup = useAgentSetup()
  const monitor = detail.monitor
  const [name, setName] = useState(monitor.name),
    [parent, setParent] = useState(monitor.parent || ''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  // A service cannot hang under itself or anything below it.
  const below = new Set([monitor.id, ...detail.children.map((child) => child.id)])
  const options = monitors.filter((item) => !below.has(item.id))
  return (
    <Dialog
      title="编辑服务"
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            取消
          </button>
          <button className="lm-btn primary" form="lm-edit-service" disabled={busy}>
            {busy ? '正在保存…' : '保存'}
          </button>
        </>
      }
    >
      <form
        id="lm-edit-service"
        style={{ display: 'grid', gap: 14 }}
        onSubmit={(event) => {
          event.preventDefault()
          setBusy(true)
          setError('')
          api
            .request('/monitors/' + encodeURIComponent(monitor.id), {
              method: 'PUT',
              body: JSON.stringify({ name: name.trim(), parent }),
            })
            .then(() => {
              refresh()
              close()
            })
            .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
            .finally(() => setBusy(false))
        }}
      >
        <label className="lm-field">
          名字
          <input
            className="lm-input"
            required
            maxLength={80}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className="lm-field">
          挂在哪个服务下面
          <select className="lm-input" value={parent} onChange={(event) => setParent(event.target.value)}>
            <option value="">不挂，作为主服务</option>
            {options.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </select>
        </label>
      </form>
      <AgentCard
        art={false}
        status={false}
        title="要换检查的地址、端口或告警等待时间？"
        lead="交给 Agent：它会先告诉你这个服务现在怎么检查，再问你想怎么改。"
        text={changePrompt(monitor, setup)}
      />
    </Dialog>
  )
}

export function ServicePage({ id }: { id: string }) {
  const { api, pulse, now } = useApp()
  const resource = useResource(
    (signal) =>
      api.request<MonitorDetail>('/monitors/' + encodeURIComponent(id), { signal }).catch((err: unknown) => {
        // The API's 404 text is written for agents ("call list_monitors"); a person followed a stale link.
        if (err instanceof APIError && err.status === 404)
          throw new Error('没有找到这个服务，它可能已经被删除了。')
        throw err
      }),
    [pulse],
    id,
  )
  const [dialog, setDialog] = useState<'' | 'child' | 'edit' | 'remove'>('')
  const gate = useGate()
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
          <button className="lm-btn" onClick={() => setDialog('child')}>
            <Glyph name="plus" size={16} />
            添加子服务
          </button>
          <Menu>
            <button onClick={() => gate('config', '编辑服务', () => setDialog('edit'))}>
              <Glyph name="edit" size={16} />
              编辑
            </button>
            <button className="danger" onClick={() => gate('config', '删除服务', () => setDialog('remove'))}>
              <Glyph name="trash" size={16} />
              删除
            </button>
          </Menu>
        </div>
      </header>

      <section className="lm-section" aria-label="最近 24 小时">
        <header>
          <h2 className="lm-h2">最近 24 小时</h2>
          <small>每格 15 分钟，异常按时点标记</small>
        </header>
        <Beats
          slots={detail.slots.map((slot) => ({ ...slot, span: slotSpan }))}
          size="large"
          label="最近 24 小时状态"
          liveState={overall}
        />
        <div className="lm-axis">
          <span>24 小时前</span>
          <span>现在</span>
        </div>
        <p className="lm-muted lm-small">
          实时异常时最后一格全红；恢复后仅标记持续满 1 分钟的异常，悬停查看真实时间。
        </p>
        {detail.historyNotice && <p className="lm-muted lm-small">{detail.historyNotice}</p>}
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

      <dl className="lm-stats" style={{ marginTop: 16, '--tiles': tiles.length } as CSSProperties}>
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
          <small>每天一格，异常按时点标记</small>
        </header>
        <Beats
          slots={days.map((day) => ({ ...day, span: daySpan }))}
          size="day"
          label="最近 30 天状态"
          liveState={overall}
        />
        {days.some((day) => day.uptime != null && !day.segments?.length) && (
          <p className="lm-muted lm-small">旧日期仅有汇总时显示正常占比，精确异常时点未保留。</p>
        )}
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

      <Incidents detail={detail} />

      <section className="lm-section" aria-label="告警规则">
        <header>
          <h2 className="lm-h2">什么时候告警</h2>
          <small>异常持续满 1 分钟才通知，避免一闪而过的波动打扰你</small>
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
      {dialog === 'edit' && <EditService detail={detail} close={() => setDialog('')} />}
      {dialog === 'remove' && <RemoveService detail={detail} close={() => setDialog('')} />}
    </div>
  )
}
