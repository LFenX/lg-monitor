/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { lazy, memo, Suspense, useMemo, useState } from 'react'
import type { KeyboardEvent, MouseEvent } from 'react'
import { AddService, AgentAdd } from './AddService'
import { AgentCard } from './AgentCard'
import { CardGallery, ServiceCard, styleOf } from './cards'

const BoardDependencyMap = lazy(() =>
  import('./DependencyMap').then((value) => ({ default: value.BoardDependencyMap })),
)
import type { StyleChoice } from './cards'
import type { ParentOption } from './AddService'
import { Glyph, kindName } from './glyphs'
import { useI18n } from './i18n'
import { JobBriefText, JobStatusBadge } from './Job'
import { ago } from './kit'
import { useGate } from './access'
import { Dialog, Menu, dependencyPrompt, promptChoices, useAgentSetup } from './parts'
import { RoleIcon, roleOf, roles } from './roles'
import {
  Beats,
  Dot,
  EntryLink,
  UptimePill,
  latencyText,
  ownIssue,
  percentText,
  rowSlots,
  severity,
} from './status'
import { useMonitorHref } from './shared'
import { PageLoading } from './loading'
import { useApp, useResource } from './state'
import type { BoardView, CardStyle, Monitor } from './types'
import { ExternalNote, ExternalSection, externalGraph } from './External'

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
    .map((group, index) => ({
      group,
      index,
      rank: group.below.reduce(
        (rank, child) => Math.min(rank, severity(child.state)),
        severity(group.item.state),
      ),
    }))
    .sort((a, b) => a.rank - b.rank || a.index - b.index)
    .map(({ group }) => group)
}

export function parentOptions(items: Monitor[]): ParentOption[] {
  return items.map((item) => ({ id: item.id, name: item.name, depth: item.depth || 0 }))
}

/* What the row says under the name: the problem when there is one, otherwise what is watched. */
function subtitle(item: Monitor, childCount: number, t: ReturnType<typeof useI18n>['t']) {
  const host = item.hosts[0]
  const issue = ownIssue(item)
  if (issue) return <span className="issue">{issue}</span>
  // A job has no host checks: its line is its latest day.
  if (item.kind === 'job') return <JobBriefText brief={item.job} />
  if (item.state === 'down') return <span className="issue">{host?.reason || t('boardCheckFailed')}</span>
  if (item.state === 'unknown')
    return <span className="wait">{host?.reason || t('boardWaitingFirstCheck')}</span>
  const parts = item.description ? [item.description] : [...item.watching]
  if (childCount) parts.push(t('boardSubServicesCount', { count: childCount }))
  const links = (item.depends?.length || 0) + (item.external?.length || 0)
  if (links) parts.push(t('boardDependsCount', { count: links }))
  return parts.join(' · ')
}

