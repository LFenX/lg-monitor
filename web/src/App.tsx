import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { API } from './api'
import { AccessDialog, TokenForm, askAccess, onAccessRequest } from './access'
import type { AccessRequest } from './access'
import { AgentStack, Signal, agentName, groupAgents, useAgents } from './agents'
import { BoardPage } from './Board'
import { CommandPalette } from './CommandPalette'
import { HostWarnings } from './Computers'
import { BohemiaMark } from './bohemia'
import { BrandMark, Wordmark } from './brand'
import { Glyph } from './glyphs'
import type { GlyphName } from './glyphs'
import { HTTPWarning } from './HTTPWarning'
import { LanPage, RemoteBoardPage } from './Lan'
import { ServicePage } from './Service'
import { Settings } from './Settings'
import { StartPage } from './Start'
import { State, useApp } from './state'
import { Dot } from './status'
import { ErrorNotice } from './ui'
import { NewBoard, Welcome } from './Welcome'
import type { Scope, Theme, ThemeChoice } from './types'

const Assistant = lazy(() => import('./Assistant').then((value) => ({ default: value.Assistant })))
const Explore = lazy(() => import('./Explore').then((value) => ({ default: value.Explore })))
const Connect = lazy(() => import('./Connect').then((value) => ({ default: value.Connect })))
const Alerts = lazy(() => import('./Alerts').then((value) => ({ default: value.Alerts })))
const Diagnostic = lazy(() => import('./Diagnostic').then((value) => ({ default: value.Diagnostic })))
const Teams = lazy(() => import('./Teams').then((value) => ({ default: value.Teams })))
const TeamJoin = lazy(() => import('./Teams').then((value) => ({ default: value.TeamJoin })))
const SharedTeamView = lazy(() =>
  import('./SharedTeams').then((value) => ({ default: value.SharedTeamView })),
)
const HostDetail = lazy(() => import('./Details').then((value) => ({ default: value.HostDetail })))
const TargetDetail = lazy(() => import('./Details').then((value) => ({ default: value.TargetDetail })))
const Views = lazy(() => import('./Dashboards').then((value) => ({ default: value.Dashboards })))
const Notifications = lazy(() =>
  import('./Notifications').then((value) => ({ default: value.Notifications })),
)
const Setup = lazy(() => import('./Notifications').then((value) => ({ default: value.Setup })))
const TemplateEditor = lazy(() =>
  import('./TemplateEditor').then((value) => ({ default: value.TemplateEditor })),
)
const RuleNotifications = lazy(() =>
  import('./TemplateLibrary').then((value) => ({ default: value.RuleNotifications })),
)

