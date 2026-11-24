/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { allows, askAccess, useGate } from './access'
import { AgentCard } from './AgentCard'
import { Avatar, AvatarPicker, byOwner } from './avatar'
import { BrandMark, Wordmark } from './brand'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import type { Lang } from './i18n'
import { OSBadge } from './os'
import { Dialog, promptChoices, startPrompt, useAgentSetup } from './parts'
import { useSystemRestart } from './restart'
import { openBoardPage } from './reveal'
import { useApp, useResource } from './state'
import { Dot, ownIssue } from './status'
import { forgetKeptToken, hasKeptToken } from './tokens'
import type {
  BoardInfo,
  BoardStats,
  CloudBoardView,
  CloudDeviceSummary,
  LANOutgoing,
  LANRemoteBoard,
  Monitor,
  Profile,
} from './types'
import { inLauncher } from './launcher'
import { useTripleClick } from './license'
import { Credits, Signature } from './credits'
import { departmentTree, flatten, useDepartments } from './departments'
import { ago } from './kit'
import './start.css'

function boardLink(href: string, after?: () => void) {
  return {
    href,
    target: '_blank' as const,
    onClick: (event: ReactMouseEvent<HTMLAnchorElement>) => {
      after?.()
      if (
        inLauncher() ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey ||
        event.button !== 0
      )
        return
      event.preventDefault()
      openBoardPage(href)
    },
  }
}

/*
 * What opening lgmon shows, in lgmon's own window: whether it runs, how the services are, and the one next step.
 * A headline, the services with their sub-services, the agent card, and the board at the bottom.
 */

// Updates exist only inside lgmon's own window, so a browser never downloads that code.
const UpdateCard = lazy(() => import('./update').then((module) => ({ default: module.UpdateCard })))
const UpdateLink = lazy(() => import('./update').then((module) => ({ default: module.UpdateLink })))

const shown = 6

type Mood = 'loading' | 'offline' | 'empty' | 'down' | 'waiting' | 'up'

function mood(loading: boolean, offline: boolean, services: Monitor[], down: Monitor[]): Mood {
  if (offline) return 'offline'
  if (loading) return 'loading'
  if (!services.length) return 'empty'
  if (down.length) return 'down'
  return services.some((monitor) => monitor.state !== 'up') ? 'waiting' : 'up'
}

// "All clear" only when every service has answered; a fresh service without data yet is said as such.
function headline(state: Mood, services: Monitor[], down: Monitor[]) {
  switch (state) {
    case 'offline':
      return '暂时读不到状态'
    case 'loading':
      return '正在读取…'
    case 'empty':
      return '还没有在监控服务'
    case 'down':
      return down.every((item) => item.parent) ? down.length + ' 个子服务异常' : down.length + ' 个服务异常'
    case 'waiting': {
      const waiting = services.filter((monitor) => monitor.state !== 'up').length
      return waiting === services.length ? '正在检查服务…' : waiting + ' 个服务还没有数据'
    }
  }
  return '一切正常'
}

// Troubled ones first, so the window answers "is anything wrong" at a glance; otherwise the board's order.
function troubledFirst(items: Monitor[], families?: Set<string>) {
  const troubled = (item: Monitor) => item.state === 'down' || families?.has(item.id)
  return [...items.filter(troubled), ...items.filter((item) => !troubled(item))]
}

function rowNote(monitor: Monitor, lang: Lang = 'zh') {
  const en = lang === 'en'
  const state = monitor.state
  if (state === 'down') return ownIssue(monitor) || (en ? 'Down' : '异常')
  if (state !== 'up') return en ? 'No data yet' : '还没有数据'
  const uptime = monitor.uptime
  return typeof uptime === 'number'
    ? uptime >= 99.95
      ? '100%'
      : uptime.toFixed(1) + '%'
    : en
      ? 'Up'
      : '正常'
}

/* Every state under a service, so a collapsed row still tells whether something below it is wrong. */
function descendants(monitor: Monitor, all: Monitor[], depth = 0): Monitor[] {
  if (depth > 3) return []
  const direct = all.filter((item) => item.parent === monitor.id)
  return direct.flatMap((child) => [child, ...descendants(child, all, depth + 1)])
}

const foldKey = 'lgmon-start-fold'
function readFolds(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(foldKey) || '{}') as Record<string, boolean>
  } catch {
    return {}
  }
}

/* Open or closed per service, remembered; a service whose parts are failing opens until the person closes it. */
function useFolds() {
  const [folds, setFolds] = useState(readFolds)
  const toggle = (id: string, open: boolean) =>
    setFolds((previous) => {
      const next = { ...previous, [id]: open }
      try {
        localStorage.setItem(foldKey, JSON.stringify(next))
      } catch {
        // A window without storage still folds for this session.
      }
      return next
    })
  return { folds, toggle }
}

const subShown = 6

