import { useEffect, useState } from 'react'
import { ago } from './kit'
import { bytes } from './model'
import { AddMonitorDialog } from './Monitors'
import type { MonitorPreset } from './Monitors'
import { useApp, useResource } from './state'
import type { ExploreItem, ExploreKind, ExploreResult } from './types'
import { Badge, Empty, ErrorNotice, Icon } from './ui'

const pageSize = 50
const tabs: { id: ExploreKind; label: string; hint: string }[] = [
  { id: 'ports', label: '监听端口', hint: '网站、API、数据库通常都在这里，最适合加监控。' },
  {
    id: 'services',
    label: 'Windows 服务',
    hint: '系统和软件注册的后台服务。“需要留意”是设为自动启动却停着的。',
  },
  { id: 'processes', label: '进程', hint: '按内存占用从高到低排列。' },
]
const serviceStates: Record<string, string> = {
  running: '运行中',
  stopped: '已停止',
  start_pending: '正在启动',
  stop_pending: '正在停止',
  paused: '已暂停',
}
const startTypes: Record<string, string> = {
  automatic: '自动',
  auto: '自动',
  automatic_delayed: '自动（延迟）',
  manual: '手动',
  disabled: '已禁用',
  boot: '引导',
  system: '系统',
}

function hashParam(name: string) {
  return new URLSearchParams(window.location.hash.split('?')[1] || '').get(name) || ''
}

function initialTab(): ExploreKind {
  const value = hashParam('kind')
  return value === 'services' || value === 'processes' ? value : 'ports'
}

export function Explore() {
  const { api, data, version } = useApp()
  const [kind, setKind] = useState<ExploreKind>(initialTab),
    [input, setInput] = useState(() => hashParam('q')),
    [query, setQuery] = useState(''),
    [state, setState] = useState(''),
    [offset, setOffset] = useState(0),
    [collector, setCollector] = useState(''),
    [adding, setAdding] = useState<MonitorPreset>()
  useEffect(() => {
    const timer = setTimeout(() => {
      setQuery(input.trim())
      setOffset(0)
    }, 220)
    return () => clearTimeout(timer)
  }, [input])
  const hosts = data?.collectors || []
  const host = collector || hosts.find((item) => item.online)?.id || hosts[0]?.id || ''
  // Refresh on the shared snapshot clock only every few ticks: a process list is costly for the collector.
  const tick = Math.floor(version / 4)
  const result = useResource(
    (signal) => {
      const params = new URLSearchParams({ kind, limit: String(pageSize), offset: String(offset) })
      if (query) params.set('q', query)
      if (state) params.set('state', state)
      if (host) params.set('collector', host)
      return api.request<ExploreResult>('/explore?' + params.toString(), { signal })
    },
    [api, kind, query, state, offset, host, tick],
    kind + ':' + host,
  )
  const value = result.value
  const counts = value?.counts || {}
  const tab = tabs.find((item) => item.id === kind)!
  const chips: [string, string, number | undefined][] =
    kind === 'services'
      ? [
          ['', '全部', value?.total],
          ['running', '运行中', counts.running],
          ['stopped', '已停止', counts.stopped],
          ['attention', '需要留意', counts.attention],
        ]
      : []
  return (
    <div className="fade-in">
      <div className="page-heading">
        <div>
          <p className="eyebrow">EXPLORE</p>
          <h1>探索这台电脑</h1>
          <p className="muted">看看上面在运行什么。找到想盯住的，点“监控”就加上。</p>
        </div>
        {hosts.length > 1 && (
          <label className="range-control">
            <Icon name="server" size={16} />
            <span className="sr-only">电脑</span>
            <select
              value={host}
              onChange={(event) => {
                setCollector(event.target.value)
                setOffset(0)
              }}
            >
              {hosts.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.hostname || item.id}
                  {item.online ? '' : '（离线）'}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      <div className="explore-tabs" role="tablist" aria-label="类型">
        {tabs.map((item) => (
          <button
            key={item.id}
            role="tab"
            aria-selected={kind === item.id}
            onClick={() => {
              setKind(item.id)
              setState('')
              setOffset(0)
            }}
          >
            {item.label}
          </button>
        ))}
      </div>
      <div className="explore-toolbar">
        <label className="search-field">
          <Icon name="search" size={16} />
          <span className="sr-only">搜索</span>
          <input
            value={input}
            onChange={(event) => setInput(event.target.value)}
            placeholder={
              kind === 'ports'
                ? '端口号、程序名…'
                : kind === 'services'
                  ? '服务名或显示名，例如 sql、tailscale'
                  : '进程名或路径'
            }
            type="search"
          />
        </label>
        {!!chips.length && (
          <div className="chips">
            {chips.map(([id, label, count]) => (
              <button
                className="chip"
                key={id || 'all'}
                aria-pressed={state === id}
                onClick={() => {
                  setState(id)
                  setOffset(0)
                }}
              >
                {label} {count != null && <span className="n">{count}</span>}
              </button>
            ))}
          </div>
        )}
        {kind === 'ports' && value && (
          <span className="muted small">
            {counts.network || 0} 个对外开放 · {counts.localOnly || 0} 个仅本机
          </span>
        )}
      </div>
      <p className="muted small" style={{ margin: '0 0 12px' }}>
        {tab.hint}
        {value?.collectedAt ? ' 数据采集于 ' + ago(value.collectedAt, Date.now()) + '。' : ''}
      </p>
      <ErrorNotice message={result.error} />
      {value?.notes
        ?.filter((note) => !note.startsWith('还有'))
        .map((note) => (
          <div className="notice notice-warning" key={note}>
            {note}
          </div>
        ))}
      {value && value.items.length ? (
        <div className="explore-table">
          <div className="table-scroll">
            <table>
              <thead>
                {kind === 'ports' ? (
                  <tr>
                    <th>端口</th>
                    <th>程序</th>
                    <th>监听地址</th>
                    <th>内存</th>
                    <th aria-label="操作" />
                  </tr>
                ) : kind === 'services' ? (
                  <tr>
                    <th>服务</th>
                    <th>状态</th>
                    <th>启动类型</th>
                    <th>PID</th>
                    <th aria-label="操作" />
                  </tr>
                ) : (
                  <tr>
                    <th>进程</th>
                    <th>内存</th>
                    <th>CPU</th>
                    <th>端口</th>
                    <th aria-label="操作" />
                  </tr>
                )}
              </thead>
              <tbody>
                {value.items.map((item, index) => (
                  <Row key={item.kind + item.name + index} item={item} add={setAdding} />
                ))}
              </tbody>
            </table>
          </div>
          <div className="explore-foot">
            <span>
              {value.matched === value.total
                ? '共 ' + value.total + ' 项'
                : '找到 ' + value.matched + ' 项（共 ' + value.total + '）'}
              ，显示第 {value.offset + 1}–{value.offset + value.items.length} 项
            </span>
            <span className="row-actions">
              <button
                className="button small-button"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - pageSize))}
              >
                上一页
              </button>
              <button
                className="button small-button"
                disabled={value.offset + value.items.length >= value.matched}
                onClick={() => setOffset(offset + pageSize)}
              >
                下一页
              </button>
            </span>
          </div>
        </div>
      ) : (
        <Empty title={result.loading ? '正在读取' : query ? '没有找到“' + query + '”' : '这里还没有数据'}>
          {query ? '换个关键词试试，或者问问左下角的波西米亚。' : !hosts.length ? '还没有电脑连接。' : ''}
        </Empty>
      )}
      {adding && <AddMonitorDialog preset={adding} close={() => setAdding(undefined)} />}
    </div>
  )
}

