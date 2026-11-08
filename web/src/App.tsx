import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { API } from './api'
import { Overview } from './Overview'
import { HTTPWarning } from './HTTPWarning'
import { State, useApp } from './state'
import { Empty, ErrorNotice, Icon, Modal } from './ui'
import type { Scope, Theme } from './types'

const Alerts = lazy(() => import('./Alerts').then((value) => ({ default: value.Alerts })))
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
const navigation = [
  { href: '#/', label: '总览', icon: 'overview' },
  { href: '#/alerts', label: '告警', icon: 'alert' },
  { href: '#/boards', label: '面板', icon: 'board' },
  { href: '#/notifications', label: '通知', icon: 'bell' },
] as const

export function App() {
  const [initial] = useState(bootstrap),
    [token, setToken] = useState(() => stored('lgmon-token', true) || initial.token)
  const [scope, setScope] = useState<Scope>(() => (stored('lgmon-token', true) ? '' : initial.scope)),
    [theme, setTheme] = useState<Theme>(() => (stored('lgmon-theme') === 'dark' ? 'dark' : 'light'))
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
          <p className="muted">输入此监控器的访问令牌，查看主机与告警。</p>
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
    <a href="#/" className="brand">
      <span className="brand-symbol">
        <Icon name="pulse" size={24} />
      </span>
      <span>
        <strong>lg-monitor</strong>
        <small>服务监控</small>
      </span>
    </a>
  )
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
  const { data, error, scope, status, updated, refresh } = useApp(),
    [hash, setHash] = useState(() => window.location.hash || '#/')
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [hash])
  useEffect(() => {
    const change = () => setHash(window.location.hash || '#/')
    window.addEventListener('hashchange', change)
    return () => window.removeEventListener('hashchange', change)
  }, [])
  const selected = hash.startsWith('#/alerts')
    ? '告警'
    : hash.startsWith('#/boards')
      ? '面板'
      : hash.startsWith('#/notifications')
        ? '通知'
        : '总览'
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <Brand />
        <p className="nav-label">监控空间</p>
        <nav aria-label="主导航">
          {navigation.map((item) => (
            <a key={item.href} href={item.href} aria-current={selected === item.label ? 'page' : undefined}>
              <Icon name={item.icon} />
              <span>{item.label}</span>
              {item.label === '告警' && !!data?.alerts.length && (
                <span className="nav-count">{data.alerts.length}</span>
              )}
            </a>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span className="mini-dot" />
          本地部署 · 数据自主
        </div>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <span className="workspace-title">
            服务监控<span className="topbar-separator">/</span>
            <strong>{selected}</strong>
          </span>
          <div className="topbar-actions">
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
            <button className="access-button" onClick={authentication}>
              <Icon name="key" size={16} />
              <span>{scope === 'admin' ? '管理员' : scope === 'config' ? '可配置' : '只读'}</span>
            </button>
          </div>
        </header>
        <main className="content" id="main-content">
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
          <footer className="page-footer">
            <span>lg-monitor</span>
            <span>
              {updated
                ? '快照更新于 ' + new Date(updated).toLocaleTimeString('zh-CN', { hour12: false })
                : '等待数据'}
            </span>
          </footer>
        </main>
      </div>
      <nav className="mobile-nav" aria-label="手机导航">
        {navigation.map((item) => (
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
    parts = hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent)
  } catch {
    return (
      <Empty title="页面地址无效">
        <a href="#/">返回总览</a>
      </Empty>
    )
  }
  if (!parts.length) return <Overview />
  if (parts[0] === 'alerts' && parts.length === 1) return <Alerts />
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
