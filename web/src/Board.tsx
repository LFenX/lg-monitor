import { useMemo, useState } from 'react'
import type { KeyboardEvent, MouseEvent } from 'react'
import { AddService, AgentAdd } from './AddService'
import { AgentCard } from './AgentCard'
import { DependencyMap, boardScope } from './DependencyMap'
import { CardGallery, ServiceCard, styleOf } from './cards'
import type { ParentOption } from './AddService'
import { Glyph, kindName } from './glyphs'
import { JobBriefText, JobStatusBadge } from './Job'
import { ago } from './kit'
import { useGate } from './access'
import { Dialog, Menu, dependencyPrompt, useAgentSetup } from './parts'
import { RoleIcon, roleOf, roles } from './roles'
import { Beats, Dot, UptimePill, latencyText, percentText, rowSlots, severity } from './status'
import { useApp, useResource } from './state'
import type { BoardView, CardStyle, Monitor } from './types'

type Group = { item: Monitor; below: Monitor[] }
type Layout = 'list' | 'cards' | 'map'

function remembered(key: string) {
  try {
    return localStorage.getItem(key) || ''
  } catch {
    return ''
  }
}
function remember(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* A private window may refuse storage; the default layout still works. */
  }
}

export function monitorHref(id: string) {
  return '#/monitors/' + encodeURIComponent(id)
}

/* Items arrive in display order (each service followed by its subtree); this regroups them. */
export type { Group }

export function groupItems(items: Monitor[]): Group[] {
  const groups: Group[] = []
  for (const item of items) {
    if (!item.depth || !groups.length) groups.push({ item, below: [] })
    else groups[groups.length - 1].below.push(item)
  }
  return groups
    .map((group, index) => ({ group, index }))
    .sort(
      (a, b) =>
        severity(a.group.item.overall || a.group.item.state) -
          severity(b.group.item.overall || b.group.item.state) || a.index - b.index,
    )
    .map(({ group }) => group)
}

export function parentOptions(items: Monitor[]): ParentOption[] {
  return items.map((item) => ({ id: item.id, name: item.name, depth: item.depth || 0 }))
}

/* What the row says under the name: the problem when there is one, otherwise what is watched. */
function subtitle(item: Monitor, childCount: number) {
  const host = item.hosts[0]
  if (item.issue) return <span className="issue">{item.issue}</span>
  // A job has no host checks: its line is its latest day.
  if (item.kind === 'job') return <JobBriefText brief={item.job} />
  if (item.state === 'down') return <span className="issue">{host?.reason || '检测失败'}</span>
  if (item.state === 'unknown') return <span className="wait">{host?.reason || '等待第一次检查'}</span>
  const parts = item.description ? [item.description] : [...item.watching]
  if (childCount) parts.push(childCount + ' 个子服务')
  if (item.depends?.length) parts.push('用到 ' + item.depends.length + ' 个服务')
  return parts.join(' · ')
}

