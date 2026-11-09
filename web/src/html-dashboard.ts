import { API, APIError } from './api'

export type LiveUpdate = { event: string; data: unknown }
export type HTMLContext = {
  title: string
  theme: string
  refresh: string
  params: Record<string, unknown>
}
export const htmlTopics = new Set([
  'target',
  'alert',
  'sample',
  'dashboard',
  'pause',
  'ruleErrors',
  '*',
  'context',
  'connection',
])
const wireTopics = new Set(['target', 'alert', 'sample', 'dashboard', 'pause', 'ruleErrors', 'cardAction'])
const encoder = new TextEncoder()

function queryParams(value: unknown) {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('params 必须是查询参数对象')
  const params = value as Record<string, unknown>
  for (const [key, item] of Object.entries(params)) {
    if (key === 'range' && typeof item === 'string') continue
    if (
      (key === 'from' || key === 'to') &&
      typeof item === 'number' &&
      Number.isSafeInteger(item) &&
      item >= 0
    )
      continue
    if (
      key === 'collector' &&
      (item === null || (typeof item === 'string' && item.length > 0 && encoder.encode(item).length <= 255))
    )
      continue
    throw new Error('params 只接受 range/from/to/collector，字段类型必须与查询接口一致')
  }
  return params
}

export function mergeHTMLParams(base: Record<string, unknown>, input: unknown) {
  const params = input === undefined ? {} : queryParams(input)
  const merged = { ...base }
  if (['range', 'from', 'to'].some((name) => Object.prototype.hasOwnProperty.call(params, name))) {
    delete merged.range
    delete merged.from
    delete merged.to
  }
  return { ...merged, ...params }
}

export function createHTMLBridge(options: {
  api: API
  context: () => HTMLContext
  status: () => string
  send: (value: unknown) => void
  subscribe: (callback: (event: LiveUpdate) => void) => () => void
  error: (message: string) => void
}) {
  const pending = new Map<number, AbortController>()
  const topics = new Set<string>()
  const unconfirmedEvents = new Set<number>()
  let eventSequence = 0
  let closed = false
  let unsubscribe: (() => void) | undefined
  const send = (value: unknown) => {
    if (!closed) options.send(value)
  }
  const failure = (id: number, code: string, message: string, status = 400) =>
    send({ type: 'result', id, ok: false, error: { code, message, status } })
  const close = () => {
    closed = true
    for (const controller of pending.values()) controller.abort()
    pending.clear()
    topics.clear()
    unconfirmedEvents.clear()
    unsubscribe?.()
    unsubscribe = undefined
  }
  const event = (name: string, data: unknown) => {
    if (unconfirmedEvents.size >= 64) {
      topics.clear()
      unsubscribe?.()
      unsubscribe = undefined
      options.error('HTML 面板处理实时事件过慢，已停止订阅；请刷新面板重新连接')
      return
    }
    const id = ++eventSequence
    unconfirmedEvents.add(id)
    send({ type: 'event', id, event: name, data })
  }
  const syncSubscription = () => {
    const active = [...topics].some((event) => event !== 'context')
    if (!active) {
      unsubscribe?.()
      unsubscribe = undefined
      return
    }
    if (!unsubscribe)
      unsubscribe = options.subscribe((update) => {
        if (
          (wireTopics.has(update.event) || update.event === 'connection') &&
          (topics.has(update.event) || topics.has('*'))
        ) {
          event(update.event, update.data)
        }
      })
  }
  return {
    close,
    contextChanged() {
      send({ type: 'context', value: options.context() })
    },
    receive(raw: unknown) {
      if (closed || !raw || typeof raw !== 'object' || Array.isArray(raw)) return
      const message = raw as Record<string, unknown>
      const id = message.id
      const validID = typeof id === 'number' && Number.isSafeInteger(id) && id > 0
      if (message.type === 'close') {
        close()
        return
      }
      if (message.type === 'event_ack') {
        if (validID) unconfirmedEvents.delete(id)
        return
      }
      if (message.type === 'error') {
        if (typeof message.message === 'string') options.error(message.message.slice(0, 500))
        return
      }
      if (message.type === 'cancel') {
        if (validID) {
          pending.get(id)?.abort()
          pending.delete(id)
        }
        return
      }
      if (message.type === 'subscribe' || message.type === 'unsubscribe') {
        if (typeof message.event !== 'string' || !htmlTopics.has(message.event)) return
        if (message.type === 'subscribe') topics.add(message.event)
        else topics.delete(message.event)
        syncSubscription()
        if (message.type === 'subscribe' && (message.event === 'connection' || message.event === '*'))
          event('connection', options.status())
        return
      }
      if (!validID) return
      if (
        message.type !== 'query' ||
        Object.keys(message).some((key) => !['type', 'id', 'sql', 'params'].includes(key))
      ) {
        failure(id, 'unsupported_operation', 'HTML 面板只能查询和订阅，不能提交 HTTP 路径或写操作')
        return
      }
      if (
        typeof message.sql !== 'string' ||
        message.sql.length === 0 ||
        encoder.encode(message.sql).length > 262144
      ) {
        failure(id, 'invalid_query', 'SQL 必须是至多 256 KiB 的非空字符串')
        return
      }
      if (pending.has(id) || pending.size >= 4) {
        failure(id, 'query_busy', 'HTML 面板最多同时执行四个不同的只读查询，请等待或取消现有查询', 429)
        return
      }
      let params: Record<string, unknown>
      try {
        params = mergeHTMLParams(options.context().params, message.params)
      } catch (error) {
        failure(id, 'invalid_params', error instanceof Error ? error.message : '查询参数无效')
        return
      }
      const controller = new AbortController()
      pending.set(id, controller)
      void options.api
        .request<unknown>('/query', {
          method: 'POST',
          signal: controller.signal,
          body: JSON.stringify({ sql: message.sql, params }),
        })
        .then((value) => {
          if (controller.signal.aborted) return
          if (encoder.encode(JSON.stringify(value)).length > 16 * 1024 * 1024)
            failure(id, 'result_too_large', 'HTML 面板结果超过 16 MiB，请减少返回行列')
          else send({ type: 'result', id, ok: true, value })
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted)
            failure(
              id,
              'query_failed',
              error instanceof Error ? error.message : '只读查询失败',
              error instanceof APIError ? error.status : 400,
            )
        })
        .finally(() => {
          if (pending.get(id) === controller) pending.delete(id)
        })
    },
  }
}