/* A service and, under it, the parts it depends on (a tunnel, a gateway, a database), folded behind a count. */
function ServiceRow({
  monitor,
  all,
  folds,
  level = 0,
  href = (id) => '#/monitors/' + encodeURIComponent(id),
  foldKey = '',
}: {
  monitor: Monitor
  all: Monitor[]
  folds: ReturnType<typeof useFolds>
  level?: number
  /* Where a row leads: this computer's service page, or another device's synced one. */
  href?: (id: string) => string
  /* Keeps another device's folds apart from this computer's services of the same ID. */
  foldKey?: string
}) {
  const { lang } = useI18n()
  const en = lang === 'en'
  const [more, setMore] = useState(false)
  const children = level < 3 ? troubledFirst(all.filter((item) => item.parent === monitor.id)) : []
  const below = children.length ? descendants(monitor, all) : []
  const failing = below.filter((item) => item.state === 'down').length
  const fold = (foldKey ? foldKey + ':' : '') + monitor.id
  const open = folds.folds[fold] ?? failing > 0
  const visible = more ? children : children.slice(0, subShown)
  return (
    <li data-state={monitor.state} data-open={children.length ? open : undefined}>
      <div className="lm-start-row">
        <a {...boardLink(href(monitor.id))}>
          <Dot state={monitor.state} />
          <span className="name">{monitor.name}</span>
          <span className="note">{rowNote(monitor, lang)}</span>
        </a>
        {children.length > 0 && (
          <button
            className="lm-start-fold"
            aria-expanded={open}
            aria-label={
              en
                ? `${open ? 'Collapse' : 'Expand'} sub-services of ${monitor.name}`
                : `${open ? '收起' : '展开'}${monitor.name}的子服务`
            }
            title={
              en
                ? `${below.length} sub-services${failing ? `, ${failing} down` : ''}`
                : `${below.length} 个子服务${failing ? `，${failing} 个异常` : ''}`
            }
            onClick={() => folds.toggle(fold, !open)}
          >
            <span className="lm-start-kids" aria-hidden="true">
              {below.slice(0, 6).map((item) => (
                <i key={item.id} data-state={item.state} />
              ))}
            </span>
            <small>{failing ? (en ? `${failing} down` : `${failing} 异常`) : below.length}</small>
            <Glyph name="chevron" size={14} />
          </button>
        )}
      </div>
      {open && children.length > 0 && (
        <ul
          className="lm-start-sub"
          aria-label={en ? `Sub-services of ${monitor.name}` : monitor.name + ' 的子服务'}
        >
          {visible.map((child) => (
            <ServiceRow
              key={child.id}
              monitor={child}
              all={all}
              folds={folds}
              level={level + 1}
              href={href}
              foldKey={foldKey}
            />
          ))}
          {children.length > visible.length && (
            <li className="more">
              <button onClick={() => setMore(true)}>
                {en
                  ? `Show ${children.length - visible.length} more`
                  : `再显示 ${children.length - visible.length} 个`}
              </button>
            </li>
          )}
        </ul>
      )}
    </li>
  )
}
type RemoteHealth = { summary?: BoardStats; error?: string }

/* Each shared board's health, read through this computer's lgmon from its owner, again every 20 seconds. */
function useRemoteHealth(items: LANOutgoing[]) {
  const { api } = useApp()
  const [health, setHealth] = useState<Record<string, RemoteHealth>>({})
  const key = items.map((item) => item.id).join(',')
  useEffect(() => {
    if (!key) return
    const controller = new AbortController()
    const load = () =>
      Promise.all(
        key.split(',').map((id) =>
          api
            .request<LANRemoteBoard>('/lan/remote/' + encodeURIComponent(id), { signal: controller.signal })
            .then((remote): [string, RemoteHealth] => [id, { summary: remote.board.summary }])
            .catch((err: unknown): [string, RemoteHealth] => [
              id,
              { error: err instanceof Error ? err.message : '读不到' },
            ]),
        ),
      ).then((entries) => {
        if (!controller.signal.aborted) setHealth(Object.fromEntries(entries))
      })
    void load()
    const timer = setInterval(() => void load(), 20_000)
    return () => {
      controller.abort()
      clearInterval(timer)
    }
  }, [api, key])
  return health
}

function remoteNote(health: RemoteHealth | undefined, lang: Lang = 'zh') {
  const en = lang === 'en'
  const summary = health?.summary
  if (!summary) return health?.error ? (en ? 'Unreachable' : '连不上对方') : en ? 'Loading…' : '正在读取…'
  if (summary.down) return en ? `${summary.down} down` : `${summary.down} 个异常`
  if (summary.state === 'empty') return en ? 'No services' : '还没有服务'
  if (summary.state !== 'up') return en ? 'No data yet' : '还没有数据'
  return typeof summary.uptime === 'number'
    ? summary.uptime >= 99.95
      ? '100%'
      : summary.uptime.toFixed(1) + '%'
    : en
      ? 'Up'
      : '正常'
}