export function ServiceRow({
  item,
  depth = 0,
  childCount = 0,
  expanded,
  toggle,
  remote = false,
  guides = [],
  last = false,
}: {
  item: Monitor
  depth?: number
  childCount?: number
  expanded?: boolean
  toggle?: () => void
  /* A board read from another computer has no service pages here to open. */
  remote?: boolean
  /* For each level above this row, whether that level's line still runs on below it. */
  guides?: boolean[]
  last?: boolean
}) {
  const { now } = useApp()
  const overall = item.overall || item.state
  const grouped = !!toggle && !!item.groupBeats?.length
  const beats = grouped ? item.groupBeats! : item.beats
  const uptime = grouped && item.groupUptime !== undefined ? item.groupUptime : item.uptime
  const open = () => {
    if (toggle) toggle()
    else if (!remote) window.location.hash = monitorHref(item.id)
  }
  const role = roleOf(item)
  const label = (
    <>
      <RoleIcon role={role} size={depth ? 22 : 28} title={roles[role].name + ' · ' + kindName[item.kind]} />
      <strong>{item.name}</strong>
    </>
  )
  return (
    <div
      className={'lm-row' + (depth ? ' child' : '') + (childCount ? ' parent' : '')}
      data-overall={overall}
      data-open={childCount && expanded ? '' : undefined}
      data-last={last || undefined}
      role="listitem"
      tabIndex={0}
      onClick={open}
      onKeyDown={(event: KeyboardEvent) => {
        if (event.key === 'Enter' && event.target === event.currentTarget) open()
      }}
    >
      {toggle ? (
        <button
          className="lm-chev"
          aria-expanded={!!expanded}
          aria-label={(expanded ? '收起' : '展开') + item.name + '的子服务'}
          onClick={(event: MouseEvent) => {
            event.stopPropagation()
            toggle()
          }}
        >
          <Glyph name="chevron" size={14} />
        </button>
      ) : (
        <span />
      )}
      <UptimePill value={uptime} state={overall} />
      <div className="lm-row-name" style={{ ['--depth' as string]: depth }}>
        {depth > 0 && (
          <span className="lm-tree" aria-hidden="true">
            {guides.map((on, index) => (
              <i key={index} data-on={on || undefined} />
            ))}
            <i data-elbow="" data-last={last || undefined} />
          </span>
        )}
        {remote ? (
          <span className="lm-row-title" title={kindName[item.kind] + ' · ' + item.watching.join(' · ')}>
            {label}
          </span>
        ) : (
          <a
            href={monitorHref(item.id)}
            onClick={(event) => event.stopPropagation()}
            title={kindName[item.kind] + ' · ' + item.watching.join(' · ')}
          >
            {label}
          </a>
        )}
        <span className="lm-row-sub">{subtitle(item, childCount)}</span>
      </div>
      {item.kind === 'job' && !grouped ? (
        <span className="lm-job-row">{item.job && <JobStatusBadge status={item.job.status} />}</span>
      ) : (
        <Beats
          slots={rowSlots(beats, now(), grouped ? item.groupBeatSlots : item.beatSlots)}
          liveState={overall}
          label={item.name + ' 最近 24 小时'}
        />
      )}
      <span className="lm-latency" title="最近 5 分钟平均响应">
        {item.latencyMs != null
          ? latencyText(item.latencyMs)
          : item.hosts[0]?.ts
            ? ago(item.hosts[0].ts, now())
            : ''}
      </span>
    </div>
  )
}

export function ServiceList({
  groups,
  forceOpen,
  remote = false,
}: {
  groups: Group[]
  forceOpen: boolean
  remote?: boolean
}) {
  const [toggled, setToggled] = useState<Record<string, boolean>>({})
  return (
    <div className="lm-list" role="list" aria-label="服务列表">
      {groups.map(({ item, below }) => {
        const expanded = forceOpen || (toggled[item.id] ?? (item.overall || item.state) === 'down')
        return [
          <ServiceRow
            key={item.id}
            item={item}
            remote={remote}
            childCount={below.length}
            expanded={expanded}
            toggle={
              below.length ? () => setToggled((value) => ({ ...value, [item.id]: !expanded })) : undefined
            }
          />,
          ...(expanded
            ? below.map((child, index) => {
                const depth = child.depth || 1
                // A level's line runs on while a later row still sits at that level before the tree climbs out.
                const runs = (level: number) => {
                  for (const next of below.slice(index + 1)) {
                    const at = next.depth || 1
                    if (at < level) return false
                    if (at === level) return true
                  }
                  return false
                }
                return (
                  <ServiceRow
                    key={child.id}
                    item={child}
                    depth={depth}
                    remote={remote}
                    guides={Array.from({ length: depth - 1 }, (_, level) => runs(level + 1))}
                    last={!runs(depth)}
                  />
                )
              })
            : []),
        ]
      })}
    </div>
  )
}

export function ServiceCards({
  groups,
  board,
  remote = false,
  pick,
  more,
}: {
  groups: Group[]
  board?: Pick<BoardView, 'cardStyle' | 'cards'>
  remote?: boolean
  pick?: (id: string, style: CardStyle) => void
  more?: () => void
}) {
  return (
    <div className="lm-cards" role="list" aria-label="服务卡片">
      {groups.map(({ item, below }) => (
        <ServiceCard
          key={item.id}
          item={item}
          below={below}
          style={styleOf(board, item.id)}
          remote={remote}
          pick={pick ? (style) => pick(item.id, style) : undefined}
          more={more}
        />
      ))}
    </div>
  )
}

export function Stats({ board }: { board: BoardView }) {
  const stats = board.summary
  return (
    <dl className="lm-stats" aria-label="面板概况">
      <div className="lm-stat">
        <dt>服务</dt>
        <dd>{stats.services}</dd>
      </div>
      <div className="lm-stat">
        <dt>子服务</dt>
        <dd>{stats.children}</dd>
      </div>
      <div className="lm-stat">
        <dt>
          <Dot state="up" />
          正常
        </dt>
        <dd>
          {stats.up}
          {stats.unknown > 0 && <small>{stats.unknown} 个等待</small>}
        </dd>
      </div>
      <div className="lm-stat" data-tone={stats.down ? 'down' : undefined}>
        <dt>
          <Dot state={stats.down ? 'down' : 'empty'} />
          异常
        </dt>
        <dd>{stats.down}</dd>
      </div>
      <div className="lm-stat">
        <dt>24 小时可用率</dt>
        <dd>{percentText(stats.uptime)}</dd>
      </div>
    </dl>
  )
}