type Bootstrap = { token: string; scope: Scope }
function bootstrap(): Bootstrap {
  try {
    const value = JSON.parse(document.getElementById('lgmon-session')?.textContent || '{}')
    return {
      token: typeof value.token === 'string' ? value.token : '',
      scope: value.scope === 'read' ? 'read' : '',
    }
  } catch {
    return { token: '', scope: '' }
  }
}
function stored(key: string, session = false) {
  try {
    return (session ? sessionStorage : localStorage).getItem(key) || ''
  } catch {
    return ''
  }
}
function store(key: string, value: string, session = false) {
  try {
    const storage = session ? sessionStorage : localStorage
    if (value) storage.setItem(key, value)
    else storage.removeItem(key)
  } catch {
    /* A private browser may disable persistent storage. */
  }
}
function systemTheme(): Theme {
  try {
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}
// An explicit choice wins; otherwise follow the system.
function initialTheme(): Theme {
  const saved = stored('lgmon-theme')
  if (saved === 'light' || saved === 'dark') return saved
  return systemTheme()
}

export function App() {
  const [initial] = useState(bootstrap),
    [token, setToken] = useState(() => stored('lgmon-token', true) || initial.token)
  const [scope, setScope] = useState<Scope>(() => (stored('lgmon-token', true) ? '' : initial.scope)),
    [theme, setThemeState] = useState<Theme>(initialTheme)
  const [request, setRequest] = useState<AccessRequest | null>(null),
    [authError, setAuthError] = useState('')
  const api = useMemo(() => new API(token), [token])
  useEffect(() => {
    api.onScope = setScope
    api.onUnauthorized = () => {
      store('lgmon-token', '', true)
      setToken('')
      setScope('')
      setAuthError('访问令牌已失效，请重新连接监控器。')
    }
    return () => {
      api.onScope = undefined
      api.onUnauthorized = undefined
    }
  }, [api])
  useEffect(() => onAccessRequest(setRequest), [])
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute('content', theme === 'dark' ? '#1f1e1d' : '#faf9f5')
  }, [theme])
  // Following the system keeps following it when Windows switches between light and dark.
  useEffect(() => {
    const query = window.matchMedia?.('(prefers-color-scheme: dark)')
    if (!query?.addEventListener) return
    const follow = () => {
      if (!stored('lgmon-theme')) setThemeState(systemTheme())
    }
    query.addEventListener('change', follow)
    return () => query.removeEventListener('change', follow)
  }, [])
  const setTheme = (next: ThemeChoice) => {
    store('lgmon-theme', next === 'system' ? '' : next)
    setThemeState(next === 'system' ? systemTheme() : next)
  }
  const login = (value: string, role: Scope) => {
    store('lgmon-token', value, true)
    setToken(value)
    setScope(role)
    setRequest(null)
    setAuthError('')
  }
  if (!token)
    return (
      <main className="login-screen">
        <div className="login-card">
          <HTTPWarning />
          <Brand />
          <h1>连接服务监控</h1>
          <p className="muted">
            你是从别的设备打开的 lgmon 网页，需要一个令牌证明是你。在这台监控电脑上直接打开时不需要。
          </p>
          <ErrorNotice message={authError} />
          <TokenForm complete={login} submit="连接监控器" />
          <details className="access-help">
            <summary>令牌从哪里来？</summary>
            <p>在运行 lgmon 的那台电脑上打开 PowerShell，运行：</p>
            <pre>
              <code>lgmon token create --scope read --name 手机</code>
            </pre>
            <p>
              只想看状态用 read；要在网页里添加服务、确认告警，把 read 换成 config。lgmon 用了自定义目录时加上
              --home 参数。令牌只显示一次。
            </p>
          </details>
        </div>
      </main>
    )
  return (
    <State key={token} api={api} scope={scope} theme={theme}>
      <Shell theme={theme} setTheme={setTheme} />
      {request && (
        <AccessDialog
          request={request}
          local={!!initial.token && token === initial.token}
          close={() => setRequest(null)}
          login={login}
        />
      )}
    </State>
  )
}

function Brand() {
  return (
    <a href="#/" className="lm-brand" aria-label="lgmon 首页">
      <BrandMark size={28} />
      <Wordmark />
    </a>
  )
}

