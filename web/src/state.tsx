import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { LiveUpdate } from './html-dashboard'
import { API, APIError, stream, summarySQL } from './api'
import type {
  Alert,
  BoardInfo,
  BoardSummary,
  LANOutgoing,
  LANState,
  Collector,
  Guide,
  Monitor,
  Notifiers,
  NotificationPause,
  RuleError,
  Scope,
  Snapshot,
  StreamStatus,
  Summary,
  Target,
  TargetState,
  Theme,
} from './types'

type AppState = {
  api: API
  scope: Scope
  theme: Theme
  data?: Snapshot
  error: string
  updated: number
  version: number
  dashboardChanges: Record<string, number>
  subscribeLive: (callback: (event: LiveUpdate) => void) => () => void
  refresh: () => void
  status: StreamStatus
  now: () => number
  guide?: Guide
  monitors: Monitor[]
  boards: BoardSummary[]
  /* Boards are groups of services; `boards` above are the older chart pages (custom views). */
  boardList: BoardInfo[]
  boardsLoaded: boolean
  // monitorsLoaded is false until the service list has been read once, so an early failure never reads as "none".
  monitorsLoaded: boolean
  /* Counts board and monitor refreshes; pages that show them reload on it. */
  pulse: number
  lan?: LANState
  remoteBoards: LANOutgoing[]
  /* When the data on screen was saved, while it is the copy from the last visit and not yet read again. */
  staleSince: number
}
const Context = createContext<AppState | undefined>(undefined)

type Extra = {
  guide?: Guide
  monitors: Monitor[]
  boards: BoardSummary[]
  boardList: BoardInfo[]
  boardsLoaded: boolean
  monitorsLoaded: boolean
  pulse: number
  lan?: LANState
  remoteBoards: LANOutgoing[]
}

/*
 * The last data this computer showed, kept for the next time a window opens: people look in now and then, so the
 * page starts from it at once and refreshes behind it. Only on the computer lgmon runs on, never in a browser
 * elsewhere, and never in tests unless asked.
 */
const snapshotKey = 'lgmon-snapshot-v1'
const snapshotAge = 7 * 24 * 3600 * 1000
type Saved = { at: number; data: Snapshot; extra: Omit<Extra, 'pulse'> }

export function keepsSnapshot() {
  if (import.meta.env.MODE === 'test') return false
  return ['127.0.0.1', 'localhost', '[::1]', '::1'].includes(window.location.hostname)
}

function readSnapshot(keep: boolean, now: number): Saved | undefined {
  if (!keep) return undefined
  try {
    const saved = JSON.parse(localStorage.getItem(snapshotKey) || 'null') as Saved | null
    if (saved?.data && saved.extra && now - saved.at < snapshotAge) return saved
  } catch {
    // A broken or blocked store only means starting from nothing, as before.
  }
  return undefined
}

function writeSnapshot(saved: Saved) {
  try {
    localStorage.setItem(snapshotKey, JSON.stringify(saved))
  } catch {
    // Full or blocked storage: the next visit simply starts empty.
  }
}
// Category pages read in the order people check a machine: itself, its services, its sites, then what went wrong.
const categoryOrder = ['system', 'services', 'websites', 'alerts', 'windows']
function orderBoards(items: BoardSummary[]) {
  const rank = (id: string) => {
    const index = categoryOrder.indexOf(id)
    return index < 0 ? categoryOrder.length : index
  }
  return [...items].sort((a, b) => rank(a.id) - rank(b.id))
}

export function useApp() {
  const value = useContext(Context)
  if (!value) throw new Error('缺少页面上下文')
  return value
}