function EmptyBoard({ board, manual }: { board: BoardView; manual: () => void }) {
  const gate = useGate()
  return (
    <section className="lm-empty" aria-label="空面板指引">
      <header className="lm-empty-head">
        <div>
          <h2>添加第一个服务</h2>
          <p>最省事的是交给 Agent：它会找到对应的端口、服务或进程，顺手把附属的服务挂好。</p>
        </div>
        <button className="lm-btn" onClick={() => gate('config', '手动添加服务', manual)}>
          <Glyph name="edit" size={15} />
          手动创建
        </button>
      </header>
      <AgentAdd board={board} />
    </section>
  )
}

function EditBoard({ board, close }: { board: BoardView; close: () => void }) {
  const { api, refresh } = useApp()
  const [name, setName] = useState(board.name),
    [description, setDescription] = useState(board.description || '')
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  return (
    <Dialog
      title="面板名称和简介"
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            取消
          </button>
          <button className="lm-btn primary" form="lm-edit-board" disabled={busy}>
            保存
          </button>
        </>
      }
    >
      <form
        id="lm-edit-board"
        style={{ display: 'grid', gap: 14 }}
        onSubmit={(event) => {
          event.preventDefault()
          setBusy(true)
          api
            .request('/boards/' + encodeURIComponent(board.id), {
              method: 'PUT',
              body: JSON.stringify({ name, description }),
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
          名称
          <input
            className="lm-input"
            required
            maxLength={40}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className="lm-field">
          简介
          <textarea
            className="lm-input"
            maxLength={200}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
          <small>打开内网可见后，局域网里的人能看到名称和这句简介，看不到服务状态。</small>
        </label>
      </form>
    </Dialog>
  )
}

function RemoveBoard({ board, close }: { board: BoardView; close: () => void }) {
  const { api, refresh } = useApp()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  return (
    <Dialog
      title={'删除面板「' + board.name + '」？'}
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
              api
                .request('/boards/' + encodeURIComponent(board.id), { method: 'DELETE' })
                .then(() => {
                  refresh()
                  window.location.hash = '#/'
                })
                .catch((err: unknown) => setError(err instanceof Error ? err.message : '删除失败'))
                .finally(() => setBusy(false))
            }}
          >
            删除面板
          </button>
        </>
      }
    >
      <p style={{ margin: 0 }}>
        上面的 {board.summary.services} 个服务和它们的告警会保留，只是不再显示在这个面板上。
      </p>
    </Dialog>
  )
}

