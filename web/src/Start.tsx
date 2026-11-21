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
import { promptChoices, startPrompt, useAgentSetup } from './parts'
import { openBoardPage } from './reveal'
import { useApp } from './state'
import { Dot, ownIssue } from './status'
import { forgetKeptToken, hasKeptToken } from './tokens'
import type { BoardStats, LANOutgoing, LANRemoteBoard, Monitor, Profile } from './types'
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
}: {
  monitor: Monitor
  all: Monitor[]
  folds: ReturnType<typeof useFolds>
  level?: number
}) {
  const { lang } = useI18n()
  const en = lang === 'en'
  const [more, setMore] = useState(false)
  const children = level < 3 ? troubledFirst(all.filter((item) => item.parent === monitor.id)) : []
  const below = children.length ? descendants(monitor, all) : []
  const failing = below.filter((item) => item.state === 'down').length
  const open = folds.folds[monitor.id] ?? failing > 0
  const visible = more ? children : children.slice(0, subShown)
  return (
    <li data-state={monitor.state} data-open={children.length ? open : undefined}>
      <div className="lm-start-row">
        <a {...boardLink('#/monitors/' + encodeURIComponent(monitor.id))}>
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
            onClick={() => folds.toggle(monitor.id, !open)}
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
            <ServiceRow key={child.id} monitor={child} all={all} folds={folds} level={level + 1} />
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

/*
 * This computer's account in the corner. A click opens a small menu: who this is on the LAN (avatar, nickname,
 * departments, whether boards are shared), and the few things people do with it.
 */
function Me() {
  const { api, data, lan, scope, refresh, showProfile } = useApp()
  const { lang, t } = useI18n()
  const en = lang === 'en'
  const gate = useGate()
  const [picking, setPicking] = useState(false)
  const [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
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
  const mine = flatten(departmentTree(departments.value))
    .filter((item) => item.people.some((person) => person.key === me))
    .map((item) => item.name)
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
              <Avatar value={lan.avatar} seed={lan.fingerprint} size={46} />
            </span>
            <span className="lm-me-text">
              <b>{lan.name}</b>
              <small>
                {departments.value
                  ? mine.length
                    ? mine.join(en ? ', ' : '、')
                    : en
                      ? 'No department'
                      : '没有加入部门'
                  : en
                    ? 'Loading departments…'
                    : '正在读取部门…'}
              </small>
              <small>
                {lan.visible
                  ? en
                    ? 'Visible on LAN · teammates can request access'
                    : '内网可见 · 同事可以申请看我的面板'
                  : en
                    ? 'Hidden on LAN · private to this computer'
                    : '内网不可见 · 面板只有自己能看'}
              </small>
            </span>
          </div>
          {error && <p className="lm-me-error">{error}</p>}
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
            <a role="menuitem" {...boardLink('#/settings', () => setOpen(false))}>
              <Glyph name="settings" size={15} />
              {t('settings')}
            </a>
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
  const { data, error, monitors, monitorsLoaded, guide, status, remoteBoards, staleSince, now, boardList } =
    useApp()
  const { lang, setLang, t } = useI18n()
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
  const facts =
    state === 'offline'
      ? [
          staleSince
            ? '下面是 ' + ago(staleSince, now()) + '的状态，lgmon 可能还在启动'
            : 'lgmon 可能还在启动，稍后会自动重试',
        ]
      : lang === 'en'
        ? [
            services.length ? services.length + ' services' : '',
            children ? children + ' sub-services' : '',
            firing ? firing + ' alerts' : '',
          ].filter(Boolean)
        : [
            services.length ? services.length + ' 个服务' : '',
            children ? children + ' 个子服务' : '',
            firing ? firing + ' 条告警' : '',
          ].filter(Boolean)
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
    return { dotState, title: `${t('startSharedSummary')} · ${countLabel}`, detail }
  })()
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
          {sharedSummary && (
            <button
              type="button"
              className="lm-start-shared-summary"
              data-state={sharedSummary.dotState}
              onClick={() => document.getElementById('start-remote')?.scrollIntoView({ behavior: 'smooth' })}
            >
              <span className="lm-start-shared-top">
                <Dot state={sharedSummary.dotState} />
                <span>{sharedSummary.title}</span>
              </span>
              <span className="lm-start-shared-detail">{sharedSummary.detail}</span>
            </button>
          )}
        </section>

        {listed.length > 0 && (
          <ul className="lm-start-list" aria-label={lang === 'en' ? 'Services' : '服务'}>
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
        )}

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
