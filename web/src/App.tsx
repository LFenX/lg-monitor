import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { API } from './api'
import { CommandPalette } from './CommandPalette'
import { Home } from './Home'
import { Monitors } from './Monitors'
import { ago } from './kit'
import { HTTPWarning } from './HTTPWarning'
import { State, useApp } from './state'
import { Empty, ErrorNotice, Icon, Modal } from './ui'
import type { Scope, Theme } from './types'

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
const Boards = lazy(() => import('./Dashboards').then((value) => ({ default: value.Dashboards })))
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
// An explicit choice wins; otherwise follow the system, falling back to dark.
function initialTheme(): Theme {
  const saved = stored('lgmon-theme')
  if (saved === 'light' || saved === 'dark') return saved
  try {
    return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}
const navigation = [
  { href: '#/', label: '首页', icon: 'home' },
  { href: '#/monitors', label: '监控', icon: 'radar' },
  { href: '#/boards', label: '面板', icon: 'board' },
  { href: '#/explore', label: '探索', icon: 'compass' },
  { href: '#/alerts', label: '告警', icon: 'alert' },
  { href: '#/notifications', label: '通知', icon: 'bell' },
] as const

export function App() {
  const [initial] = useState(bootstrap),
    [token, setToken] = useState(() => stored('lgmon-token', true) || initial.token)
  const [scope, setScope] = useState<Scope>(() => (stored('lgmon-token', true) ? '' : initial.scope)),
    [theme, setTheme] = useState<Theme>(initialTheme)
  const [authOpen, setAuthOpen] = useState(false),
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
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    store('lgmon-theme', theme)
  }, [theme])
  const login = (value: string, role: Scope) => {
    store('lgmon-token', value, true)
    setToken(value)
    setScope(role)
    setAuthOpen(false)
    setAuthError('')
  }
  if (!token)
    return (
      <main className="login-screen">
        <div className="login-card">
          <HTTPWarning />
          <Brand />
          <h1>连接服务监控</h1>
          <p className="muted">在这台电脑上直接打开时不需要令牌；从其他设备访问时，粘贴访问令牌。</p>
          <ErrorNotice message={authError} />
          <Login complete={login} />
        </div>
      </main>
    )
  return (
    <State key={token} api={api} scope={scope} theme={theme}>
      <Shell
        theme={theme}
        toggleTheme={() => setTheme((value) => (value === 'light' ? 'dark' : 'light'))}
        authentication={() => setAuthOpen(true)}
      />
      {authOpen && (
        <Modal title="访问权限" close={() => setAuthOpen(false)}>
          <p className="muted">本机自动会话只有查看权限。输入配置或管理员令牌后，可以确认告警和设置静音。</p>
          <Login complete={login} />
        </Modal>
      )}
    </State>
  )
}

function Login({ complete }: { complete: (token: string, scope: Scope) => void }) {
  const [value, setValue] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault()
        const token = value.trim()
        if (!token) return
        setBusy(true)
        setError('')
        const candidate = new API(token)
        const controller = new AbortController()
        pending.current?.abort()
        pending.current = controller
        void candidate
          .request('/collectors', { signal: controller.signal })
          .then(() => {
            if (!controller.signal.aborted) complete(token, candidate.scope)
          })
          .catch((err: unknown) => {
            if (!controller.signal.aborted)
              setError(err instanceof Error ? err.message : '连接失败，请检查地址和令牌')
          })
          .finally(() => {
            if (!controller.signal.aborted) setBusy(false)
          })
      }}
    >
      <label className="field">
        访问令牌
        <input
          type="password"
          autoComplete="off"
          spellCheck={false}
          required
          disabled={busy}
          maxLength={512}
          value={value}
          onChange={(event) => setValue(event.target.value)}
          placeholder="粘贴访问令牌"
        />
      </label>
      <ErrorNotice message={error} />
      <button className="button primary full-width" disabled={busy}>
        {busy ? '正在连接' : '连接监控器'}
        <Icon name="arrow" size={17} />
      </button>
      <details className="access-help">
        <summary>如何获取访问令牌？</summary>
        <p>在监控器电脑以管理员身份运行：</p>
        <pre>
          <code>lgmon token create --scope config --name browser</code>
        </pre>
        <p>
          仅查看数据时把 config 改为 read；监控器使用自定义目录时加上对应的 --home
          参数。令牌只显示一次，请自行保存。
        </p>
      </details>
    </form>
  )
}