function Row({ item, add }: { item: ExploreItem; add: (preset: MonitorPreset) => void }) {
  const action = item.monitoredBy ? (
    <a className="badge badge-up" href="#/monitors" title={'已由监控 ' + item.monitoredBy + ' 覆盖'}>
      <span className="dot" />
      已监控
    </a>
  ) : item.suggest ? (
    <button className="button small-button" onClick={() => add(item.suggest!)}>
      <Icon name="plus" size={14} />
      监控
    </button>
  ) : null
  if (item.kind === 'port') {
    const local = item.address?.startsWith('127.') || item.address === '::1'
    return (
      <tr>
        <td className="port">{item.port}</td>
        <td>
          <div className="primary-line">{item.process}</div>
          <div className="secondary-line mono">
            {item.executable ||
              (item.services?.length ? '服务：' + item.services.join('、') : 'PID ' + item.pid)}
          </div>
        </td>
        <td>
          <span className="tag">{local ? '仅本机' : '对外'}</span>{' '}
          <span className="mono small">{item.address}</span>
        </td>
        <td className="small">{bytes(item.rss)}</td>
        <td>{action}</td>
      </tr>
    )
  }
  if (item.kind === 'service') {
    const attention =
      item.state === 'stopped' && (item.startType === 'automatic' || item.startType === 'auto')
    return (
      <tr>
        <td>
          <div className="primary-line">{item.displayName}</div>
          <div className="secondary-line mono">{item.name}</div>
        </td>
        <td>
          <Badge state={item.state === 'running' ? 'up' : attention ? 'warning' : 'unknown'}>
            {serviceStates[item.state || ''] || item.state}
          </Badge>
        </td>
        <td className="small">{startTypes[item.startType || ''] || item.startType}</td>
        <td className="mono small">{item.pid || '—'}</td>
        <td>{action}</td>
      </tr>
    )
  }
  return (
    <tr>
      <td>
        <div className="primary-line">{item.name}</div>
        <div className="secondary-line mono">{item.executable || 'PID ' + item.pid}</div>
      </td>
      <td className="small">{bytes(item.rss)}</td>
      <td className="small">
        {typeof item.cpuPercent === 'number' ? item.cpuPercent.toFixed(1) + '%' : '0.0%'}
      </td>
      <td className="mono small">{item.port || '—'}</td>
      <td>{action}</td>
    </tr>
  )
}