function parse(hash: string): string[] | null {
  try {
    return hash.replace(/^#\/?/, '').split('?')[0].split('/').filter(Boolean).map(decodeURIComponent)
  } catch {
    return null
  }
}

function Shell({ theme, setTheme }: { theme: Theme; setTheme: (theme: ThemeChoice) => void }) {
  const { data, error, status, boardList, refresh, lan, remoteBoards } = useApp(),
    [hash, setHash] = useState(() => window.location.hash || '#/')
  const [assistant, setAssistant] = useState(() => stored('lgmon-assistant') === 'open'),
    [palette, setPalette] = useState(false),
    [drawer, setDrawer] = useState(false),
    [creating, setCreating] = useState(false)
  useEffect(() => {
    window.scrollTo(0, 0)
    setDrawer(false)
  }, [hash])
  useEffect(() => {
    const change = () => setHash(window.location.hash || '#/')
    window.addEventListener('hashchange', change)
    return () => window.removeEventListener('hashchange', change)
  }, [])
  const toggleAssistant = () =>
    setAssistant((value) => {
      store('lgmon-assistant', value ? '' : 'open')
      return !value
    })
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPalette((value) => !value)
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'j') {
        event.preventDefault()
        toggleAssistant()
      }
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  })
  const parts = parse(hash) || []
  // The small window lgmon opens stands on its own, without the board's frame.
  if (parts[0] === 'start') return <StartPage />
  const firstBoard = boardList[0]?.id
  const currentBoard = parts[0] === 'boards' ? parts[1] : !parts.length ? firstBoard : undefined
  const firing = data?.alerts.length || 0
  const pages: { href: string; glyph: GlyphName; label: string; count?: number; match: boolean }[] = [
    { href: '#/alerts', glyph: 'bell', label: '告警', count: firing, match: parts[0] === 'alerts' },
    {
      href: '#/lan',
      glyph: 'lan',
      label: '内网',
      count: lan?.pending,
      match: parts[0] === 'lan' && parts[1] !== 'remote',
    },
    {
      href: '#/settings',
      glyph: 'settings',
      label: '设置',
      match: ['settings', 'notifications', 'connect', 'views', 'hosts', 'explore', 'teams'].includes(
        parts[0] || '',
      ),
    },
  ]
  const current = boardList.find((board) => board.id === currentBoard)
  const title = current?.name || pages.find((page) => page.match)?.label || 'lgmon'
  return (
    <div className="lm-app" data-drawer={drawer ? 'open' : undefined}>
      <aside className="lm-side" aria-label="导航">
        <Brand />
        <p className="lm-nav-label">面板</p>
        <nav className="lm-nav" aria-label="面板">
          {boardList.map((board) => (
            <a
              key={board.id}
              href={'#/boards/' + encodeURIComponent(board.id)}
              aria-current={board.id === currentBoard ? 'page' : undefined}
              title={board.description || board.name}
            >
              <Dot state={board.summary.state} />
              <span className="name">{board.name}</span>
              {board.summary.down > 0 ? (
                <span className="count bad">{board.summary.down}</span>
              ) : (
                <span className="count">{board.summary.services + board.summary.children || ''}</span>
              )}
            </a>
          ))}
          <button
            className="add"
            onClick={() => {
              setDrawer(false)
              setCreating(true)
            }}
          >
            <Glyph name="plus" size={16} />
            <span>新建面板</span>
          </button>
        </nav>
        {remoteBoards.length > 0 && (
          <>
            <p className="lm-nav-label">别人的面板</p>
            <nav className="lm-nav" aria-label="别人的面板">
              {remoteBoards.map((item) => (
                <a
                  key={item.id}
                  href={'#/lan/remote/' + encodeURIComponent(item.id)}
                  aria-current={parts[0] === 'lan' && parts[2] === item.id ? 'page' : undefined}
                  title={item.ownerName + ' 的「' + item.boardName + '」'}
                >
                  <Glyph name="lan" size={16} />
                  <span className="name">{item.boardName}</span>
                  <span className="count">{item.ownerName}</span>
                </a>
              ))}
            </nav>
          </>
        )}
        <p className="lm-nav-label" aria-hidden="true" />
        <nav className="lm-nav" aria-label="主导航">
          {pages.map((page) => (
            <a key={page.href} href={page.href} aria-current={page.match ? 'page' : undefined}>
              <Glyph name={page.glyph} />
              <span className="name">{page.label}</span>
              {page.count ? (
                <span className={'count ' + (page.label === '告警' ? 'bad' : 'accent')}>{page.count}</span>
              ) : null}
            </a>
          ))}
        </nav>
        <SideAgents />
        <div className="lm-side-foot">
          <button
            className="live"
            onClick={refresh}
            aria-label="刷新数据"
            title={(status === 'connected' ? '实时连接' : '正在重新连接') + '，点击刷新'}
          >
            <Dot state={status === 'connected' ? 'up' : 'unknown'} />
            {status === 'connected' ? '实时' : status === 'connecting' ? '连接中' : '重连中'}
          </button>
          <button
            className="lm-btn ghost small icon"
            onClick={() => {
              setDrawer(false)
              setPalette(true)
            }}
            title="搜索（Ctrl K）"
            aria-label="搜索"
          >
            <Glyph name="search" size={16} />
          </button>
          <button
            className="lm-btn ghost small icon"
            onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}
            title={theme === 'light' ? '切换到深色' : '切换到浅色'}
            aria-label={theme === 'light' ? '切换到深色' : '切换到浅色'}
          >
            <Glyph name={theme === 'light' ? 'moon' : 'sun'} size={16} />
          </button>
          <button
            className="lm-btn ghost small icon"
            aria-pressed={assistant}
            onClick={() => {
              setDrawer(false)
              toggleAssistant()
            }}
            title="波西米亚：问服务状况或 lgmon 怎么用（Ctrl J）"
            aria-label="波西米亚"
          >
            <BohemiaMark size={18} />
          </button>
        </div>
      </aside>
      <div className="lm-scrim" onClick={() => setDrawer(false)} />
      <div className="lm-main">
        <header className="lm-top">
          <button className="lm-btn ghost icon" aria-label="打开导航" onClick={() => setDrawer(true)}>
            <Glyph name="menu" />
          </button>
          <BrandMark size={22} />
          <strong>{title}</strong>
        </header>
        <main id="main-content">
          <div className="lm-banners">
            <HTTPWarning />
            <ErrorNotice message={error} />
            <NotificationPauseBanner />
            <CollectorConfigWarnings />
            <HostWarnings />
            <RuleErrorWarnings />
          </div>
          {data ? (
            <Suspense fallback={<div className="lm-page lm-muted">正在加载页面…</div>}>
              <Route
                parts={parse(hash)}
                setTheme={setTheme}
                openAssistant={() => !assistant && toggleAssistant()}
              />
            </Suspense>
          ) : (
            <div className="lm-page">
              <p className="lm-muted">
                {error ? '暂时无法读取监控数据，请检查监控器是否在运行。' : '正在读取监控数据…'}
              </p>
            </div>
          )}
        </main>
      </div>
      {assistant && (
        <Suspense fallback={null}>
          <Assistant close={toggleAssistant} />
        </Suspense>
      )}
      {palette && (
        <CommandPalette
          close={() => setPalette(false)}
          askAssistant={() => !assistant && toggleAssistant()}
        />
      )}
      {creating && <NewBoard close={() => setCreating(false)} />}
    </div>
  )
}

