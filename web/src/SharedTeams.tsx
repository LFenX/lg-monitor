import { useMemo, useState } from 'react'
import { DashboardPanel } from './DashboardPanel'
import { useApp } from './state'
import { teamPath, useTeamResource } from './team'
import type { Joined, Shared, SharedData, TeamState } from './team'
import { Empty, ErrorNotice, Icon, Panel } from './ui'
import './teams.css'

export function TeamDashboardGroup() {
  const { api } = useApp()
  const state = useTeamResource((signal) => api.request<TeamState>('/teams/state', { signal }), [api])
  if (!state.value?.enabled) return null
  return (
    <section className="team-group">
      <div className="dashboard-heading">
        <div>
          <h2>团队共享</h2>
          <p className="muted">实时读取负责人分享给本设备的卡片。</p>
        </div>
        <a className="button" href="#/teams">
          团队与分享
        </a>
      </div>
      {state.value.joined > 0 ? (
        <SharedTeamList />
      ) : (
        <Panel title="团队已启用">
          <p className="team-padding muted">可以在“团队与分享”中管理成员和分享范围。</p>
        </Panel>
      )}
    </section>
  )
}

function SharedOwner({ joined }: { joined: Joined }) {
  const { api } = useApp()
  const source = useTeamResource(
    (signal) => api.request<Shared[]>('/team-joins/' + teamPath(joined.id) + '/shares', { signal }),
    [api, joined.id],
  )
  return (
    <Panel
      title={joined.ownerName + ' · ' + joined.teamName}
      extra={
        <button className="button small-button" disabled={source.loading} onClick={source.reload}>
          刷新
        </button>
      }
    >
      <div className="team-padding">
        <ErrorNotice message={source.error} />
        {source.value?.length ? (
          <div className="team-shared-list">
            {source.value.map((share) => (
              <a
                key={share.id}
                className="team-shared-link"
                href={'#/teams/' + teamPath(joined.id) + '/' + teamPath(share.id)}
              >
                <strong>{share.title}</strong>
                <span>{share.panels.length} 张卡片 · 只读</span>
                <Icon name="arrow" size={16} />
              </a>
            ))}
          </div>
        ) : (
          !source.error && (
            <Empty title={source.loading ? '正在读取共享面板' : '负责人尚未向本设备分享可用卡片'} />
          )
        )}
      </div>
    </Panel>
  )
}

export function SharedTeamList() {
  const { api } = useApp()
  const source = useTeamResource((signal) => api.request<Joined[]>('/team-joins', { signal }), [api])
  return (
    <div className="team-owner-list">
      <ErrorNotice message={source.error} />
      {source.value?.map((joined) => (
        <SharedOwner key={joined.id} joined={joined} />
      ))}
      {source.value?.length === 0 && <Empty title="本机尚未加入团队" />}
    </div>
  )
}

export function SharedTeamView({ joined, share }: { joined: string; share: string }) {
  const { api } = useApp()
  const path = '/team-joins/' + teamPath(joined) + '/shares/' + teamPath(share)
  const source = useTeamResource((signal) => api.request<Shared>(path, { signal }), [api, path])
  const [range, setRange] = useState('')
  const [mode, setMode] = useState('relative')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [collector, setCollector] = useState('')
  const [revision, setRevision] = useState(0)
  const definition = source.value
  const request = useMemo(() => {
    const values = new URLSearchParams()
    if (mode === 'relative') values.set('range', range || definition?.defaultRange || '1h')
    else {
      const start = new Date(from).getTime(),
        end = new Date(to).getTime()
      if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end)
        return { error: '请选择有效起止时间，开始须早于结束。', query: '' }
      values.set('from', String(start))
      values.set('to', String(end))
    }
    if (collector) values.set('collector', collector)
    return { error: '', query: values.toString() }
  }, [mode, from, to, range, collector, definition?.defaultRange])
  return (
    <div className="team-page">
      <div className="breadcrumbs">
        <a href="#/boards">面板</a>
        <span>/</span>
        <a href="#/teams">团队共享</a>
      </div>
      <div className="page-heading">
        <div>
          <p className="eyebrow">SHARED DASHBOARD</p>
          <h1>{definition?.title || '共享面板'}</h1>
          <p className="muted">
            {definition ? definition.ownerName + ' · ' + definition.teamName : '只读访问负责人分享的数据'}
          </p>
        </div>
        <button
          className="button"
          disabled={source.loading}
          onClick={() => {
            source.reload()
            setRevision((v) => v + 1)
          }}
        >
          <Icon name="refresh" size={16} />
          刷新共享面板
        </button>
      </div>
      <ErrorNotice message={source.error} />
      {source.error && (
        <Panel title="共享数据暂不可用">
          <p className="team-padding">
            已清空上次结果。请检查负责人连接、分享范围或本设备的成员身份，再重试。
          </p>
        </Panel>
      )}
      {!definition && !source.error && <Empty title="正在读取授权范围" />}
      {definition && (
        <>
          <div className="team-filters">
            <label>
              时间方式
              <select aria-label="时间方式" value={mode} onChange={(e) => setMode(e.target.value)}>
                <option value="relative">相对时间</option>
                <option value="fixed">固定时间</option>
              </select>
            </label>
            {mode === 'relative' ? (
              <label>
                时间范围
                <select
                  aria-label="时间范围"
                  value={range || definition.defaultRange}
                  onChange={(e) => setRange(e.target.value)}
                >
                  {[...new Set([definition.defaultRange, '1h', '6h', '24h', '7d'])].map((v) => (
                    <option key={v} value={v}>
                      {v}
                    </option>
                  ))}
                </select>
              </label>
            ) : (
              <>
                <label>
                  开始时间
                  <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} />
                </label>
                <label>
                  结束时间
                  <input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} />
                </label>
              </>
            )}
            <label>
              主机
              <select aria-label="主机" value={collector} onChange={(e) => setCollector(e.target.value)}>
                <option value="">分享范围内的全部主机</option>
                {definition.collectors.map((c) => (
                  <option value={c.id} key={c.id}>
                    {c.name} · {c.id}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <p className="small muted">仅显示负责人授权的卡片；每张卡片最快十秒刷新，结果不写入本机数据库。</p>
          <ErrorNotice message={request.error} />
          {!request.error && (
            <div className="dashboard-grid">
              {definition.panels.map((panel) => (
                <SharedCard
                  key={panel.id}
                  panel={panel}
                  path={path}
                  query={request.query}
                  revision={revision}
                  version={definition.version}
                />
              ))}
            </div>
          )}
          {definition.panels.length === 0 && <Empty title="该分享目前没有可用卡片" />}
        </>
      )}
    </div>
  )
}

function SharedCard({
  panel,
  path,
  query,
  revision,
  version,
}: {
  panel: Shared['panels'][number]
  path: string
  query: string
  revision: number
  version: string
}) {
  const { api } = useApp()
  const source = useTeamResource(
    (signal) => api.request<SharedData>(path + '/panels/' + teamPath(panel.id) + '?' + query, { signal }),
    [api, path, panel.id, query, revision, version],
  )
  return (
    <DashboardPanel
      panel={{ ...panel, query: '' }}
      result={source.value}
      error={source.error}
      loading={source.loading}
      params={source.value?.params}
    />
  )
}
