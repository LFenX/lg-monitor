import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { LiveUpdate } from './html-dashboard'
import { API, stream, summarySQL } from './api'
import type {
  Alert,
  Collector,
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
}
const Context = createContext<AppState | undefined>(undefined)
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
}: {
  api: API
  scope: Scope
  theme: Theme
  children: ReactNode
  now?: () => number
}) {
  const [data, setData] = useState<Snapshot>(),
    [error, setError] = useState(''),
    [updated, setUpdated] = useState(0)
  const [version, setVersion] = useState(0)
  const [dashboardChanges, setDashboardChanges] = useState<Record<string, number>>({})
  const [status, setStatus] = useState<StreamStatus>('connecting')
  const pending = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const reload = useRef<() => void>(() => {})
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
      queued = false
    const request = <T,>(path: string) => api.request<T>(path, { signal: controller.signal })
    const load = async () => {
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
            }
          })
          .catch((err: unknown) => {
            if (active) setError(err instanceof Error ? err.message : '读取监控数据失败')
          })
      } while (queued && active)
      busy = false
    }
    reload.current = () => {
      void load()
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
          refresh()
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
  return { value, error, loading }
}