function Brand() {
  return (
    <a href="#/" className="brand lg-brand">
      <span className="lg-brand-mark">
        <Icon name="pulse" size={21} />
      </span>
      <span>
        <strong>lg-monitor</strong>
        <small>这台电脑的监控台</small>
      </span>
    </a>
  )
}

function section(hash: string) {
  if (hash.startsWith('#/alerts')) return '告警'
  if (hash.startsWith('#/boards') || hash.startsWith('#/teams')) return '面板'
  if (hash.startsWith('#/notifications')) return '通知'
  if (hash.startsWith('#/monitors')) return '监控'
  if (hash.startsWith('#/explore')) return '探索'
  if (hash.startsWith('#/connect')) return '接入 Agent'
  return '首页'
}

function Shell({
  theme,
  toggleTheme,
  authentication,
}: {
  theme: Theme
  toggleTheme: () => void
  authentication: () => void
}) {
  const { data, error, scope, status, updated, refresh, boards, guide, now } = useApp(),
    [hash, setHash] = useState(() => window.location.hash || '#/')
  const [assistant, setAssistant] = useState(() => stored('lgmon-assistant') === 'open'),
    [palette, setPalette] = useState(false)
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [hash])
  useEffect(() => {
    const change = () => setHash(window.location.hash || '#/')
    window.addEventListener('hashchange', change)
    return () => window.removeEventListener('hashchange', change)
  }, [])
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPalette((value) => !value)
      }
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [])
  const toggleAssistant = () =>
    setAssistant((value) => {
      store('lgmon-assistant', value ? '' : 'open')
      return !value
    })
  const selected = section(hash)
  const board = /^#\/boards\/[^/]+$/.test(hash)
  const agentSeen = !!guide?.state.agentSeenAt
  return (
    <div className={'app-shell lg-shell' + (assistant ? ' with-assistant' : '')}>
      <aside className="lg-rail">
        <Brand />
        <nav className="lg-nav" aria-label="主导航">
          {navigation.map((item) => (
            <a key={item.href} href={item.href} aria-current={selected === item.label ? 'page' : undefined}>
              <Icon name={item.icon} size={19} />
              <span>{item.label}</span>
              {item.label === '告警' && !!data?.alerts.length && (
                <span className="nav-count">{data.alerts.length}</span>
              )}
            </a>
          ))}
        </nav>
        <p className="lg-rail-label">
          我的面板 <a href="#/boards">全部</a>
        </p>
        {boards.length ? (
          <nav className="lg-nav lg-boardlinks" aria-label="我的面板">
            {boards.slice(0, 9).map((page) => (
              <a
                key={page.id}
                href={'#/boards/' + encodeURIComponent(page.id)}
                aria-current={hash === '#/boards/' + encodeURIComponent(page.id) ? 'page' : undefined}
                data-board-theme={page.theme || 'aurora'}
                title={page.title}
              >
                <i className="swatch" style={{ ['--swatch' as string]: 'var(--board-accent)' }} />
                <span className="name">{page.title}</span>
              </a>
            ))}
          </nav>
        ) : (
          <p className="lg-rail-empty">做好的面板会一直留在这里，随时点开。</p>
        )}
        <div className="lg-rail-spacer" />
        <a className="lg-connect" href="#/connect">
          <i className={'status-dot' + (agentSeen ? ' on' : '')} />
          <span>
            <strong>{agentSeen ? 'Agent 已接入' : '接入 Agent'}</strong>
            <small>
              {agentSeen ? '最近调用 ' + ago(guide!.state.agentSeenAt, now()) : '让 AI 替你加监控、做面板'}
            </small>
          </span>
        </a>
      </aside>
      <div className="workspace lg-main">
        <header className="lg-bar">
          <span className="workspace-title">
            lg-monitor<span className="topbar-separator"> / </span>
            <strong>{selected}</strong>
          </span>
          <button className="lg-search" onClick={() => setPalette(true)} aria-label="搜索">
            <Icon name="search" size={16} />
            <span>搜索监控、面板、服务、端口…</span>
            <kbd>Ctrl K</kbd>
          </button>
          <div className="lg-bar-actions">
            <span
              className={'connection connection-' + status}
              title={
                updated
                  ? '快照更新于 ' + new Date(updated).toLocaleTimeString('zh-CN', { hour12: false })
                  : '等待首次快照'
              }
            >
              <span className="dot" />
              {status === 'connected' ? '实时连接' : status === 'connecting' ? '正在连接' : '正在重连'}
            </span>
            <button className="icon-button" title="刷新数据" aria-label="刷新数据" onClick={refresh}>
              <Icon name="refresh" />
            </button>
            <button
              className="icon-button"
              aria-label={theme === 'light' ? '切换到深色' : '切换到浅色'}
              title={theme === 'light' ? '切换到深色' : '切换到浅色'}
              onClick={toggleTheme}
            >
              <Icon name={theme === 'light' ? 'moon' : 'sun'} />
            </button>
            <button
              className="access-button"
              onClick={authentication}
              title="访问权限：只读可以查看；可配置可以添加监控、做面板和确认告警"
            >
              <Icon name="key" size={15} />
              <span>{scope === 'admin' ? '管理员' : scope === 'config' ? '可配置' : '只读'}</span>
            </button>
            <button className="lg-ai-button" aria-pressed={assistant} onClick={toggleAssistant}>
              <Icon name="sparkles" size={16} />问 AI
            </button>
          </div>
        </header>
        <main className={'content lg-content' + (board ? ' wide' : '')} id="main-content">
          <HTTPWarning />
          <ErrorNotice message={error} />
          <NotificationPauseBanner />
          <CollectorConfigWarnings />
          <RuleErrorWarnings />
          {error && data && <p className="stale-note">以下保留上次成功读取的数据，请检查连接后刷新。</p>}
          {data ? (
            <Page hash={hash} />
          ) : (
            <div className="loading-state" role="status">
              <Icon name="refresh" size={28} />
              <h1>{error ? '暂时无法读取监控数据' : '正在读取监控数据'}</h1>
              <p className="muted">
                {error ? '检查监控器连接，或点击右上角刷新重试。' : '正在汇总主机、目标和告警状态。'}
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
      <nav className="lg-mobile-nav" aria-label="手机导航">
        {navigation.slice(0, 5).map((item) => (
          <a href={item.href} key={item.href} aria-current={selected === item.label ? 'page' : undefined}>
            <Icon name={item.icon} />
            <span>{item.label}</span>
          </a>
        ))}
      </nav>
    </div>
  )
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
        disabled={busy || (scope !== 'config' && scope !== 'admin')}
        title={scope === 'read' ? '恢复通知需要配置权限' : undefined}
        onClick={() => {
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

function Page({ hash }: { hash: string }) {
  return (
    <Suspense
      fallback={
        <div className="loading-state" role="status">
          正在加载页面
        </div>
      }
    >
      <Route hash={hash} />
    </Suspense>
  )
}
function Route({ hash }: { hash: string }) {
  let parts: string[]
  try {
    parts = hash.replace(/^#\/?/, '').split('?')[0].split('/').filter(Boolean).map(decodeURIComponent)
  } catch {
    return (
      <Empty title="页面地址无效">
        <a href="#/">返回总览</a>
      </Empty>
    )
  }
  if (!parts.length) return <Home />
  if (parts[0] === 'monitors' && parts.length === 1) return <Monitors />
  if (parts[0] === 'explore' && parts.length === 1) return <Explore />
  if (parts[0] === 'connect' && parts.length === 1) return <Connect />
  if (parts[0] === 'teams' && parts.length === 1) return <Teams />
  if (parts[0] === 'teams' && parts.length === 2 && parts[1] === 'join') return <TeamJoin />
  if (parts[0] === 'teams' && parts.length === 3)
    return <SharedTeamView key={parts[1] + ':' + parts[2]} joined={parts[1]} share={parts[2]} />
  if (parts[0] === 'alerts' && parts.length === 1) return <Alerts />
  if (parts[0] === 'alerts' && parts.length === 3 && parts[2] === 'diagnostics')
    return <Diagnostic id={parts[1]} />
  if (parts[0] === 'boards' && parts.length === 1) return <Boards />
  if (parts[0] === 'boards' && parts.length === 2) return <Boards id={parts[1]} />
  if (parts[0] === 'notifications' && parts.length === 1) return <Notifications />
  if (parts[0] === 'notifications' && parts[1] === 'setup' && parts.length === 2) return <Setup />
  if (parts[0] === 'notifications' && parts[1] === 'rules' && parts.length === 2) return <RuleNotifications />
  if (parts[0] === 'notifications' && parts[1] === 'templates' && parts.length === 3)
    return <TemplateEditor id={parts[2]} key={parts[2]} />
  if (parts[0] === 'hosts' && parts.length === 2) return <HostDetail key={parts[1]} collector={parts[1]} />
  if (parts[0] === 'hosts' && parts[2] === 'targets' && parts.length === 4)
    return <TargetDetail key={parts[1] + ':' + parts[3]} collector={parts[1]} target={parts[3]} />
  return (
    <Empty title="页面不存在">
      <a href="#/">返回总览</a>
    </Empty>
  )
}