/* Boards other people let this computer see: one group per person, with their avatar and nickname. */
function RemoteBoards({ items, health }: { items: LANOutgoing[]; health: Record<string, RemoteHealth> }) {
  const { lang, t } = useI18n()
  return (
    <section id="start-remote" className="lm-start-remote" aria-label={t('startSharedTitle')}>
      <h2>{t('startSharedTitle')}</h2>
      <ul className="lm-start-list">
        {byOwner(items).map(([key, boards]) => (
          <li key={key} className="lm-start-person">
            <p>
              <Avatar value={boards[0].ownerAvatar} seed={boards[0].fingerprint} size={22} />
              <span>{boards[0].ownerName}</span>
            </p>
            <ul>
              {boards.map((item) => {
                const state = health[item.id]?.summary?.state
                const dot = state === 'up' || state === 'down' ? state : 'unknown'
                return (
                  <li key={item.id} data-state={dot}>
                    <div className="lm-start-row">
                      <a {...boardLink('#/lan/remote/' + encodeURIComponent(item.id))}>
                        <Dot state={health[item.id]?.error ? 'unknown' : dot} />
                        <span className="name">{item.boardName}</span>
                        <span className="note" title={health[item.id]?.error}>
                          {remoteNote(health[item.id], lang)}
                        </span>
                      </a>
                    </div>
                  </li>
                )
              })}
            </ul>
          </li>
        ))}
      </ul>
    </section>
  )
}

type SharedPerson = { key: string; name: string; avatar?: string; boards: string[] }

/* Shown on hover or focus of the shared chip: everyone sharing with this computer and their boards. */
function SharedPeople({ people }: { people: SharedPerson[] }) {
  return (
    <span className="lm-start-chip-pop" role="tooltip">
      {people.map((person) => (
        <span key={person.key} className="lm-start-chip-person">
          <Avatar value={person.avatar} seed={person.key} size={18} />
          <span>
            <b>{person.name}</b>
            <small>{person.boards.join(' · ')}</small>
          </span>
        </span>
      ))}
    </span>
  )
}

function cloudBoardLink(deviceId: string, boardId: string) {
  return '#/cloud/' + encodeURIComponent(deviceId) + '/boards/' + encodeURIComponent(boardId)
}

type CloudTone = 'up' | 'down' | 'unknown' | 'off'

function cloudTone(device: CloudDeviceSummary): CloudTone {
  if (!device.online) return 'off'
  if (device.downCount > 0) return 'down'
  return device.boards.some((board) => board.summary.up > 0) ? 'up' : 'unknown'
}

/* "5 services · 17 sub-services": a device's total alone mixed the two and read as more services than it has. */
function countText(services: number, children: number, en: boolean) {
  const parts = en
    ? [services + (services === 1 ? ' service' : ' services'), children ? children + ' sub-services' : '']
    : [services + ' 个服务', children ? children + ' 个子服务' : '']
  return parts.filter(Boolean).join(' · ')
}

/* One board of another device, opened: its services as they sync, each with its sub-services folded beneath. */
function CloudBoardServices({
  device,
  board,
  folds,
}: {
  device: CloudDeviceSummary
  board: BoardInfo
  folds: ReturnType<typeof useFolds>
}) {
  const { api } = useApp()
  const { lang } = useI18n()
  const view = useResource(
    (signal) =>
      api.request<CloudBoardView>(
        '/cloud/devices/' + encodeURIComponent(device.id) + '/boards/' + encodeURIComponent(board.id),
        { signal },
      ),
    [device.stateUpdated],
    device.id + ':' + board.id,
  )
  const items = view.value?.board.items
  if (!items)
    return (
      <p className="lm-start-cloud-loading">
        {view.error ? view.error : lang === 'en' ? 'Loading services…' : '正在读取服务…'}
      </p>
    )
  const ids = new Set(items.map((item) => item.id))
  const roots = troubledFirst(items.filter((item) => !item.parent || !ids.has(item.parent)))
  const href = (id: string) =>
    '#/cloud/' + encodeURIComponent(device.id) + '/monitors/' + encodeURIComponent(id)
  return (
    <ul className="lm-start-list lm-start-cloud-services" aria-label={board.name}>
      {roots.map((monitor) => (
        <ServiceRow
          key={monitor.id}
          monitor={monitor}
          all={items}
          folds={folds}
          href={href}
          foldKey={device.id}
        />
      ))}
    </ul>
  )
}

/* The account's other computers, synced through the cloud. Each starts folded as a small card that says whether
   all is well; opening it lists its boards with their services and sub-services. */
