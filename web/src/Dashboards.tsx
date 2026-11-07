import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { DashboardPanel } from './DashboardPanel'
import type { DashboardDefinition, DashboardPanelDefinition } from './dashboard'
import { useApp, useResource } from './state'
import type { Query } from './types'
import { Empty, ErrorNotice, Icon, Panel } from './ui'
import './dashboards.css'

type Summary = {
  id: string
  title: string
  file: string
  version: string
  panelCount: number
  error: string | null
}
type Definition = Omit<DashboardDefinition, 'panels'> & {
  defaultRange: string
  file: string
  version: string
  error: string | null
  panels: (DashboardPanelDefinition & { parameters: string[]; error: string | null })[]
}
type Data = Query & { version: string; params: { from: number; to: number; collector: string | null } }

export function durationMillis(value: string) {
  const units: Record<string, number> = {
    ns: 0.000001,
    us: 0.001,
    µs: 0.001,
    μs: 0.001,
    ms: 1,
    s: 1000,
    m: 60000,
    h: 3600000,
    d: 86400000,
  }
  const parts = [...value.replace(/^\+/, '').matchAll(/(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h|d)/g)]
  return Math.max(
    1,
    parts.reduce((total, part) => total + Number(part[1]) * units[part[2]], 0),
  )
}

