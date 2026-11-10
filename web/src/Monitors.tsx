import { useEffect, useMemo, useRef, useState } from 'react'
import { AskAgent, Heartbeat, SectionTitle, ago, canConfigure, stateName } from './kit'
import { useApp } from './state'
import type { Monitor } from './types'
import { Empty, ErrorNotice, Icon, Modal } from './ui'

type Kind = 'port' | 'process' | 'service' | 'url'
export type MonitorPreset = { name?: string; port?: number; process?: string; service?: string; url?: string }
type Created = {
  monitor: Monitor
  created: boolean
  dashboardUrl?: string
  dashboard?: string
  monitorUrl?: string
  next: string[]
}

const kinds: { id: Kind; label: string; field: string; placeholder: string; help: string }[] = [
  {
    id: 'port',
    label: '端口',
    field: '端口号',
    placeholder: '例如 3000',
    help: '盯住占用这个端口的程序，每 10 秒连一次；连不上就告警。',
  },
  {
    id: 'url',
    label: '网址',
    field: '网址',
    placeholder: 'https://example.com/health',
    help: '定期访问这个地址；打不开、返回错误或 HTTPS 证书快到期都会告警。',
  },
  {
    id: 'service',
    label: 'Windows 服务',
    field: '服务名',
    placeholder: '例如 postgresql-x64-16',
    help: '填服务名（不是显示名）；服务停止就告警。不确定名字可以去“探索”里搜。',
  },
  {
    id: 'process',
    label: '进程',
    field: '进程名',
    placeholder: '例如 nginx.exe',
    help: '这个进程不在了就告警。',
  },
]

const kindLabels: Record<Monitor['kind'], string> = {
  port: '端口',
  url: '网址',
  service: 'Windows 服务',
  process: '进程',
  other: '自定义',
}
const kindIcons: Record<Monitor['kind'], 'plug' | 'globe' | 'layers' | 'cpu' | 'radar'> = {
  port: 'plug',
  url: 'globe',
  service: 'layers',
  process: 'cpu',
  other: 'radar',
}

export function monitorLink(monitor: Monitor) {
  return '#/monitors/' + encodeURIComponent(monitor.id)
}

function percentText(value: number | null) {
  if (value == null) return '—'
  return (value >= 99.995 ? '100' : value.toFixed(value >= 99 ? 2 : 1)) + '%'
}

function latencyText(value: number | null) {
  if (value == null) return '—'
  if (value < 1) return '<1 ms'
  return value >= 1000 ? (value / 1000).toFixed(2) + ' s' : Math.round(value) + ' ms'
}

/* Abnormal first, then unknown, then healthy; the same order everywhere a list of monitors appears. */
export function bySeverity(a: Monitor, b: Monitor) {
  const rank = (item: Monitor) =>
    item.state === 'down' || item.firing ? 0 : item.state === 'unknown' ? 1 : 2
  return rank(a) - rank(b) || a.name.localeCompare(b.name)
}

function reasonText(monitor: Monitor) {
  const host = monitor.hosts[0]
  if (monitor.firing) return monitor.firing + ' 条告警触发中'
  if (!host) return '等待第一轮采样'
  if (monitor.state === 'up') return host.reason || '运行正常'
  return host.reason || (monitor.state === 'down' ? '检测失败' : '还没有足够的数据')
}

export function MonitorCard({ monitor }: { monitor: Monitor }) {
  const { now } = useApp()
  const host = monitor.hosts[0]
  return (
    <a
      className="mon-card"
      data-state={monitor.state}
      data-firing={monitor.firing > 0 || undefined}
      href={monitorLink(monitor)}
    >
      <header>
        <span className="mon-kind" title={kindLabels[monitor.kind]}>
          <Icon name={kindIcons[monitor.kind]} size={15} />
        </span>
        <strong title={monitor.name}>{monitor.name}</strong>
        <span className="mon-pill">{stateName(monitor.state)}</span>
      </header>
      <p className="mon-target" title={monitor.watching.join(' · ')}>
        {monitor.watching.join(' · ')}
      </p>
      <Heartbeat beats={monitor.beats} />
      <dl className="mon-stats">
        <div>
          <dt>24 小时</dt>
          <dd>{percentText(monitor.uptime)}</dd>
        </div>
        <div>
          <dt>耗时</dt>
          <dd>{latencyText(monitor.latencyMs)}</dd>
        </div>
        <div>
          <dt>检查</dt>
          <dd>{host?.ts ? ago(host.ts, now()) : '—'}</dd>
        </div>
      </dl>
      <p className="mon-reason">{reasonText(monitor)}</p>
    </a>
  )
}