export function State({
  api,
  scope,
  theme,
  children,
  now = Date.now,
  keep = keepsSnapshot(),
}: {
  api: API
  scope: Scope
  theme: Theme
  children: ReactNode
  now?: () => number
  keep?: boolean
}) {
  const [saved] = useState(() => readSnapshot(keep, now()))
  const [data, setData] = useState<Snapshot | undefined>(saved?.data),
    [error, setError] = useState(''),
    [updated, setUpdated] = useState(saved?.at || 0)
  const [staleSince, setStaleSince] = useState(saved?.at || 0)
  const [version, setVersion] = useState(0)
  const [extra, setExtra] = useState<Extra>(() =>
    saved
      ? { ...saved.extra, pulse: 0 }
      : {
          monitors: [],
          boards: [],
          boardList: [],
          boardsLoaded: false,
          monitorsLoaded: false,
          pulse: 0,
          remoteBoards: [],
        },
  )
  const fresh = useRef({ data: false, extra: false }),
    savedAt = useRef(0)
  useEffect(() => {
    // Save only what was read in this visit, and at most every 15 seconds.
    if (!keep || !data || !fresh.current.data || !fresh.current.extra || now() - savedAt.current < 15000)
      return
    savedAt.current = now()
    const { pulse: _pulse, ...rest } = extra
    void _pulse
    writeSnapshot({ at: now(), data, extra: rest })
  }, [keep, data, extra, now])
  const [dashboardChanges, setDashboardChanges] = useState<Record<string, number>>({})
  const [status, setStatus] = useState<StreamStatus>('connecting')
  const pending = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const loaded = useRef(false),
    starting = useRef(0)
  const reload = useRef<(live?: boolean) => void>(() => {})
  const refresh = useCallback(() => reload.current(), [])
  const liveListeners = useRef(new Set<(event: LiveUpdate) => void>())
  const subscribeLive = useCallback((callback: (event: LiveUpdate) => void) => {
    liveListeners.current.add(callback)
    return () => {
      liveListeners.current.delete(callback)
    }
  }, [])
  const publishLive = useCallback((event: LiveUpdate) => {
    for (const listener of liveListeners.current) {
      try {
        listener(event)
      } catch {
        /* A view must not interrupt the shared data stream. */
      }
    }
  }, [])
  useEffect(() => {
    const controller = new AbortController()
    let active = true,
      busy = false,
      queued = false,
      // Live samples arrive every second or two; boards and monitors are heavier and change slower.
      wantExtras = true,
      extrasAt = 0
    const request = <T,>(path: string) => api.request<T>(path, { signal: controller.signal })
    const load = async (live = false) => {
      if (!live) wantExtras = true
      if (busy) {
        queued = true
        return
      }
      busy = true
      do {
        queued = false
        await Promise.all([
          request<Collector[]>('/collectors'),
          request<TargetState[]>('/targets'),
          request<{ targets: Target[] }>('/config/targets'),
          request<Alert[]>('/alerts'),
          request<Notifiers>('/config/notifiers'),
          api.query<Summary>(summarySQL, controller.signal),
          request<NotificationPause>('/notifications/pause'),
          request<RuleError[]>('/rule-errors'),
        ])
          .then(([collectors, targets, definitions, alerts, notifiers, summaries, pause, ruleErrors]) => {
            if (active) {
              setData({
                collectors,
                targets,
                definitions: definitions.targets || [],
                alerts,
                notifiers,
                summaries,
                pause,
                ruleErrors,
              })
              setUpdated(now())
              setVersion((value) => value + 1)
              setError('')
              setStaleSince(0)
              fresh.current.data = true
              loaded.current = true
              starting.current = 0
            }
          })
          .catch((err: unknown) => {
            if (!active) return
            // Right after the monitor starts, a request can time out once or twice; keep showing "loading" and retry
            // quietly for a while instead of greeting people with an error.
            const status = err instanceof APIError ? err.status : 0
            if (!loaded.current && (status === 0 || status === 408 || status >= 500)) {
              starting.current ||= now()
              if (now() - starting.current < 15000) {
                setTimeout(() => active && void load(), 2000)
                return
              }
            }
            setError(err instanceof Error ? err.message : '读取监控数据失败')
          })
        // The journey, monitor cards and sidebar pages are extras: an older monitor without them still shows data.
        if (!wantExtras && now() - extrasAt < 5000) continue
        wantExtras = false
        extrasAt = now()
        const [guide, monitors, boards, boardList, lan, remoteBoards] = await Promise.allSettled([
          request<Guide>('/guide'),
          request<Monitor[]>('/monitors'),
          request<BoardSummary[]>('/dashboards'),
          request<BoardInfo[]>('/boards'),
          request<LANState>('/lan'),
          request<LANOutgoing[]>('/lan/remote'),
        ])
        if (active && monitors.status === 'fulfilled') fresh.current.extra = true
        if (active)
          setExtra((previous) => ({
            guide: guide.status === 'fulfilled' && guide.value?.steps ? guide.value : previous.guide,
            monitors:
              monitors.status === 'fulfilled' && Array.isArray(monitors.value)
                ? monitors.value
                : previous.monitors,
            boards:
              boards.status === 'fulfilled' && Array.isArray(boards.value)
                ? orderBoards(boards.value)
                : previous.boards,
            boardList:
              boardList.status === 'fulfilled' && Array.isArray(boardList.value)
                ? boardList.value
                : previous.boardList,
            boardsLoaded: previous.boardsLoaded || boardList.status === 'fulfilled',
            monitorsLoaded:
              previous.monitorsLoaded || (monitors.status === 'fulfilled' && Array.isArray(monitors.value)),
            lan:
              lan.status === 'fulfilled' && typeof lan.value?.visible === 'boolean'
                ? lan.value
                : previous.lan,
            remoteBoards:
              remoteBoards.status === 'fulfilled' && Array.isArray(remoteBoards.value)
                ? remoteBoards.value
                : previous.remoteBoards,
            pulse: previous.pulse + 1,
          }))
      } while (queued && active)
      busy = false
    }
    reload.current = (live?: boolean) => {
      void load(live)
    }
    void load()
    return () => {
      active = false
      reload.current = () => {}
      controller.abort()
    }
  }, [api, now])

  useEffect(() => {
    const controller = new AbortController()
    const invalidate = () => {
      if (!pending.current)
        pending.current = setTimeout(() => {
          pending.current = undefined
          reload.current(true)
        }, 1000)
    }
    void stream(
      api,
      controller.signal,
      (next) => {
        setStatus(next)
        publishLive({ event: 'connection', data: next })
      },
      invalidate,
      (id) => setDashboardChanges((values) => ({ ...values, [id]: (values[id] || 0) + 1 })),
      (ruleErrors) => setData((value) => (value ? { ...value, ruleErrors } : value)),
      (event, raw) => {
        if (liveListeners.current.size === 0) return
        try {
          publishLive({ event, data: JSON.parse(raw) })
        } catch {
          /* Existing views still refresh if a custom event cannot be decoded. */
        }
      },
    )
    return () => {
      controller.abort()
      clearTimeout(pending.current)
      pending.current = undefined
    }
  }, [api, refresh, publishLive])
  useEffect(() => {
    // An idle collector's configuration acknowledgement does not produce a sample SSE event.
    const timer = setInterval(refresh, 15000)
    return () => clearInterval(timer)
  }, [refresh])
  return (
    <Context.Provider
      value={{
        api,
        scope,
        theme,
        data,
        error,
        updated,
        version,
        status,
        refresh,
        now,
        dashboardChanges,
        subscribeLive,
        guide: extra.guide,
        monitors: extra.monitors,
        boards: extra.boards,
        boardList: extra.boardList,
        boardsLoaded: extra.boardsLoaded,
        monitorsLoaded: extra.monitorsLoaded,
        pulse: extra.pulse,
        lan: extra.lan,
        remoteBoards: extra.remoteBoards,
        staleSince,
      }}
    >
      {children}
    </Context.Provider>
  )
}

