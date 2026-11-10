import { useEffect, useMemo, useRef, useState } from 'react'
import { AskAgent, SectionTitle, ago, canConfigure } from './kit'
import { useApp } from './state'
import type { Monitor } from './types'
import { Badge, Empty, ErrorNotice, Icon, Modal } from './ui'

type Kind = 'port' | 'process' | 'service' | 'url'
export type MonitorPreset = { name?: string; port?: number; process?: string; service?: string; url?: string }
type Created = {
  monitor: Monitor
  created: boolean
  dashboardUrl?: string
  dashboard?: string
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

export function MonitorCard({ monitor }: { monitor: Monitor }) {
  const { now } = useApp()
  const host = monitor.hosts[0]
  const body = (
    <>
      <header>
        <i
          className={
            'status-dot ' + (monitor.state === 'up' ? 'on' : monitor.state === 'down' ? 'bad' : 'warn')
          }
        />
        <strong title={monitor.name}>{monitor.name}</strong>
        <Badge state={monitor.state} />
      </header>
      <div className="watching">
        {monitor.watching.slice(0, 3).map((text) => (
          <span className="tag" key={text} title={text}>
            {text.length > 34 ? text.slice(0, 33) + '…' : text}
          </span>
        ))}
      </div>
      <footer>
        <span className="reason">
          {monitor.firing
            ? monitor.firing + ' 条告警触发中'
            : host?.reason || (host ? '运行正常' : '等待第一轮采样')}
        </span>
        <span>{host?.ts ? ago(host.ts, now()) : ''}</span>
      </footer>
    </>
  )
  const link = host
    ? '#/hosts/' + encodeURIComponent(host.collectorId) + '/targets/' + encodeURIComponent(monitor.id)
    : ''
  return link ? (
    <a className="monitor-card" data-state={monitor.state} href={link}>
      {body}
    </a>
  ) : (
    <div className="monitor-card" data-state={monitor.state}>
      {body}
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
        <p className="muted small">已自动配好告警规则{created.dashboard ? '和一页面板' : ''}。</p>
        <div className="modal-actions">
          {created.dashboard && (
            <a
              className="button primary"
              href={'#/boards/' + encodeURIComponent(created.dashboard)}
              onClick={close}
            >
              打开面板 <Icon name="arrow" size={16} />
            </a>
          )}
          <button className="button" onClick={close}>
            完成
          </button>
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

export function Monitors() {
  const { api, monitors, scope, refresh, data } = useApp()
  const [query, setQuery] = useState(''),
    [filter, setFilter] = useState<'all' | 'down' | 'unknown' | 'up'>('all'),
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
  const keyword = query.trim().toLowerCase()
  const shown = monitors.filter(
    (item) =>
      (filter === 'all' || item.state === filter) &&
      (!keyword ||
        (item.name + ' ' + item.id + ' ' + item.watching.join(' ')).toLowerCase().includes(keyword)),
  )
  if (!data) return null
  return (
    <div className="fade-in">
      <div className="page-heading">
        <div>
          <p className="eyebrow">MONITORS</p>
          <h1>监控</h1>
          <p className="muted">每个监控盯住一个端口、进程、服务或网址，出问题时告警。</p>
        </div>
        <button className="button primary" onClick={() => setAdding(true)}>
          <Icon name="plus" size={16} />
          添加监控
        </button>
      </div>
      <div className="explore-toolbar" style={{ marginTop: 0 }}>
        <label className="search-field">
          <Icon name="search" size={16} />
          <span className="sr-only">搜索监控</span>
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索名字、端口、进程…"
          />
        </label>
        <div className="chips">
          {(
            [
              ['all', '全部'],
              ['down', '异常'],
              ['unknown', '未知'],
              ['up', '正常'],
            ] as const
          ).map(([id, label]) => (
            <button className="chip" key={id} aria-pressed={filter === id} onClick={() => setFilter(id)}>
              {label} <span className="n">{counts[id]}</span>
            </button>
          ))}
        </div>
      </div>
      <ErrorNotice message={error} />
      {shown.length ? (
        <div className="explore-table">
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>监控</th>
                  <th>状态</th>
                  <th>监控对象</th>
                  <th>告警规则</th>
                  <th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {shown.map((item) => {
                  const host = item.hosts[0]
                  return (
                    <tr key={item.id}>
                      <td>
                        {host ? (
                          <a
                            className="strong"
                            href={
                              '#/hosts/' +
                              encodeURIComponent(host.collectorId) +
                              '/targets/' +
                              encodeURIComponent(item.id)
                            }
                          >
                            {item.name}
                          </a>
                        ) : (
                          <strong>{item.name}</strong>
                        )}
                        <p className="muted small mono">{item.id}</p>
                      </td>
                      <td>
                        <Badge state={item.state} />
                        <p className="muted small">
                          {item.firing ? item.firing + ' 条告警' : host?.reason || ''}
                        </p>
                      </td>
                      <td>
                        <div className="chips">
                          {item.watching.map((text) => (
                            <span className="tag" key={text}>
                              {text}
                            </span>
                          ))}
                        </div>
                      </td>
                      <td className="muted small">
                        {item.rules.length ? item.rules.length + ' 条' : '尚未配置'}
                      </td>
                      <td>
                        <div className="row-actions">
                          {item.dashboard && (
                            <a
                              className="button small-button"
                              href={'#/boards/' + encodeURIComponent(item.dashboard)}
                            >
                              面板
                            </a>
                          )}
                          {canConfigure(scope) && (
                            <button
                              className="icon-button"
                              title="删除监控"
                              aria-label={'删除监控 ' + item.name}
                              onClick={() => setRemoving(item)}
                            >
                              <Icon name="trash" size={17} />
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
        </div>
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
            删除 <strong>{removing.name}</strong>？它的告警规则和同名面板会一起删除，历史数据保留到过期。
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