export function Dashboards({ id }: { id?: string }) {
  const { api, dashboardChanges } = useApp()
  const [revision, setRevision] = useState(0)
  const source = useResource(
    (signal) => api.request<Summary[]>('/dashboards', { signal }),
    [api, dashboardChanges, revision],
  )
  useEffect(() => {
    const timer = setInterval(() => setRevision((value) => value + 1), 10000)
    return () => clearInterval(timer)
  }, [])
  const current = id || source.value?.[0]?.id
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">DASHBOARDS</p>
          <h1>面板</h1>
          <p className="muted">按监控目标组合数据；每张卡片独立查询和刷新。</p>
        </div>
        <button className="button" onClick={() => setRevision((value) => value + 1)}>
          <Icon name="refresh" size={16} />
          刷新列表
        </button>
      </div>
      <ErrorNotice message={source.error} />
      {source.value?.length ? (
        <>
          <div className="dashboard-picker">
            <label>
              选择面板
              <select
                value={current}
                onChange={(event) => {
                  window.location.hash = '#/boards/' + encodeURIComponent(event.target.value)
                }}
              >
                {current && !source.value.some((page) => page.id === current) && (
                  <option value={current} disabled>
                    此面板已删除，请重新选择
                  </option>
                )}
                {source.value.map((page) => (
                  <option key={page.id} value={page.id}>
                    {page.title}
                    {page.error ? '（文件有错误）' : ''}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {current && <DashboardView key={current} id={current} listRevision={revision} />}
        </>
      ) : (
        <Panel title="声明式面板">
          <Empty title={source.loading ? '正在读取面板' : '还没有声明式面板'}>
            将面板定义放入监控器配置目录的 dashboards 文件夹，保存后会自动加载。仓库 examples/dashboards
            提供三个示例。
          </Empty>
        </Panel>
      )}
    </>
  )
}

function DashboardView({ id, listRevision }: { id: string; listRevision: number }) {
  const { api, data, dashboardChanges, status } = useApp()
  const [revision, setRevision] = useState(0)
  const reload = useCallback(() => setRevision((value) => value + 1), [])
  const source = useResource(
    (signal) => api.request<Definition>('/dashboards/' + encodeURIComponent(id), { signal }),
    [api, id, dashboardChanges?.[id], listRevision, revision, status],
    id,
  )
  const definition = source.value
  const [range, setRange] = useState(''),
    [mode, setMode] = useState('relative'),
    [from, setFrom] = useState(''),
    [to, setTo] = useState(''),
    [collector, setCollector] = useState('')
  const effectiveRange = range || definition?.defaultRange || '1h'
  const usesCollector = !!definition?.panels.some((panel) => panel.parameters.includes('collector'))
  const request = useMemo(() => {
    const params = new URLSearchParams()
    if (mode === 'relative') params.set('range', effectiveRange)
    else {
      const start = new Date(from).getTime(),
        stop = new Date(to).getTime()
      if (!Number.isFinite(start) || !Number.isFinite(stop) || start >= stop)
        return { error: '请选择有效的固定起止时间，开始必须早于结束。', query: '' }
      params.set('from', String(start))
      params.set('to', String(stop))
    }
    if (usesCollector && collector) params.set('collector', collector)
    return { error: '', query: params.toString() }
  }, [mode, effectiveRange, from, to, collector, usesCollector])
  if (source.error) return <ErrorNotice message={source.error} />
  if (!definition) return <Empty title="正在读取面板定义" />
  const ranges = ['1h', '6h', '24h', '7d']
  if (!ranges.includes(definition.defaultRange)) ranges.unshift(definition.defaultRange)
  return (
    <>
      <div className="dashboard-heading">
        <div>
          <h2>{definition.title}</h2>
          <p className="small muted">每 {definition.refresh} 刷新；相对时间使用监控器时钟。</p>
        </div>
        <button className="button" disabled={source.loading} onClick={reload}>
          刷新面板
        </button>
      </div>
      <ErrorNotice
        message={definition.error ? '文件有错误，当前保留上一次成功的版本：' + definition.error : ''}
      />
      <div className="dashboard-controls">
        <label>
          时间方式
          <select value={mode} onChange={(event) => setMode(event.target.value)}>
            <option value="relative">相对范围</option>
            <option value="fixed">固定起止</option>
          </select>
        </label>
        {mode === 'relative' ? (
          <label>
            时间范围
            <select value={effectiveRange} onChange={(event) => setRange(event.target.value)}>
              {ranges.map((value) => (
                <option key={value} value={value}>
                  最近{' '}
                  {(
                    { '1h': '1 小时', '6h': '6 小时', '24h': '24 小时', '7d': '7 天' } as Record<
                      string,
                      string
                    >
                  )[value] || value}
                </option>
              ))}
            </select>
          </label>
        ) : (
          <>
            <label>
              开始时间
              <input type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} />
            </label>
            <label>
              结束时间
              <input type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} />
            </label>
          </>
        )}
        {usesCollector && (
          <label>
            主机
            <select
              aria-label="主机"
              value={collector}
              onChange={(event) => setCollector(event.target.value)}
            >
              <option value="">全部主机</option>
              {data?.collectors.map((host) => (
                <option key={host.id} value={host.id}>
                  {host.hostname || host.id}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      <ErrorNotice message={request.error} />
      <div className="dashboard-grid">
        {definition.panels.map((panel) => (
          <LivePanel
            key={panel.id}
            page={id}
            panel={panel}
            refresh={definition.refresh}
            version={definition.version}
            query={request.query}
            invalid={request.error}
            reload={reload}
            refreshKey={revision}
          />
        ))}
      </div>
      <details className="dashboard-details">
        <summary>面板详情</summary>
        <p>
          {definition.file} · 版本 {definition.version}
        </p>
        {definition.panels.map((panel) => (
          <div key={panel.id}>
            <strong>{panel.title}</strong>
            <pre>{panel.query}</pre>
          </div>
        ))}
      </details>
    </>
  )
}

function LivePanel({
  page,
  panel,
  refresh,
  version,
  query,
  invalid,
  reload,
  refreshKey,
}: {
  page: string
  panel: Definition['panels'][number]
  refresh: string
  version: string
  query: string
  invalid: string
  reload: () => void
  refreshKey: number
}) {
  const { api } = useApp()
  const [data, setData] = useState<Data>(),
    [error, setError] = useState(''),
    [loading, setLoading] = useState(false)
  const update = useRef(reload)
  update.current = reload
  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    setData(undefined)
    setError('')
    setLoading(false)
    if (panel.error || invalid) return
    const load = async () => {
      setLoading(true)
      try {
        const result = await api.request<Data>(
          '/dashboards/' +
            encodeURIComponent(page) +
            '/panels/' +
            encodeURIComponent(panel.id) +
            '/data?' +
            query,
          { signal: controller.signal },
        )
        if (controller.signal.aborted) return
        if (result.version !== version) {
          update.current()
          return
        }
        setData(result)
        setError('')
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '卡片读取失败')
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false)
          timer = setTimeout(() => void load(), Math.min(2147483647, durationMillis(refresh)))
        }
      }
    }
    void load()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [api, page, panel.id, panel.error, panel.query, version, query, refresh, invalid, refreshKey])
  return (
    <DashboardPanel
      panel={panel}
      result={data}
      params={data?.params}
      loading={loading}
      error={panel.error || invalid || error}
    />
  )
}
