/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { DashboardPanel } from './DashboardPanel'
import { HTMLDashboard } from './HTMLDashboard'
import { TeamDashboardGroup } from './SharedTeams'
import type { DashboardDefinition, DashboardPanelDefinition } from './dashboard'
import {
  AskAgent,
  CopyBox,
  LayoutPreview,
  SectionTitle,
  boardIcon,
  canConfigure,
  panelTypeNames,
  themeNames,
} from './kit'
import { useApp, useResource } from './state'
import type { BoardTemplate, Monitor, Query } from './types'
import { Empty, ErrorNotice, Icon, Modal } from './ui'
import './dashboards.css'

type Definition = Omit<DashboardDefinition, 'panels'> & {
  html?: { file: string; source: string }
  defaultRange: string
  file: string
  version: string
  error: string | null
  description?: string
  theme?: string
  icon?: string
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
  if (id) return <DashboardView key={id} id={id} />
  return <Gallery />
}

function Gallery() {
  const { api, boards, scope, refresh, monitors } = useApp()
  const templates = useResource(
    (signal) => api.request<BoardTemplate[]>('/dashboard-templates', { signal }),
    [api],
  )
  const [chosen, setChosen] = useState<BoardTemplate>()
  const categories = templates.value || []
  const custom = boards.filter((page) => !categories.some((template) => template.id === page.id))
  return (
    <div className="fade-in">
      <div className="page-head">
        <div>
          <h1>面板</h1>
          <p className="muted">
            一类内容一页：本机、服务、网站、告警、Windows 系统。已经生成的直接打开，没生成的点一下就有；Agent
            做的自定义面板列在下面。
          </p>
        </div>
      </div>
      <ErrorNotice message={templates.error} />
      {!boards.length && (
        <div className="notice setup-banner">
          <Icon name="board" size={18} />
          <span>
            <strong>还没有面板</strong>
            <span>每类内容一页：点下面任意一张的“生成这页”，或者让 Agent 调用 create_dashboard。</span>
          </span>
        </div>
      )}
      <div className="board-gallery">
        {categories.map((template) => {
          const page = boards.find((item) => item.id === template.id)
          const inner = (
            <>
              <div className="bc-head">
                <span className="board-icon">
                  <Icon name={boardIcon(template.icon)} size={18} />
                </span>
                <span>
                  <strong>{template.title}</strong>
                  <small>{(page?.panelCount ?? template.layout.length) + ' 张卡片'}</small>
                </span>
                {page ? <span className="bc-tag on">已生成</span> : <span className="bc-tag">未生成</span>}
              </div>
              <LayoutPreview layout={page?.layout || template.layout} />
              <p>{template.description}</p>
            </>
          )
          return page ? (
            <a className="board-card" key={template.id} href={'#/views/' + encodeURIComponent(page.id)}>
              {inner}
              <span className="bc-foot">
                打开 <Icon name="arrow" size={14} />
              </span>
            </a>
          ) : (
            <article className="board-card pending" key={template.id}>
              {inner}
              <span className="bc-foot">
                <button className="button primary small-button" onClick={() => setChosen(template)}>
                  <Icon name="plus" size={14} />
                  生成这页
                </button>
              </span>
            </article>
          )
        })}
      </div>
      <SectionTitle title="自定义面板" count={custom.length ? custom.length + ' 页' : undefined} />
      {custom.length ? (
        <div className="board-gallery">
          {custom.map((page) => (
            <a className="board-card" key={page.id} href={'#/views/' + encodeURIComponent(page.id)}>
              <div className="bc-head">
                <span className="board-icon">
                  <Icon name={boardIcon(page.icon)} size={18} />
                </span>
                <span>
                  <strong>{page.title}</strong>
                  <small>{page.html ? '自定义 HTML' : page.panelCount + ' 张卡片'}</small>
                </span>
                {page.theme && <span className="bc-tag">{themeNames[page.theme] || page.theme}</span>}
              </div>
              <LayoutPreview layout={page.layout} />
              <p>{page.error ? '文件有错误：' + page.error : page.description || page.id}</p>
              <span className="bc-foot">
                打开 <Icon name="arrow" size={14} />
              </span>
            </a>
          ))}
        </div>
      ) : (
        <AskAgent
          prompt="用 lg-monitor 帮我做一页面板：<说说你想看什么，比如“左边大大的 CPU 和内存，右边网站响应时间曲线，底下最近的告警”>。先用 get_schema 看看有哪些数据，再用 put_dashboard 保存。"
          note="需要分类面板之外的组合时，让 Agent 做：它可以自由组合 13 种卡片，保存前服务器会把每张卡片真跑一遍。把这句话改成你的想法交给它："
        />
      )}
      <TeamDashboardGroup />
      {chosen && (
        <TemplateDialog
          template={chosen}
          monitors={monitors}
          editable={canConfigure(scope)}
          close={() => setChosen(undefined)}
          done={(id) => {
            setChosen(undefined)
            refresh()
            window.location.hash = '#/views/' + encodeURIComponent(id)
          }}
        />
      )}
    </div>
  )
}