export function useResource<T>(
  load: (signal: AbortSignal) => Promise<T>,
  dependencies: unknown[],
  identity = '',
) {
  const [value, setValue] = useState<T>(),
    [error, setError] = useState(''),
    [loading, setLoading] = useState(true)
  const loader = useRef(load),
    reload = useRef<() => void>(() => {})
  loader.current = load
  const previous = useRef(identity)
  useEffect(() => {
    const controller = new AbortController()
    let active = true,
      busy = false,
      queued = false
    if (previous.current !== identity) {
      previous.current = identity
      setValue(undefined)
      setError('')
    }
    const run = async () => {
      if (busy) {
        queued = true
        return
      }
      busy = true
      setLoading(true)
      do {
        queued = false
        await loader
          .current(controller.signal)
          .then((result) => {
            if (active) {
              setValue(result)
              setError('')
            }
          })
          .catch((err: unknown) => {
            if (active) setError(err instanceof Error ? err.message : '读取数据失败')
          })
      } while (queued && active)
      busy = false
      if (active) setLoading(false)
    }
    reload.current = () => {
      void run()
    }
    return () => {
      active = false
      reload.current = () => {}
      controller.abort()
    }
  }, [identity])
  useEffect(() => reload.current(), dependencies)
  return { value, error, loading, reload: () => reload.current() }
}