export function MonitorTable({
  monitors,
  remove,
}: {
  monitors: Monitor[]
  remove?: (monitor: Monitor) => void
}) {
  const { now } = useApp()
  return (
    <div className="mon-table">
      <table>
        <thead>
          <tr>
            <th>服务</th>
            <th>状态</th>
            <th className="hide-narrow">最近 24 小时</th>
            <th>可用率</th>
            <th className="hide-narrow">耗时</th>
            <th className="hide-narrow">最近检查</th>
            <th aria-label="操作" />
          </tr>
        </thead>
        <tbody>
          {monitors.map((item) => {
            const host = item.hosts[0]
            return (
              <tr key={item.id} data-state={item.state} data-firing={item.firing > 0 || undefined}>
                <td>
                  <a className="mon-name" href={monitorLink(item)}>
                    <span className="mon-kind">
                      <Icon name={kindIcons[item.kind]} size={14} />
                    </span>
                    <span>
                      <strong>{item.name}</strong>
                      <small>{item.watching.join(' · ')}</small>
                    </span>
                  </a>
                </td>
                <td>
                  <span className="mon-pill">{stateName(item.state)}</span>
                  <small className="mon-why">{reasonText(item)}</small>
                </td>
                <td className="hide-narrow">
                  <Heartbeat beats={item.beats} size="sm" />
                </td>
                <td className="num">{percentText(item.uptime)}</td>
                <td className="num hide-narrow">{latencyText(item.latencyMs)}</td>
                <td className="muted small hide-narrow">{host?.ts ? ago(host.ts, now()) : '—'}</td>
                <td>
                  <div className="row-actions">
                    {remove && (
                      <button
                        className="icon-button"
                        title="删除监控"
                        aria-label={'删除监控 ' + item.name}
                        onClick={() => remove(item)}
                      >
                        <Icon name="trash" size={16} />
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export function AddMonitorDialog({ close, preset }: { close: () => void; preset?: MonitorPreset }) {
  const { api, scope, refresh } = useApp()
  const initial: Kind = preset?.url
    ? 'url'
    : preset?.service
      ? 'service'
      : preset?.process
        ? 'process'
        : 'port'
  const [kind, setKind] = useState<Kind>(initial),
    [name, setName] = useState(preset?.name || ''),
    [value, setValue] = useState(
      String(preset?.url || preset?.service || preset?.process || preset?.port || ''),
    )
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [created, setCreated] = useState<Created>()
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  const spec = kinds.find((item) => item.id === kind)!
  const editable = canConfigure(scope)
  const trimmed = value.trim()
  const valid =
    !!name.trim() &&
    !!trimmed &&
    (kind !== 'port' || (/^\d{1,5}$/.test(trimmed) && Number(trimmed) >= 1 && Number(trimmed) <= 65535)) &&
    (kind !== 'url' || /^https?:\/\/\S+$/i.test(trimmed))
  const prompt =
    '用 lg-monitor 的 add_monitor 监控' +
    spec.label +
    ' ' +
    (trimmed || '<' + spec.field + '>') +
    '，名字叫“' +
    (name.trim() || '<名字>') +
    '”，然后把生成的面板地址给我。'
  if (created)
    return (
      <Modal title="监控已添加" close={close}>
        <p>
          <strong>{created.monitor.name}</strong> 已经开始监控
          {created.monitor.watching.length ? '（' + created.monitor.watching.join('、') + '）' : ''}
          。第一轮结果约十秒后出现。
        </p>
        <p className="muted small">已自动配好告警规则{created.dashboard ? '，并放进了分类面板' : ''}。</p>
        <div className="modal-actions">
          <button className="button" onClick={close}>
            完成
          </button>
          {created.dashboard && (
            <a className="button" href={'#/boards/' + encodeURIComponent(created.dashboard)} onClick={close}>
              打开面板
            </a>
          )}
          <a
            className="button primary"
            href={'#/monitors/' + encodeURIComponent(created.monitor.id)}
            onClick={close}
          >
            查看监控 <Icon name="arrow" size={16} />
          </a>
        </div>
      </Modal>
    )
  return (
    <Modal title="添加监控" close={close}>
      <form
        className="form-grid"
        onSubmit={(event) => {
          event.preventDefault()
          if (!valid || !editable) return
          const controller = new AbortController()
          pending.current?.abort()
          pending.current = controller
          setBusy(true)
          setError('')
          const body: Record<string, unknown> = { name: name.trim() }
          body[kind] = kind === 'port' ? Number(trimmed) : trimmed
          void api
            .request<Created>('/monitors', {
              method: 'POST',
              body: JSON.stringify(body),
              signal: controller.signal,
            })
            .then((result) => {
              if (controller.signal.aborted) return
              setCreated(result)
              refresh()
            })
            .catch((err: unknown) => {
              if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '添加监控失败')
            })
            .finally(() => {
              if (!controller.signal.aborted) setBusy(false)
            })
        }}
      >
        <div className="segmented" role="group" aria-label="监控类型">
          {kinds.map((item) => (
            <button
              type="button"
              key={item.id}
              aria-pressed={kind === item.id}
              onClick={() => {
                setKind(item.id)
                setValue('')
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
        <p className="muted small" style={{ margin: 0 }}>
          {spec.help}
        </p>
        <label>
          {spec.field}
          <input
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder={spec.placeholder}
            inputMode={kind === 'port' ? 'numeric' : undefined}
            spellCheck={false}
            autoFocus
            required
          />
        </label>
        <label>
          名字
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="例如 博客网站"
            maxLength={80}
            required
          />
        </label>
        <ErrorNotice message={error} />
        {editable ? (
          <div className="modal-actions">
            <button type="button" className="button" onClick={close}>
              取消
            </button>
            <button className="button primary" disabled={!valid || busy}>
              {busy ? '正在添加…' : '添加监控'}
            </button>
          </div>
        ) : (
          <AskAgent
            prompt={prompt}
            note="当前是只读访问，网页不能直接修改配置。把这句话交给已接入的 Agent，它会替你加上："
          />
        )}
      </form>
    </Modal>
  )
}

function storedView(): 'cards' | 'list' {
  try {
    return localStorage.getItem('lgmon-monitor-view') === 'list' ? 'list' : 'cards'
  } catch {
    return 'cards'
  }
}

export function Monitors() {
  const { api, monitors, scope, refresh, data } = useApp()
  const [query, setQuery] = useState(''),
    [filter, setFilter] = useState<'all' | 'down' | 'unknown' | 'up'>('all'),
    [kind, setKind] = useState<'' | Monitor['kind']>(''),
    [view, setView] = useState<'cards' | 'list'>(storedView),
    [adding, setAdding] = useState(false)
  const [removing, setRemoving] = useState<Monitor>(),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const counts = useMemo(
    () => ({
      all: monitors.length,
      down: monitors.filter((item) => item.state === 'down').length,
      unknown: monitors.filter((item) => item.state === 'unknown').length,
      up: monitors.filter((item) => item.state === 'up').length,
    }),
    [monitors],
  )
  const kinds = useMemo(() => [...new Set(monitors.map((item) => item.kind))], [monitors])
  const keyword = query.trim().toLowerCase()
  const shown = monitors
    .filter(
      (item) =>
        (filter === 'all' || item.state === filter) &&
        (!kind || item.kind === kind) &&
        (!keyword ||
          (item.name + ' ' + item.id + ' ' + item.watching.join(' ')).toLowerCase().includes(keyword)),
    )
    .sort(bySeverity)
  const pick = (value: 'cards' | 'list') => {
    setView(value)
    try {
      localStorage.setItem('lgmon-monitor-view', value)
    } catch {
      /* The view resets to cards next time. */
    }
  }
  if (!data) return null
  const uptimes = monitors.map((item) => item.uptime).filter((value): value is number => value != null)
  const average = uptimes.length ? uptimes.reduce((sum, value) => sum + value, 0) / uptimes.length : null
  return (
    <div className="fade-in">
      <div className="page-head">
        <div>
          <h1>服务</h1>
          <p className="muted">每个监控盯住一个端口、网址、Windows 服务或进程。异常的排在最前面。</p>
        </div>
        <div className="page-head-stats">
          <span>
            <b className="tone-up">{counts.up}</b>正常
          </span>
          <span>
            <b className={counts.down ? 'tone-down' : ''}>{counts.down}</b>异常
          </span>
          <span>
            <b className={counts.unknown ? 'tone-unknown' : ''}>{counts.unknown}</b>未知
          </span>
          <span>
            <b>{percentText(average)}</b>平均可用率
          </span>
        </div>
      </div>
      <div className="toolbar">
        <label className="search-field">
          <Icon name="search" size={16} />
          <span className="sr-only">搜索监控</span>
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索名字、端口、进程…"
          />
        </label>
        <div className="seg" role="group" aria-label="按状态筛选">
          {(
            [
              ['all', '全部'],
              ['down', '异常'],
              ['unknown', '未知'],
              ['up', '正常'],
            ] as const
          ).map(([id, label]) => (
            <button key={id} aria-pressed={filter === id} data-tone={id} onClick={() => setFilter(id)}>
              {label} <span className="n">{counts[id]}</span>
            </button>
          ))}
        </div>
        {kinds.length > 1 && (
          <label className="select-wrap">
            <span className="sr-only">类型</span>
            <select value={kind} onChange={(event) => setKind(event.target.value as typeof kind)}>
              <option value="">全部类型</option>
              {kinds.map((value) => (
                <option key={value} value={value}>
                  {kindLabels[value]}
                </option>
              ))}
            </select>
          </label>
        )}
        <div className="toolbar-end">
          <div className="seg icon-seg" role="group" aria-label="显示方式">
            <button
              aria-pressed={view === 'cards'}
              onClick={() => pick('cards')}
              title="卡片"
              aria-label="卡片视图"
            >
              <Icon name="grid" size={16} />
            </button>
            <button
              aria-pressed={view === 'list'}
              onClick={() => pick('list')}
              title="列表"
              aria-label="列表视图"
            >
              <Icon name="list" size={16} />
            </button>
          </div>
          <button className="button primary" onClick={() => setAdding(true)}>
            <Icon name="plus" size={16} />
            添加监控
          </button>
        </div>
      </div>
      <ErrorNotice message={error} />
      {shown.length ? (
        view === 'cards' ? (
          <div className="mon-grid">
            {shown.map((item) => (
              <MonitorCard key={item.id} monitor={item} />
            ))}
          </div>
        ) : (
          <MonitorTable monitors={shown} remove={canConfigure(scope) ? setRemoving : undefined} />
        )
      ) : monitors.length ? (
        <Empty title="没有符合条件的监控">换个关键词或筛选条件。</Empty>
      ) : (
        <>
          <Empty title="还没有监控">
            点“添加监控”，或者去 <a href="#/explore">探索</a> 看看这台电脑上在运行什么。
          </Empty>
          <SectionTitle title="也可以直接告诉 Agent" />
          <AskAgent prompt="调用 lg-monitor 的 start_here，看看这台电脑上有什么值得监控，把最重要的几个加上监控。" />
        </>
      )}
      {adding && <AddMonitorDialog close={() => setAdding(false)} />}
      {removing && (
        <Modal title="删除监控" close={() => setRemoving(undefined)}>
          <p>
            删除 <strong>{removing.name}</strong>
            ？它的告警规则会一起删除，历史数据保留到过期；分类面板不受影响。
          </p>
          <div className="modal-actions">
            <button className="button" onClick={() => setRemoving(undefined)}>
              取消
            </button>
            <button
              className="button danger"
              disabled={busy}
              onClick={() => {
                setBusy(true)
                setError('')
                void api
                  .request('/monitors/' + encodeURIComponent(removing.id), { method: 'DELETE' })
                  .then(() => refresh())
                  .catch((err: unknown) => setError(err instanceof Error ? err.message : '删除监控失败'))
                  .finally(() => {
                    setBusy(false)
                    setRemoving(undefined)
                  })
              }}
            >
              {busy ? '正在删除…' : '删除'}
            </button>
          </div>
        </Modal>
      )}
    </div>
  )
}