/* Pages from before the redesign keep working inside the new frame. */
function Legacy({ children }: { children: ReactNode }) {
  return <div className="lm-legacy content lg-content">{children}</div>
}

function Missing({ title }: { title: string }) {
  return (
    <div className="lm-page">
      <div className="lm-note">
        {title}
        <a className="lm-btn small" href="#/">
          回到面板
        </a>
      </div>
    </div>
  )
}

function Route({
  parts,
  setTheme,
  openAssistant,
}: {
  parts: string[] | null
  setTheme: (theme: ThemeChoice) => void
  openAssistant: () => void
}) {
  const { boardList, boardsLoaded, boards: views } = useApp()
  if (!parts) return <Missing title="页面地址无效" />
  const [section, first, second, third] = parts
  const home = () => {
    if (boardList.length) return <BoardPage key={boardList[0].id} id={boardList[0].id} />
    if (!boardsLoaded) return <div className="lm-page lm-muted">正在读取面板…</div>
    return <Welcome />
  }
  switch (section) {
    case undefined:
      return home()
    case 'boards':
      if (!first) return home()
      // Links from before the redesign point custom chart pages at #/boards/<id>.
      if (
        boardsLoaded &&
        !boardList.some((board) => board.id === first) &&
        views.some((view) => view.id === first)
      )
        return (
          <Legacy>
            <Views id={first} />
          </Legacy>
        )
      return <BoardPage key={first} id={first} />
    case 'monitors':
      return first ? <ServicePage key={first} id={first} /> : home()
    case 'lan':
      if (!first) return <LanPage />
      if (first === 'remote' && second) return <RemoteBoardPage key={second} id={second} />
      break
    case 'settings':
      return <Settings section={first} setTheme={setTheme} openAssistant={openAssistant} />
    case 'views':
      return <Legacy>{first ? <Views key={first} id={first} /> : <Views />}</Legacy>
    case 'alerts':
      if (!first)
        return (
          <Legacy>
            <Alerts />
          </Legacy>
        )
      if (second === 'diagnostics')
        return (
          <Legacy>
            <Diagnostic id={first} />
          </Legacy>
        )
      break
    case 'explore':
      return (
        <Legacy>
          <Explore />
        </Legacy>
      )
    case 'connect':
      return (
        <Legacy>
          <Connect />
        </Legacy>
      )
    case 'teams':
      if (!first)
        return (
          <Legacy>
            <Teams />
          </Legacy>
        )
      if (first === 'join')
        return (
          <Legacy>
            <TeamJoin />
          </Legacy>
        )
      if (second)
        return (
          <Legacy>
            <SharedTeamView key={first + ':' + second} joined={first} share={second} />
          </Legacy>
        )
      break
    case 'notifications':
      if (!first)
        return (
          <Legacy>
            <Notifications />
          </Legacy>
        )
      if (first === 'setup')
        return (
          <Legacy>
            <Setup />
          </Legacy>
        )
      if (first === 'rules')
        return (
          <Legacy>
            <RuleNotifications />
          </Legacy>
        )
      if (first === 'templates' && second)
        return (
          <Legacy>
            <TemplateEditor id={second} key={second} />
          </Legacy>
        )
      break
    case 'hosts':
      if (first && !second)
        return (
          <Legacy>
            <HostDetail key={first} collector={first} />
          </Legacy>
        )
      if (first && second === 'targets' && third)
        return (
          <Legacy>
            <TargetDetail key={first + ':' + third} collector={first} target={third} />
          </Legacy>
        )
      break
  }
  return <Missing title="页面不存在" />
}