function CloudDevices({ devices }: { devices: CloudDeviceSummary[] }) {
  const { lang, t } = useI18n()
  const en = lang === 'en'
  const { now } = useApp()
  const folds = useFolds()
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const [boardOpen, setBoardOpen] = useState<Record<string, boolean>>({})
  return (
    <section id="start-cloud" className="lm-start-cloud" aria-label={t('startCloudTitle')}>
      <h2>
        <Glyph name="cloud" size={14} />
        <span>{t('startCloudTitle')}</span>
        <em>{devices.length}</em>
      </h2>
      <ul className="lm-start-cloud-list">
        {devices.map((device) => {
          const tone = cloudTone(device)
          const expanded = open[device.id] ?? false
          const seen = device.stateUpdated || device.lastSeen
          const services = device.boards.reduce((sum, board) => sum + board.summary.services, 0)
          const children = device.boards.reduce((sum, board) => sum + board.summary.children, 0)
          const counts = countText(services, children, en)
          const meta = device.online
            ? (en ? 'Online · ' : '在线 · ') + counts
            : (en ? 'Last seen ' : '') + (seen ? ago(seen, now()) : '—') + (en ? ' · ' : '在线 · ') + counts
          const pill =
            tone === 'off'
              ? t('startCloudOffline')
              : tone === 'down'
                ? t('startCloudDownShort', { count: device.downCount })
                : tone === 'up'
                  ? t('startCloudAllUp')
                  : t('startCloudWaiting')
          return (
            <li
              key={device.id}
              className="lm-start-cloud-dev"
              data-state={tone}
              data-open={expanded || undefined}
            >
              <button
                type="button"
                className="lm-start-cloud-head"
                aria-expanded={expanded}
                aria-label={t('startCloudToggle', { name: device.name })}
                onClick={() => setOpen((state) => ({ ...state, [device.id]: !expanded }))}
              >
                <span className="lm-start-cloud-face" data-online={device.online || undefined}>
                  <Avatar value={device.avatar} seed={device.id} size={30} />
                  <i aria-hidden="true" />
                </span>
                <span className="lm-start-cloud-name">
                  <span className="lm-start-cloud-title">
                    <b>{device.name}</b>
                    <OSBadge os={device.os} compact />
                  </span>
                  <small>{meta}</small>
                  {device.boards.length > 0 && (
                    <span className="lm-start-cloud-bar" aria-hidden="true">
                      {/* Each board splits into its failing and working parts, so one fault in twenty does not
                          paint the whole board red. */}
                      {device.boards.flatMap((board) => {
                        const total = Math.max(board.summary.services + board.summary.children, 1)
                        if (!device.online || (board.summary.state !== 'up' && !board.summary.down))
                          return [<i key={board.id} style={{ flexGrow: total }} data-state="unknown" />]
                        const down = Math.min(board.summary.down, total)
                        return [
                          down > 0 && (
                            <i key={board.id + ':down'} style={{ flexGrow: down }} data-state="down" />
                          ),
                          down < total && (
                            <i key={board.id + ':up'} style={{ flexGrow: total - down }} data-state="up" />
                          ),
                        ].filter(Boolean)
                      })}
                    </span>
                  )}
                </span>
                <span className="lm-start-cloud-pill" data-state={tone}>
                  {pill}
                </span>
                <Glyph name="chevron" size={14} />
              </button>
              {expanded && (
                <ul className="lm-start-cloud-boards" aria-label={device.name}>
                  {device.boards.length === 0 ? (
                    <li className="empty">{t('startCloudNoBoards')}</li>
                  ) : (
                    device.boards.map((board) => {
                      const state = !device.online
                        ? 'unknown'
                        : board.summary.state === 'up' || board.summary.state === 'down'
                          ? board.summary.state
                          : 'unknown'
                      const key = device.id + ':' + board.id
                      const showing = boardOpen[key] ?? (device.boards.length === 1 || board.summary.down > 0)
                      return (
                        <li key={board.id} data-state={state} data-open={showing || undefined}>
                          <div className="lm-start-cloud-board">
                            <a {...boardLink(cloudBoardLink(device.id, board.id))}>
                              <Dot state={state} />
                              <span className="name">{board.name}</span>
                              <span className="svc">
                                {countText(board.summary.services, board.summary.children, en)}
                              </span>
                              <span className="note">{remoteNote({ summary: board.summary }, lang)}</span>
                            </a>
                            <button
                              type="button"
                              className="lm-start-fold"
                              aria-expanded={showing}
                              aria-label={
                                (showing
                                  ? en
                                    ? 'Hide services of '
                                    : '收起服务：'
                                  : en
                                    ? 'Show services of '
                                    : '展开服务：') + board.name
                              }
                              onClick={() => setBoardOpen((value) => ({ ...value, [key]: !showing }))}
                            >
                              <Glyph name="chevron" size={14} />
                            </button>
                          </div>
                          {showing && <CloudBoardServices device={device} board={board} folds={folds} />}
                        </li>
                      )
                    })
                  )}
                </ul>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}

/*
 * This computer's account in the corner. A click opens a small menu: who this is on the LAN (avatar, nickname,
 * departments), and the few things people do with it.
 */
function Me() {
  const { api, data, lan, scope, refresh, showProfile, cloudStatus } = useApp()
  const { lang, t } = useI18n()
  const en = lang === 'en'
  const gate = useGate()
  const [picking, setPicking] = useState(false)
  const [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const [confirmRestart, setConfirmRestart] = useState(false)
  const { restarting, restartMessage, restartError, triggerRestart } = useSystemRestart(api)
  const box = useRef<HTMLDivElement>(null)
  const departments = useDepartments(open)
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => {
      if (!box.current?.contains(event.target as Node)) setOpen(false)
    }
    const key = (event: KeyboardEvent) => event.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', outside)
    window.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', outside)
      window.removeEventListener('keydown', key)
    }
  }, [open])
  if (!lan) return null
  const editable = !data || allows(scope, 'config')
  const me = departments.value?.me
  const mine = flatten(departmentTree(departments.value)).filter((item) =>
    item.people.some((person) => person.key === me),
  )
  const shuffle = () => {
    setBusy(true)
    setError('')
    api
      .request<Profile>('/profile', { method: 'PUT', body: JSON.stringify({ shuffle: true }) })
      .then((saved) => {
        if (saved?.avatar !== undefined) showProfile(saved)
        refresh()
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '没有换成'))
      .finally(() => setBusy(false))
  }
  return (
    <div className="lm-me" ref={box}>
      {picking && (
        <AvatarPicker
          value={lan.avatar}
          seed={lan.fingerprint}
          save={(avatar) =>
            api
              .request<Profile>('/profile', { method: 'PUT', body: JSON.stringify({ avatar }) })
              .then((saved) => {
                if (saved?.avatar !== undefined) showProfile(saved)
                refresh()
              })
          }
          close={() => setPicking(false)}
        />
      )}
      {confirmRestart && (
        <Dialog title={t('restartConfirmTitle')} close={() => setConfirmRestart(false)}>
          <p style={{ margin: '0 0 18px', fontSize: 13.5, lineHeight: 1.6, color: 'var(--text-2)' }}>
            {t('restartConfirmBody')}
          </p>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
            <button type="button" className="lm-btn" onClick={() => setConfirmRestart(false)}>
              {t('storageCancelBtn')}
            </button>
            <button
              type="button"
              className="lm-btn primary"
              onClick={() => {
                setConfirmRestart(false)
                void triggerRestart()
              }}
            >
              {t('restartConfirmBtn')}
            </button>
          </div>
        </Dialog>
      )}
      <button
        className="lm-start-me"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={'我的账户：' + lan.name}
        title={lan.name}
        onClick={() => setOpen((value) => !value)}
      >
        <Avatar value={lan.avatar} seed={lan.fingerprint} size={28} />
      </button>
      {open && (
        <div className="lm-me-menu" role="menu" aria-label={en ? 'My account' : '我的账户'}>
          <div className="lm-me-card">
            <span
              className="lm-me-face"
              data-busy={busy || undefined}
              style={{ cursor: 'pointer' }}
              onClick={() => {
                setOpen(false)
                gate('config', en ? 'Change avatar' : '换头像', () => setPicking(true))
              }}
            >
              <Avatar value={lan.avatar} seed={lan.fingerprint} size={40} />
            </span>
            <span className="lm-me-text">
              <b>{lan.name}</b>
              {cloudStatus?.loggedIn && cloudStatus.email && (
                <small className="lm-me-email" title={cloudStatus.email}>
                  {cloudStatus.email}
                </small>
              )}
              {departments.value ? (
                mine.length ? (
                  <span className="lm-me-depts" aria-label={en ? 'Departments' : '所在部门'}>
                    {mine.map((item) => (
                      <span key={item.key} className="lm-me-dept" title={item.name}>
                        <Avatar value={item.avatar} seed={item.key} size={16} />
                        <span>{item.name}</span>
                      </span>
                    ))}
                  </span>
                ) : (
                  <small>{en ? 'No department' : '没有加入部门'}</small>
                )
              ) : (
                <small>{en ? 'Loading departments…' : '正在读取部门…'}</small>
              )}
            </span>
          </div>
          {(error || restartError) && <p className="lm-me-error">{error || restartError}</p>}
          {restarting && (
            <p className="lm-me-error" style={{ color: 'var(--text-2)' }}>
              {restartMessage || t('restartWaiting')}
            </p>
          )}
          <div className="lm-me-actions">
            <button
              role="menuitem"
              disabled={busy}
              onClick={() => {
                setOpen(false)
                gate('config', en ? 'Change avatar' : '换头像', () => setPicking(true))
              }}
            >
              <Glyph name="sparkle" size={15} />
              {t('startChooseAvatar')}
            </button>
            <button
              role="menuitem"
              disabled={busy}
              onClick={() => {
                if (!editable) {
                  setOpen(false)
                  askAccess('config', en ? 'Change avatar' : '换头像')
                  return
                }
                shuffle()
              }}
            >
              <Glyph name="refresh" size={15} />
              {busy ? (en ? 'Shuffling…' : '正在换…') : t('startRandomAvatar')}
            </button>
            <a role="menuitem" {...boardLink('#/lan', () => setOpen(false))}>
              <Glyph name="edit" size={15} />
              {t('startEditProfile')}
            </a>
            <a role="menuitem" {...boardLink('#/lan', () => setOpen(false))}>
              <Glyph name="lan" size={15} />
              {t('startLanDept')}
              {lan.pending > 0 && <em>{lan.pending}</em>}
            </a>
            <a role="menuitem" {...boardLink('#/settings/account', () => setOpen(false))}>
              <Glyph name="cloud" size={15} />
              {t('paneAccount')}
            </a>
            <a role="menuitem" {...boardLink('#/settings', () => setOpen(false))}>
              <Glyph name="settings" size={15} />
              {t('settings')}
            </a>
            <button
              role="menuitem"
              disabled={restarting}
              onClick={() => {
                setOpen(false)
                gate('config', t('restartServiceBtn'), () => setConfirmRestart(true))
              }}
            >
              <Glyph name="refresh" size={15} />
              {restarting ? t('restartInProgress') : t('restartServiceBtn')}
            </button>
            {!editable ? (
              <button
                role="menuitem"
                onClick={() => {
                  setOpen(false)
                  askAccess('config', en ? 'Edit profile & settings' : '修改头像和配置')
                }}
              >
                <Glyph name="key" size={15} />
                {t('startGetAccess')}
              </button>
            ) : (
              hasKeptToken() && (
                <button
                  role="menuitem"
                  onClick={() => {
                    setOpen(false)
                    forgetKeptToken()
                  }}
                >
                  <Glyph name="key" size={15} />
                  {t('startForgetToken')}
                </button>
              )
            )}
          </div>
        </div>
      )}
    </div>
  )
}

export function StartPage() {
  const {
    data,
    error,
    monitors,
    monitorsLoaded,
    guide,
    status,
    lan,
    remoteBoards,
    staleSince,
    now,
    boardList,
    cloudStatus,
    cloudDevices,
  } = useApp()
  const currentCloudDevice = cloudDevices.find((device) => device.isCurrent)
  const otherDevices = cloudDevices.filter((device) => !device.isCurrent)
  const { lang, setLang, t } = useI18n()
  const selfName = cloudStatus?.deviceName || lan?.name || lan?.hostname || t('cloudDeviceThis')
  const selfAvatar = lan?.avatar || currentCloudDevice?.avatar
  const selfSeed = cloudStatus?.deviceId || lan?.fingerprint || 'self'
  const selfOS = cloudStatus?.os || lan?.os || currentCloudDevice?.os
  const remoteHealth = useRemoteHealth(remoteBoards)
  const setup = useAgentSetup('all')
  const triple = useTripleClick()
  const folds = useFolds()
  const launcher = inLauncher()
  // The copy from the last visit shows at once; if lgmon cannot be reached, it says that is what it is.
  const offline = !!error && (!data || staleSince > 0)
  const loading = !data || !guide || !monitorsLoaded
  const services = monitors.filter((monitor) => !monitor.parent)
  const children = monitors.length - services.length
  const down = monitors.filter((monitor) => monitor.state === 'down')
  const firing = data?.alerts.length || 0
  const state = mood(loading, offline, monitors, down)
  const parents = new Map(monitors.map((item) => [item.id, item.parent]))
  const families = new Set<string>()
  for (const item of down) {
    for (let parent = item.parent; parent && !families.has(parent); parent = parents.get(parent)) {
      families.add(parent)
    }
  }
  const ordered = troubledFirst(services, families)
  const listed = ordered.slice(0, shown)
  const live = status === 'connected'
  const empty = state === 'empty'
  // The headline and counts describe this computer only; other devices and shared boards have their own chips.
  const localCount = (
    lang === 'en'
      ? [services.length ? services.length + ' services' : '', children ? children + ' sub-services' : '']
      : [services.length ? services.length + ' 个服务' : '', children ? children + ' 个子服务' : '']
  )
    .filter(Boolean)
    .join(' · ')
  const scoped = (list: string[]) =>
    list.length ? [(lang === 'en' ? 'This computer: ' : '本机：') + list[0], ...list.slice(1)] : list
  const facts =
    state === 'offline'
      ? [
          staleSince
            ? '下面是 ' + ago(staleSince, now()) + '的状态，lgmon 可能还在启动'
            : 'lgmon 可能还在启动，稍后会自动重试',
        ]
      : scoped(
          [localCount, firing ? (lang === 'en' ? firing + ' alerts' : firing + ' 条告警') : ''].filter(
            Boolean,
          ),
        )
  const sharedSummary = (() => {
    if (!remoteBoards.length) return null
    let downCount = 0
    let upCount = 0
    let unreachable = 0
    let pending = 0
    for (const item of remoteBoards) {
      const entry = remoteHealth[item.id]
      if (!entry) {
        pending++
        continue
      }
      if (entry.error || !entry.summary) {
        unreachable++
        continue
      }
      downCount += entry.summary.down || 0
      upCount += entry.summary.up || 0
    }
    const dotState: 'up' | 'down' | 'unknown' =
      downCount > 0 ? 'down' : pending === remoteBoards.length || unreachable > 0 ? 'unknown' : 'up'
    const countLabel = lang === 'en' ? `${remoteBoards.length} boards` : `${remoteBoards.length} 个面板`
    let detail = t('startSharedAllUp')
    if (pending === remoteBoards.length) {
      detail = t('startSharedLoading')
    } else if (downCount > 0) {
      detail = lang === 'en' ? `${downCount} down` : `${downCount} 个服务异常`
    } else if (unreachable > 0) {
      detail = lang === 'en' ? `${unreachable} unreachable` : `${unreachable} 个连不上`
    } else if (upCount > 0) {
      detail = lang === 'en' ? `${upCount} up` : `${upCount} 个正常`
    }
    // The people behind the shared boards: a name or two reads at a glance, more stack like agent icons.
    const people = byOwner(remoteBoards).map(([key, boards]) => ({
      key,
      name: boards[0].ownerName,
      avatar: boards[0].ownerAvatar,
      boards: boards.map((item) => item.boardName),
    }))
    const named =
      people.length <= 2 ? people.map((person) => person.name).join(lang === 'en' ? ', ' : '、') : ''
    return {
      dotState,
      title: `${named || t('startSharedSummary')} · ${countLabel}`,
      label: `${t('startSharedSummary')} · ${named ? named + ' · ' : ''}${countLabel}`,
      detail,
      faces: people.slice(0, 3).map((person) => ({ id: person.key, avatar: person.avatar })),
      people,
    }
  })()
  // Up to two other devices get a chip each, named and with their face, so the chip cannot be read as this
  // computer; more than that fold into one chip with stacked faces.
  const cloudChips = (() => {
    if (!otherDevices.length) return []
    const detailOf = (list: CloudDeviceSummary[]) => {
      const online = list.filter((device) => device.online)
      const downCount = online.reduce((sum, device) => sum + device.downCount, 0)
      const dotState: 'up' | 'down' | 'unknown' = downCount > 0 ? 'down' : online.length ? 'up' : 'unknown'
      let detail = t('startCloudAllUp')
      if (downCount > 0) detail = t('startCloudDown', { count: downCount })
      else if (!online.length) detail = t('startCloudOffline')
      else if (online.length < list.length)
        detail = t('startCloudSomeOffline', { count: list.length - online.length })
      return { dotState, detail }
    }
    if (otherDevices.length <= 2)
      return otherDevices.map((device) => {
        const { dotState, detail } = detailOf([device])
        return {
          key: 'cloud-' + device.id,
          target: 'start-cloud',
          dotState,
          title: device.name,
          os: device.os,
          detail:
            device.online && device.totalCount
              ? t('startCloudChipDetail', { count: device.totalCount, state: detail })
              : detail,
          faces: [{ id: device.id, avatar: device.avatar }],
          people: undefined as SharedPerson[] | undefined,
          label: t('startCloudChipLabel', { name: device.name }),
        }
      })
    const { dotState, detail } = detailOf(otherDevices)
    return [
      {
        key: 'cloud',
        target: 'start-cloud',
        dotState,
        title: t('startCloudSummary', { count: otherDevices.length }),
        os: undefined as string | undefined,
        detail,
        faces: otherDevices.slice(0, 3).map((device) => ({ id: device.id, avatar: device.avatar })),
        people: undefined as SharedPerson[] | undefined,
        label: t('startCloudSummary', { count: otherDevices.length }),
      },
    ]
  })()
  const chips = [
    ...cloudChips,
    ...(sharedSummary
      ? [
          {
            ...sharedSummary,
            key: 'shared',
            target: 'start-remote',
            os: undefined as string | undefined,
          },
        ]
      : []),
  ]
  const titleText =
    lang === 'en'
      ? state === 'offline'
        ? t('startOffline')
        : state === 'loading'
          ? t('startLoading')
          : state === 'empty'
            ? t('startEmpty')
            : state === 'down'
              ? `${down.length} down`
              : state === 'waiting'
                ? 'Checking services…'
                : t('startAllUp')
      : headline(state, monitors, down)
  return (
    <div className="lm-start" data-mood={state}>
      <i className="lm-start-glow" aria-hidden="true" />
      <header className="lm-start-head">
        <span className="lm-start-left">
          <span className="lm-start-brand" onClick={triple}>
            <BrandMark size={28} />
            <Wordmark />
            <Signature />
          </span>
          <Credits />
        </span>
        <span className="lm-start-tools">
          <button
            type="button"
            className="lm-start-lang"
            onClick={() => setLang(lang === 'zh' ? 'en' : 'zh')}
            title={t('langSwitchTitle')}
            aria-label={t('langSwitchTitle')}
          >
            {lang === 'zh' ? 'EN' : '中'}
          </button>
          <span className="lm-start-live" data-live={live || undefined}>
            <i aria-hidden="true" />
            {offline ? t('startDisconnected') : live ? t('startRunning') : t('connecting')}
          </span>
          <Me />
        </span>
      </header>

      <main className="lm-start-main">
        {launcher && (
          <Suspense fallback={null}>
            <UpdateCard />
          </Suspense>
        )}
        <section className="lm-start-hero" aria-label={lang === 'en' ? 'Service overview' : '服务概况'}>
          <div className="lm-start-hero-main">
            <h1>{titleText}</h1>
            {facts.length > 0 && <p>{facts.join(' · ')}</p>}
          </div>
        </section>

        {listed.length > 0 && (
          <section className="lm-start-local" aria-label={selfName}>
            <div className="lm-start-local-head">
              <span className="lm-start-cloud-face" data-online={live || undefined}>
                <Avatar value={selfAvatar} seed={selfSeed} size={28} />
                <i aria-hidden="true" />
              </span>
              <span className="lm-start-local-meta">
                <b>{selfName}</b>
                <span className="lm-dev-self-tag">{t('cloudDeviceThis')}</span>
                <OSBadge os={selfOS} />
              </span>
              {localCount && <small className="lm-start-local-count">{localCount}</small>}
            </div>
            <ul
              className="lm-start-list"
              aria-label={lang === 'en' ? 'Services on this computer' : '本机服务'}
            >
              {listed.map((monitor) => (
                <ServiceRow key={monitor.id} monitor={monitor} all={monitors} folds={folds} />
              ))}
              {ordered.length > shown && (
                <li className="more">
                  <a {...boardLink('#/')}>
                    {lang === 'en'
                      ? `${ordered.length - shown} more services — view in dashboard`
                      : `还有 ${ordered.length - shown} 个服务，在看板里查看`}
                  </a>
                </li>
              )}
            </ul>
          </section>
        )}

        {chips.length > 0 && (
          <nav
            className="lm-start-chips"
            data-count={chips.length}
            aria-label={lang === 'en' ? 'Other devices and shared boards' : '其他设备与共享面板'}
          >
            {chips.map((chip) => (
              <button
                key={chip.key}
                type="button"
                className="lm-start-shared-summary"
                data-state={chip.dotState}
                aria-label={chip.label + ' · ' + chip.detail}
                onClick={() => document.getElementById(chip.target)?.scrollIntoView({ behavior: 'smooth' })}
              >
                {chip.people && <SharedPeople people={chip.people} />}
                {chip.faces.length > 0 && (
                  <span
                    className="lm-start-chip-faces"
                    data-stacked={chip.faces.length > 1 || undefined}
                    aria-hidden="true"
                  >
                    {chip.faces.map((device) => (
                      <Avatar
                        key={device.id}
                        value={device.avatar}
                        seed={device.id}
                        size={chip.faces.length === 1 ? 26 : 18}
                      />
                    ))}
                  </span>
                )}
                <span className="lm-start-chip-body">
                  <span className="lm-start-shared-top">
                    {chip.faces.length === 0 && <Dot state={chip.dotState} />}
                    <span className="lm-start-chip-title">{chip.title}</span>
                    {chip.os && <OSBadge os={chip.os} compact />}
                  </span>
                  <span className="lm-start-shared-detail">
                    {chip.faces.length > 0 && <Dot state={chip.dotState} />}
                    {chip.detail}
                  </span>
                </span>
              </button>
            ))}
          </nav>
        )}

        {otherDevices.length > 0 && <CloudDevices devices={otherDevices} />}

        {remoteBoards.length > 0 && <RemoteBoards items={remoteBoards} health={remoteHealth} />}

        <AgentCard
          persona
          quiet={!empty}
          title={empty ? t('startAgentTitleEmpty') : t('startAgentTitleReady')}
          lead={empty ? t('startAgentLeadEmpty') : t('startAgentLeadReady')}
          text={startPrompt(setup, !empty, lang)}
          choices={promptChoices(setup, !empty, boardList[0], lang)}
        />
      </main>

      <footer className="lm-start-foot">
        <a className={'lm-start-board' + (empty ? ' soft' : '')} {...boardLink('#/')}>
          <Glyph name="board" size={17} />
          {t('startOpenBoard')}
        </a>
        <nav aria-label="更多">
          <a {...boardLink('#/alerts')}>
            {t('alerts')}
            {firing ? ' ' + firing : ''}
          </a>
          <a {...boardLink('#/settings')}>{t('settings')}</a>
          {launcher && (
            <Suspense fallback={null}>
              <UpdateLink />
            </Suspense>
          )}
        </nav>
      </footer>
    </div>
  )
}