export const ServiceRow = memo(function ServiceRow({
  item,
  depth = 0,
  childCount = 0,
  expanded,
  toggle,
  guides = [],
  last = false,
}: {
  item: Monitor
  depth?: number
  childCount?: number
  expanded?: boolean
  toggle?: () => void
  /* For each level above this row, whether that level's line still runs on below it. */
  guides?: boolean[]
  last?: boolean
}) {
  const { now } = useApp()
  const { t } = useI18n()
  const href = useMonitorHref()
  const overall = item.state
  const { beats, uptime } = item
  const open = () => {
    if (toggle) toggle()
    else window.location.hash = href(item.id)
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
        <span className="lm-row-head">
          <a
            href={href(item.id)}
            onClick={(event) => event.stopPropagation()}
            title={kindName[item.kind] + ' · ' + item.watching.join(' · ')}
          >
            {label}
          </a>
          <EntryLink href={item.entry} name={item.name} />
        </span>
        <span className="lm-row-sub">{subtitle(item, childCount, t)}</span>
      </div>
      {item.kind === 'job' ? (
        <span className="lm-job-row">{item.job && <JobStatusBadge status={item.job.status} />}</span>
      ) : (
        <Beats
          slots={rowSlots(beats, now(), item.beatSlots)}
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
})

export function ServiceList({ groups, forceOpen }: { groups: Group[]; forceOpen: boolean }) {
  const [toggled, setToggled] = useState<Record<string, boolean>>({})
  return (
    <div className="lm-list" role="list" aria-label="服务列表">
      {groups.map(({ item, below }) => {
        const expanded =
          forceOpen ||
          (toggled[item.id] ?? (item.state === 'down' || below.some((child) => child.state === 'down')))
        return [
          <ServiceRow
            key={item.id}
            item={item}
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
  pick,
  more,
}: {
  groups: Group[]
  board?: Pick<BoardView, 'cardStyle' | 'cards'>
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
          pick={pick ? (style) => pick(item.id, style) : undefined}
          more={more}
        />
      ))}
    </div>
  )
}

export function Stats({ board }: { board: BoardView }) {
  const { t } = useI18n()
  const stats = board.summary
  return (
    <dl className="lm-stats" aria-label="面板概况">
      <div className="lm-stat">
        <dt>{t('statServices')}</dt>
        <dd>{stats.services}</dd>
      </div>
      <div className="lm-stat">
        <dt>{t('statChildren')}</dt>
        <dd>{stats.children}</dd>
      </div>
      <div className="lm-stat">
        <dt>
          <Dot state="up" />
          {t('statUp')}
        </dt>
        <dd>
          {stats.up}
          {stats.unknown > 0 && <small>{t('statWaiting', { count: stats.unknown })}</small>}
        </dd>
      </div>
      <div className="lm-stat" data-tone={stats.down ? 'down' : undefined}>
        <dt>
          <Dot state={stats.down ? 'down' : 'empty'} />
          {t('statDown')}
        </dt>
        <dd>{stats.down}</dd>
      </div>
      <div className="lm-stat">
        <dt>{t('statUptime24h')}</dt>
        <dd>{percentText(stats.uptime)}</dd>
      </div>
    </dl>
  )
}

function EmptyBoard({ board, manual }: { board: BoardView; manual: () => void }) {
  const gate = useGate()
  const { t } = useI18n()
  return (
    <section className="lm-empty" aria-label={t('boardEmptyAria')}>
      <header className="lm-empty-head">
        <div>
          <h2>{t('boardEmptyTitle')}</h2>
          <p>{t('boardEmptyLead')}</p>
        </div>
        <button className="lm-btn" onClick={() => gate('config', t('boardManualAddAction'), manual)}>
          <Glyph name="edit" size={15} />
          {t('boardManualCreate')}
        </button>
      </header>
      <AgentAdd board={board} />
    </section>
  )
}

function EditBoard({ board, close }: { board: BoardView; close: () => void }) {
  const { api, refresh } = useApp()
  const { t } = useI18n()
  const [name, setName] = useState(board.name),
    [description, setDescription] = useState(board.description || '')
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  return (
    <Dialog
      title={t('boardEditTitle')}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            {t('cancel')}
          </button>
          <button className="lm-btn primary" form="lm-edit-board" disabled={busy}>
            {t('save')}
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
          {t('boardNameLabel')}
          <input
            className="lm-input"
            required
            maxLength={40}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className="lm-field">
          {t('boardDescLabel')}
          <textarea
            className="lm-input"
            maxLength={200}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
          <small>{t('boardDescHint')}</small>
        </label>
      </form>
    </Dialog>
  )
}

function RemoveBoard({ board, close }: { board: BoardView; close: () => void }) {
  const { api, refresh } = useApp()
  const { t } = useI18n()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  return (
    <Dialog
      title={t('boardDeleteTitle', { name: board.name })}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            {t('cancel')}
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
            {t('boardDelete')}
          </button>
        </>
      }
    >
      <p style={{ margin: 0 }}>{t('boardDeleteNote', { count: board.summary.services })}</p>
    </Dialog>
  )
}

export function BoardPage({ id }: { id: string }) {
  const { api, pulse, refresh, guest } = useApp()
  const { t } = useI18n()
  const resource = useResource(
    (signal) => api.request<BoardView>('/boards/' + encodeURIComponent(id), { signal }),
    [pulse],
    id,
  )
  const loaded = resource.value
  // A chosen card style shows at once; the saved board catches up in the background.
  const [styles, setStyles] = useState<StyleChoice>({})
  const board = useMemo(() => {
    if (!loaded) return loaded
    const cards: Record<string, CardStyle> = { ...loaded.cards }
    for (const [id, style] of Object.entries(styles.cards || {})) {
      if (style) cards[id] = style
      else delete cards[id]
    }
    return { ...loaded, cardStyle: styles.cardStyle || loaded.cardStyle, cards }
  }, [loaded, styles])
  const applyStyle = (choice: StyleChoice) => {
    const before = styles
    setStyles((value) => ({
      cardStyle: choice.cardStyle || value.cardStyle,
      cards: { ...value.cards, ...choice.cards },
    }))
    return () => setStyles(before)
  }
  const gate = useGate()
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
          <PageLoading label={t('boardLoading')} />
        )}
      </div>
    )
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
          {(!empty || !!board.graph?.length) && (
            <div className="lm-seg" role="group" aria-label={t('boardDisplayMode')}>
              <button aria-pressed={layout === 'list'} onClick={() => choose('list')}>
                <Glyph name="list" size={15} />
                {t('viewList')}
              </button>
              <button aria-pressed={layout === 'cards'} onClick={() => choose('cards')}>
                <Glyph name="cards" size={15} />
                {t('viewCards')}
              </button>
              <button aria-pressed={layout === 'map'} onClick={() => choose('map')}>
                <Glyph name="graph" size={15} />
                {t('viewMap')}
              </button>
            </div>
          )}
          {!guest && (
            <button className="lm-btn primary" onClick={() => setDialog('add')}>
              <Glyph name="plus" size={16} />
              {t('addService')}
            </button>
          )}
          {!guest && (
            <Menu>
              <button onClick={() => setDialog('cards')}>
                <Glyph name="cards" size={16} />
                {t('boardCardStyle')}
              </button>
              <button onClick={() => gate('config', t('boardRenameDesc'), () => setDialog('edit'))}>
                <Glyph name="edit" size={16} />
                {t('boardRenameDesc')}
              </button>
              <button
                className="danger"
                onClick={() => gate('config', t('boardDelete'), () => setDialog('remove'))}
              >
                <Glyph name="trash" size={16} />
                {t('boardDelete')}
              </button>
            </Menu>
          )}
        </div>
      </header>
      {!guest && (board.unassigned?.length ?? 0) > 0 && (
        <div className="lm-note" data-tone="accent">
          {t('boardUnassigned', { count: board.unassigned.length })}
          <button
            className="lm-btn small"
            disabled={adopting}
            onClick={() =>
              gate('config', t('boardAdopt'), () => {
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
            {t('boardAdopt')}
          </button>
        </div>
      )}
      {empty && guest ? (
        layout === 'map' && board.graph?.length ? (
          <BoardMap board={board} />
        ) : (
          <p className="lm-muted">{t('lanNoSharedServices')}</p>
        )
      ) : empty ? (
        <EmptyBoard board={board} manual={() => setDialog('manual')} />
      ) : (
        <>
          <ExternalNote board={board} />
          <Stats board={board} />
          {groups.length > 8 && layout !== 'map' && (
            <div className="lm-toolbar">
              <input
                className="lm-input"
                type="search"
                placeholder={t('boardFilterPlaceholder')}
                value={filter}
                onChange={(event) => setFilter(event.target.value)}
                aria-label={t('boardFilterPlaceholder')}
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
                gate('config', '修改卡片样式', () => {
                  const undo = applyStyle({ cards: { [service]: style } })
                  api
                    .request('/boards/' + encodeURIComponent(board.id), {
                      method: 'PUT',
                      body: JSON.stringify({ cards: { [service]: style } }),
                    })
                    .then(refresh)
                    .catch(undo)
                })
              }
              more={() => setDialog('cards')}
            />
          ) : (
            <ServiceList groups={shown} forceOpen={!!filter.trim()} />
          )}
          {layout !== 'map' && <ExternalSection board={board} />}
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
        <CardGallery
          board={board}
          close={() => setDialog('')}
          saved={() => choose('cards')}
          apply={applyStyle}
        />
      )}
      {dialog === 'remove' && <RemoveBoard board={board} close={() => setDialog('')} />}
    </div>
  )
}

/* The board as a dependency map; with nothing linked yet, the agent card offers to work the links out. */
/* remote: someone else's board, which carries only the relations between the services it shares. */
export function BoardMap({ board, remote = false }: { board: BoardView; remote?: boolean }) {
  const { monitors, guest } = useApp()
  const { lang, t } = useI18n()
  const setup = useAgentSetup('all')
  // The board's own items are freshest; the global list adds the services they link to elsewhere.
  // Services on the account's other computers join as nodes of their own, linked from the services using them.
  const all = useMemo(() => {
    if (remote) return board.items
    const graph = externalGraph(board)
    const own = new Map(graph.items.map((item) => [item.id, item]))
    return [...graph.items, ...monitors.filter((monitor) => !own.has(monitor.id)), ...graph.nodes]
  }, [board, monitors, remote])
  const boardIDs = useMemo(() => board.items.map((item) => item.id), [board.items])
  const linked = board.items.some(
    (item) => item.depends?.length || item.dependents?.length || item.external?.length,
  )
  return (
    <div className="lm-map">
      <Suspense fallback={<PageLoading label={t('boardMapDrawing')} />}>
        <BoardDependencyMap
          monitors={all}
          boardItems={boardIDs}
          boardGraph={board.graph}
          label={t('boardMapLabel', { name: board.name })}
        />
      </Suspense>
      {!linked && remote && <p className="lm-muted lm-small">{t('boardMapRemoteEmpty')}</p>}
      {!remote && !guest && (
        <AgentCard
          quiet
          status={false}
          title={linked ? t('boardMapAgentTitleLinked') : t('boardMapAgentTitleEmpty')}
          lead={linked ? t('boardMapAgentLeadLinked') : t('boardMapAgentLeadEmpty')}
          text={dependencyPrompt(board, setup, lang)}
          choices={promptChoices(setup, true, board, lang)}
        />
      )}
    </div>
  )
}