function CollectorConfigWarnings() {
  const { data } = useApp()
  return (
    <>
      {data?.collectors
        .filter((host) => host.config?.state === 'rejected' || host.config?.state === 'outdated')
        .map((host) => (
          <div className="notice notice-warning" role="status" key={host.id}>
            采集器 {host.hostname || host.id} 配置未更新：{host.config?.message}
          </div>
        ))}
    </>
  )
}

function RuleErrorWarnings() {
  const { data } = useApp()
  const active = data?.ruleErrors.filter((row) => row.active) || []
  if (!active.length) return null
  return (
    <div className="notice notice-warning rule-errors" role="status" aria-label="规则错误">
      <ul>
        {active.slice(0, 3).map((row) => (
          <li key={row.ruleId}>
            规则 {row.ruleName || row.ruleId}：{row.error}
          </li>
        ))}
      </ul>
      {active.length > 3 && <p>等 {active.length} 条规则错误</p>}
    </div>
  )
}

function NotificationPauseBanner() {
  const { api, data, scope, refresh } = useApp()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  const pause = data?.pause
  if (!pause?.paused || pause.until === null) return null
  return (
    <div className="notice notice-warning" role="status" data-control="notification-pause">
      <span>
        通知已暂停到{' '}
        {new Date(pause.until).toLocaleTimeString('zh-CN', {
          hour: '2-digit',
          minute: '2-digit',
          hour12: false,
        })}
      </span>{' '}
      <button
        className="button small-button"
        disabled={busy}
        onClick={() => {
          if (scope !== 'config' && scope !== 'admin') return askAccess('config', '恢复通知')
          const controller = new AbortController()
          pending.current = controller
          setBusy(true)
          setError('')
          void api
            .request('/notifications/pause', { method: 'DELETE', signal: controller.signal })
            .then(() => {
              if (!controller.signal.aborted) refresh()
            })
            .catch((err: unknown) => {
              if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '恢复通知失败')
            })
            .finally(() => {
              if (!controller.signal.aborted) setBusy(false)
            })
        }}
      >
        {busy ? '正在恢复…' : '恢复通知'}
      </button>
      <ErrorNotice message={error} />
    </div>
  )
}

/* The agents connected right now, at the foot of the side bar: their logos and a live signal. */
function SideAgents() {
  const connected = (useAgents() || []).filter((agent) => agent.connected)
  if (!connected.length) return null
  const groups = groupAgents(connected)
  const label =
    groups.length === 1 && connected.length === 1
      ? agentName(connected[0]) + ' 已连接'
      : connected.length + ' 个 Agent 已连接'
  return (
    <a
      className="lm-side-agents"
      href="#/settings/agent"
      title={groups.map((group) => group.name).join('、') + ' 已连接'}
    >
      <AgentStack list={connected} max={3} />
      <span className="name">{label}</span>
      <Signal />
    </a>
  )
}