export function BoardPage({ id }: { id: string }) {
  const { api, pulse, refresh } = useApp()
  const resource = useResource(
    (signal) => api.request<BoardView>('/boards/' + encodeURIComponent(id), { signal }),
    [pulse],
    id,
  )
  const board = resource.value
  const [layout, setLayout] = useState<Layout>(() => {
    const saved = remembered('lgmon-layout-' + id)
    return saved === 'cards' || saved === 'map' ? saved : 'list'
  })
  const [dialog, setDialog] = useState<'' | 'add' | 'manual' | 'edit' | 'remove' | 'cards'>('')
  const [filter, setFilter] = useState('')
  const [adopting, setAdopting] = useState(false)
  const groups = useMemo(() => groupItems(board?.items || []), [board])
  const shown = useMemo(() => {
    const words = filter.trim().toLowerCase()
    if (!words) return groups
    const hit = (item: Monitor) => (item.name + ' ' + item.watching.join(' ')).toLowerCase().includes(words)
    return groups.filter((group) => hit(group.item) || group.below.some(hit))
  }, [groups, filter])
  if (!board)
    return (
      <div className="lm-page">
        {resource.error ? (
          <p className="lm-note" data-tone="down">
            {resource.error}
          </p>
        ) : (
          <p className="lm-muted">正在读取面板…</p>
        )}
      </div>
    )
  const gate = useGate()
  const choose = (next: Layout) => {
    setLayout(next)
    remember('lgmon-layout-' + id, next)
  }
  const empty = board.items.length === 0
  return (
    <div className="lm-page">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">{board.name}</h1>
          {board.description && <p className="lm-lead">{board.description}</p>}
        </div>
        <div className="lm-actions">
          {!empty && (
            <div className="lm-seg" role="group" aria-label="显示方式">
              <button aria-pressed={layout === 'list'} onClick={() => choose('list')}>
                <Glyph name="list" size={15} />
                列表
              </button>
              <button aria-pressed={layout === 'cards'} onClick={() => choose('cards')}>
                <Glyph name="cards" size={15} />
                卡片
              </button>
              <button aria-pressed={layout === 'map'} onClick={() => choose('map')}>
                <Glyph name="graph" size={15} />
                关系
              </button>
            </div>
          )}
          <button className="lm-btn primary" onClick={() => setDialog('add')}>
            <Glyph name="plus" size={16} />
            添加服务
          </button>
          <Menu>
            <button onClick={() => setDialog('cards')}>
              <Glyph name="cards" size={16} />
              卡片样式
            </button>
            <button onClick={() => gate('config', '修改面板', () => setDialog('edit'))}>
              <Glyph name="edit" size={16} />
              改名和简介
            </button>
            <button className="danger" onClick={() => gate('config', '删除面板', () => setDialog('remove'))}>
              <Glyph name="trash" size={16} />
              删除面板
            </button>
          </Menu>
        </div>
      </header>
      {board.unassigned.length > 0 && (
        <div className="lm-note" data-tone="accent">
          还有 {board.unassigned.length} 个服务不在任何面板上。
          <button
            className="lm-btn small"
            disabled={adopting}
            onClick={() =>
              gate('config', '把服务放进面板', () => {
                setAdopting(true)
                api
                  .request('/boards/' + encodeURIComponent(board.id), {
                    method: 'PUT',
                    body: JSON.stringify({ add: board.unassigned }),
                  })
                  .then(refresh)
                  .finally(() => setAdopting(false))
              })
            }
          >
            放进这个面板
          </button>
        </div>
      )}
      {empty ? (
        <EmptyBoard board={board} manual={() => setDialog('manual')} />
      ) : (
        <>
          <Stats board={board} />
          {groups.length > 8 && layout !== 'map' && (
            <div className="lm-toolbar">
              <input
                className="lm-input"
                type="search"
                placeholder="筛选服务"
                value={filter}
                onChange={(event) => setFilter(event.target.value)}
                aria-label="筛选服务"
              />
            </div>
          )}
          {layout === 'map' ? (
            <BoardMap board={board} />
          ) : layout === 'cards' ? (
            <ServiceCards
              groups={shown}
              board={board}
              pick={(service, style) =>
                gate('config', '修改卡片样式', () =>
                  api
                    .request('/boards/' + encodeURIComponent(board.id), {
                      method: 'PUT',
                      body: JSON.stringify({ cards: { [service]: style } }),
                    })
                    .then(refresh),
                )
              }
              more={() => setDialog('cards')}
            />
          ) : (
            <ServiceList groups={shown} forceOpen={!!filter.trim()} />
          )}
        </>
      )}
      {(dialog === 'add' || dialog === 'manual') && (
        <AddService
          board={board}
          parents={parentOptions(board.items)}
          manual={dialog === 'manual'}
          close={() => setDialog('')}
        />
      )}
      {dialog === 'edit' && <EditBoard board={board} close={() => setDialog('')} />}
      {dialog === 'cards' && (
        <CardGallery board={board} close={() => setDialog('')} saved={() => choose('cards')} />
      )}
      {dialog === 'remove' && <RemoveBoard board={board} close={() => setDialog('')} />}
    </div>
  )
}

/* The board as a dependency map; with nothing linked yet, the agent card offers to work the links out. */
function BoardMap({ board }: { board: BoardView }) {
  const { monitors } = useApp()
  const setup = useAgentSetup('all')
  // The board's own items are freshest; the global list adds the services they link to elsewhere.
  const all = useMemo(() => {
    const own = new Map(board.items.map((item) => [item.id, item]))
    return [...board.items, ...monitors.filter((monitor) => !own.has(monitor.id))]
  }, [board.items, monitors])
  const scope = useMemo(
    () =>
      boardScope(
        all,
        board.items.map((item) => item.id),
      ),
    [all, board.items],
  )
  const linked = board.items.some((item) => item.depends?.length || item.dependents?.length)
  return (
    <div className="lm-map">
      <DependencyMap
        monitors={all}
        ids={scope.all}
        inside={scope.inside}
        label={board.name + ' 的依赖关系'}
      />
      {!linked && (
        <AgentCard
          quiet
          status={false}
          title="让 Agent 梳理依赖关系"
          lead="现在只画出了子服务。把这段话发给 Agent，它会查清服务之间谁调用谁、谁读写哪个数据库、经由哪条隧道，写好说明和依赖，图就完整了。"
          text={dependencyPrompt(board, setup)}
        />
      )}
    </div>
  )
}