function TemplateDialog({
  template,
  monitors,
  editable,
  close,
  done,
}: {
  template: BoardTemplate
  monitors: Monitor[]
  editable: boolean
  close: () => void
  done: (id: string) => void
}) {
  const { api } = useApp()
  const [target, setTarget] = useState(''),
    [theme, setTheme] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  const prompt =
    '用 lg-monitor 的 create_dashboard 按模板 ' +
    template.id +
    (target ? '（target 用监控 ' + target + '）' : '') +
    (theme ? ' 生成一页面板，主题用 ' + theme : ' 生成一页面板') +
    '，然后把地址给我。'
  return (
    <Modal title={'生成「' + template.title + '」'} close={close}>
      <div className="form-grid">
        <p className="muted small" style={{ margin: 0 }}>
          {template.description}
        </p>
        <LayoutPreview layout={template.layout} />
        {template.targeted && monitors.length > 0 && (
          <label>
            范围
            <select value={target} onChange={(event) => setTarget(event.target.value)}>
              <option value="">全部监控</option>
              {monitors.map((item) => (
                <option key={item.id} value={item.id}>
                  只看 {item.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <label>
          配色
          <select value={theme} onChange={(event) => setTheme(event.target.value)}>
            <option value="">跟随网页（推荐）</option>
            {Object.entries(themeNames).map(([id, name]) => (
              <option key={id} value={id}>
                {name}
              </option>
            ))}
          </select>
        </label>
        <ErrorNotice message={error} />
        {editable ? (
          <div className="modal-actions">
            <button className="button" onClick={close}>
              取消
            </button>
            <button
              className="button primary"
              disabled={busy}
              onClick={() => {
                const controller = new AbortController()
                pending.current?.abort()
                pending.current = controller
                setBusy(true)
                setError('')
                const body: Record<string, string> = { template: template.id }
                if (theme) body.theme = theme
                if (target) body.target = target
                void api
                  .request<{ id: string }>('/dashboards', {
                    method: 'POST',
                    body: JSON.stringify(body),
                    signal: controller.signal,
                  })
                  .then((result) => {
                    if (!controller.signal.aborted) done(result.id)
                  })
                  .catch((err: unknown) => {
                    if (!controller.signal.aborted)
                      setError(err instanceof Error ? err.message : '生成面板失败')
                  })
                  .finally(() => {
                    if (!controller.signal.aborted) setBusy(false)
                  })
              }}
            >
              {busy ? '正在生成…' : '生成面板'}
            </button>
          </div>
        ) : (
          <AskAgent prompt={prompt} note="这个网页现在只能查看，不能直接保存。把这句话交给 Agent：" />
        )}
      </div>
    </Modal>
  )
}

const rangeNames: Record<string, string> = {
  '15m': '15 分钟',
  '1h': '1 小时',
  '6h': '6 小时',
  '24h': '24 小时',
  '7d': '7 天',
}

/* Sharing goes through the existing team feature; this explains where it lives and what it needs. */
function ShareDialog({
  id,
  title,
  html,
  close,
}: {
  id: string
  title: string
  html: boolean
  close: () => void
}) {
  const { scope } = useApp()
  return (
    <Modal title={'分享「' + title + '」'} close={close}>
      {html ? (
        <p>自定义 HTML 面板不能分享，只有声明式面板可以按卡片、主机和成员范围授权。</p>
      ) : (
        <>
          <p>
            分享给团队成员后，他们在自己的电脑上实时只读查看这页，可以只给部分卡片和主机，随时可以撤销。成员设备通过一次性邀请加入你的团队。
          </p>
          <ol className="share-steps">
            <li>在“团队”里建一个团队，生成邀请交给对方加入；</li>
            <li>选这页面板，勾选卡片、主机和成员，保存分享。</li>
          </ol>
          {scope === 'admin' ? (
            <div className="modal-actions">
              <button className="button" onClick={close}>
                取消
              </button>
              <a className="button primary" href={'#/teams?share=' + encodeURIComponent(id)} onClick={close}>
                去分享这页 <Icon name="arrow" size={16} />
              </a>
            </div>
          ) : (
            <AskAgent
              prompt={
                '用 lg-monitor 把面板 ' +
                id +
                ' 分享给团队：先 list_teams 看有哪些团队，向我确认要给哪些成员、哪些卡片和主机，再 create_share。'
              }
              note="团队与分享需要管理员权限，这个网页现在不能直接操作。把这句话交给接入了全部工具（--tools all）的 Agent："
            />
          )}
        </>
      )}
    </Modal>
  )
}

function BoardPicker({ current }: { current: string }) {
  const { boards } = useApp()
  if (boards.length < 2 && boards.some((page) => page.id === current)) return null
  return (
    <label>
      <span className="sr-only">选择面板</span>
      <select
        value={current}
        onChange={(event) => {
          window.location.hash = '#/views/' + encodeURIComponent(event.target.value)
        }}
      >
        {!boards.some((page) => page.id === current) && (
          <option value={current} disabled>
            此面板已删除，请重新选择
          </option>
        )}
        {boards.map((page) => (
          <option key={page.id} value={page.id}>
            {page.title}
            {page.error ? '（文件有错误）' : ''}
          </option>
        ))}
      </select>
    </label>
  )
}

function DashboardView({ id }: { id: string }) {
  const { api, data, dashboardChanges, status, scope, refresh } = useApp()
  const [revision, setRevision] = useState(0)
  const reload = useCallback(() => setRevision((value) => value + 1), [])
  const source = useResource(
    (signal) => api.request<Definition>('/dashboards/' + encodeURIComponent(id), { signal }),
    [api, id, dashboardChanges?.[id], revision, status],
    id,
  )
  const definition = source.value
  const [range, setRange] = useState(''),
    [mode, setMode] = useState('relative'),
    [from, setFrom] = useState(''),
    [to, setTo] = useState(''),
    [collector, setCollector] = useState(''),
    [editing, setEditing] = useState(false),
    [sharing, setSharing] = useState(false),
    [removing, setRemoving] = useState(false),
    [error, setError] = useState('')
  const effectiveRange = range || definition?.defaultRange || '1h'
  const usesCollector =
    !!definition?.html || !!definition?.panels.some((panel) => panel.parameters.includes('collector'))
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
  const query = request.query
  const htmlParams = useMemo<Record<string, unknown>>(
    () =>
      Object.fromEntries(
        [...new URLSearchParams(query)].map(([key, value]) => [
          key,
          key === 'from' || key === 'to' ? Number(value) : value,
        ]),
      ),
    [query],
  )
  const picker = <BoardPicker current={id} />
  if (source.error && !definition)
    return (
      <div className="fade-in">
        <div className="board-head">
          <a className="back" href="#/views" aria-label="返回面板库">
            <Icon name="arrow" size={17} />
          </a>
          <div className="board-tools">{picker}</div>
        </div>
        <ErrorNotice message={source.error} />
        <Empty title="打不开这页面板">
          可以从上面换一页，或者 <a href="#/views">返回面板库</a>。
        </Empty>
      </div>
    )
  if (!definition) return <div className="loading-state">正在读取面板</div>
  const ranges = ['15m', '1h', '6h', '24h', '7d']
  if (!ranges.includes(definition.defaultRange)) ranges.unshift(definition.defaultRange)
  return (
    <div className="board-view fade-in" data-board-theme={definition.theme || undefined}>
      <div className="board-head">
        <a className="back" href="#/views" aria-label="返回面板库">
          <Icon name="arrow" size={17} />
        </a>
        <span className="board-icon lg">
          <Icon name={boardIcon(definition.icon)} size={19} />
        </span>
        <div>
          <h1>{definition.title}</h1>
          <p>
            {definition.description ||
              (definition.html ? '自定义页面' : definition.panels.length + ' 张卡片')}
          </p>
        </div>
        <div className="board-tools">
          <span className="live-dot">
            <i />每 {definition.refresh} 刷新
          </span>
          {picker}
          <label>
            <span className="sr-only">时间方式</span>
            <select value={mode} onChange={(event) => setMode(event.target.value)}>
              <option value="relative">相对范围</option>
              <option value="fixed">固定起止</option>
            </select>
          </label>
          {mode === 'relative' ? (
            <label>
              <span className="sr-only">时间范围</span>
              <select value={effectiveRange} onChange={(event) => setRange(event.target.value)}>
                {ranges.map((value) => (
                  <option key={value} value={value}>
                    最近 {rangeNames[value] || value}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <>
              <label>
                <span className="sr-only">开始时间</span>
                <input type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} />
              </label>
              <label>
                <span className="sr-only">结束时间</span>
                <input type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} />
              </label>
            </>
          )}
          {usesCollector && (data?.collectors.length || 0) > 1 && (
            <label>
              <span className="sr-only">主机</span>
              <select
                aria-label="主机"
                value={collector}
                onChange={(event) => setCollector(event.target.value)}
              >
                <option value="">全部电脑</option>
                {data?.collectors.map((host) => (
                  <option key={host.id} value={host.id}>
                    {host.hostname || host.id}
                  </option>
                ))}
              </select>
            </label>
          )}
          <button className="icon-button" title="刷新面板" aria-label="刷新面板" onClick={reload}>
            <Icon name="refresh" size={18} />
          </button>
          <button className="button small-button" onClick={() => setSharing(true)}>
            <Icon name="share" size={15} />
            分享
          </button>
          <button className="button small-button" onClick={() => setEditing(true)}>
            <Icon name="sparkles" size={15} />
            改造这页
          </button>
          {canConfigure(scope) && !definition.html && (
            <button
              className="icon-button"
              title="删除面板"
              aria-label="删除面板"
              onClick={() => setRemoving(true)}
            >
              <Icon name="trash" size={17} />
            </button>
          )}
        </div>
      </div>
      <ErrorNotice
        message={definition.error ? '文件有错误，当前保留上一次成功的版本：' + definition.error : ''}
      />
      <ErrorNotice message={source.error} />
      <ErrorNotice message={error} />
      <ErrorNotice message={request.error} />
      {definition.html ? (
        !request.error && (
          <HTMLDashboard
            html={definition.html}
            version={definition.version}
            title={definition.title}
            refresh={definition.refresh}
            params={htmlParams}
            revision={revision}
          />
        )
      ) : (
        <div className="dashboard-grid">
          {definition.panels.map((panel) => (
            <LivePanel
              key={panel.id}
              page={id}
              panel={panel}
              refresh={definition.refresh}
              version={definition.version}
              query={query}
              invalid={request.error}
              reload={reload}
              refreshKey={revision}
            />
          ))}
        </div>
      )}
      <details className="dashboard-details">
        <summary>查询详情</summary>
        <p>
          {definition.file} · 版本 {definition.version}
        </p>
        {definition.panels.map((panel) => (
          <div key={panel.id}>
            <strong>
              {panel.title}（{panelTypeNames[panel.type] || panel.type}）
            </strong>
            <pre>{panel.query}</pre>
          </div>
        ))}
      </details>
      {sharing && (
        <ShareDialog
          id={id}
          title={definition.title}
          html={!!definition.html}
          close={() => setSharing(false)}
        />
      )}
      {editing && (
        <Modal title="改造这页面板" close={() => setEditing(false)}>
          <AskAgent
            prompt={
              '用 lg-monitor 改一下面板 ' +
              id +
              '：先 get_dashboard 读出现在的定义，然后<说说你想怎么改，比如“加一张内存排行”“换成 terminal 主题”“把 CPU 换成仪表盘”>，最后用 put_dashboard 保存。'
            }
            note="面板由 Agent 维护。把下面这句改成你的想法交给它，保存后这里会自动刷新："
          />
          <p className="muted small" style={{ marginTop: 14 }}>
            面板 ID
          </p>
          <CopyBox text={id} />
        </Modal>
      )}
      {removing && (
        <Modal title="删除面板" close={() => setRemoving(false)}>
          <p>
            删除面板 <strong>{definition.title}</strong>？监控和数据不受影响，之后可以从模板重新生成。
          </p>
          <div className="modal-actions">
            <button className="button" onClick={() => setRemoving(false)}>
              取消
            </button>
            <button
              className="button danger"
              onClick={() => {
                setError('')
                void api
                  .request('/dashboards/' + encodeURIComponent(id), { method: 'DELETE' })
                  .then(() => {
                    refresh()
                    window.location.hash = '#/views'
                  })
                  .catch((err: unknown) => setError(err instanceof Error ? err.message : '删除面板失败'))
                  .finally(() => setRemoving(false))
              }}
            >
              删除
            </button>
          </div>
        </Modal>
      )}
    </div>
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
    setError('')
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
  }, [api, page, panel.id, panel.error, panel.query, version, query, invalid, refresh, refreshKey])
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
