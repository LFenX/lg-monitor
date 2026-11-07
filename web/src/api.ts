import type { Query, Scope, StreamStatus } from './types'
import type { ValidationIssue } from './templates'

export class APIError extends Error {
  constructor(
    message: string,
    public readonly status = 0,
    public readonly issues: ValidationIssue[] = [],
  ) {
    super(message)
  }
}

export class API {
  scope: Scope = ''
  onScope?: (value: Scope) => void
  onUnauthorized?: () => void
  constructor(
    readonly token: string,
    readonly fetcher: typeof fetch = globalThis.fetch.bind(globalThis),
  ) {}

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    let response: Response
    try {
      response = await this.fetcher('/api/v1' + path, {
        ...init,
        redirect: 'error',
        credentials: 'omit',
        cache: 'no-store',
        headers: {
          Authorization: 'Bearer ' + this.token,
          ...(init.body ? { 'Content-Type': 'application/json' } : {}),
          ...init.headers,
        },
      })
    } catch (err) {
      if (init.signal?.aborted) throw err
      throw new APIError('无法连接监控器，请检查地址和网络')
    }
    this.receiveScope(response)
    if (!response.ok) {
      if (response.status === 401) this.onUnauthorized?.()
      let message = '请求失败（HTTP ' + response.status + '）'
      let issues: ValidationIssue[] = []
      try {
        const value = await response.json()
        if (typeof value?.error?.message === 'string') message = value.error.message
        if (Array.isArray(value?.error?.issues))
          issues = value.error.issues.filter(
            (issue: ValidationIssue) =>
              issue &&
              typeof issue.file === 'string' &&
              Number.isInteger(issue.line) &&
              typeof issue.message === 'string',
          )
      } catch {
        /* Upstream bodies are never rendered as HTML. */
      }
      throw new APIError(message, response.status, issues)
    }
    try {
      return (await response.json()) as T
    } catch {
      throw new APIError('监控器返回的数据格式无效，请重试')
    }
  }

  receiveScope(response: Response) {
    const value = response.headers.get('X-Lgmon-Scope')
    if (value === 'read' || value === 'config' || value === 'admin') {
      this.scope = value
      this.onScope?.(value)
    }
  }

  async query<T>(sql: string, signal?: AbortSignal): Promise<T[]> {
    const result = await this.request<Query>('/query', {
      method: 'POST',
      body: JSON.stringify({ sql }),
      signal,
    })
    if (result.truncated) throw new APIError('结果超过一万行，请缩小查询范围')
    return result.rows.map((row) => Object.fromEntries(result.columns.map((name, i) => [name, row[i]])) as T)
  }
}

export function quoteSQL(value: string) {
  return "'" + value.replaceAll("'", "''") + "'"
}

export const summarySQL =
  'SELECT s.collector_id,s.ts,s.cpu,s.memory_percent,s.memory_used,s.memory_total,s.uptime FROM collectors c JOIN system_metrics s ON s.collector_id=c.id AND s.ts=(SELECT max(n.ts) FROM system_metrics n WHERE n.collector_id=c.id) ORDER BY c.id'

export async function readEvents(
  body: ReadableStream<Uint8Array>,
  signal: AbortSignal,
  receive: (event: string, data: string) => void,
) {
  const reader = body.getReader(),
    decoder = new TextDecoder()
  let buffer = '',
    name = 'message',
    data: string[] = [],
    size = 0
  const cancel = () => {
    void reader.cancel().catch(() => {})
  }
  signal.addEventListener('abort', cancel, { once: true })
  try {
    while (!signal.aborted) {
      const next = await reader.read()
      buffer += decoder.decode(next.value, { stream: !next.done })
      let end: number
      while ((end = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, end).replace(/\r$/, '')
        buffer = buffer.slice(end + 1)
        size += line.length
        if (size > 1024 * 1024) throw new APIError('实时事件过大，正在重新读取快照')
        if (!line) {
          if (data.length || name === 'reset') receive(name, data.join('\n'))
          name = 'message'
          data = []
          size = 0
        } else if (line === ': heartbeat') receive('heartbeat', '')
        else if (line.startsWith('event:')) name = line.slice(6).trimStart()
        else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''))
      }
      if (buffer.length > 1024 * 1024) throw new APIError('实时事件过大，正在重新读取快照')
      if (next.done) break
    }
  } finally {
    signal.removeEventListener('abort', cancel)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

function wait(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve) => {
    const done = () => {
      clearTimeout(timer)
      signal.removeEventListener('abort', done)
      resolve()
    }
    const timer = setTimeout(done, ms)
    signal.addEventListener('abort', done, { once: true })
    if (signal.aborted) done()
  })
}

export async function stream(
  api: API,
  signal: AbortSignal,
  status: (value: StreamStatus) => void,
  invalidate: () => void,
) {
  let delay = 1000
  while (!signal.aborted) {
    status(delay === 1000 ? 'connecting' : 'reconnecting')
    try {
      const response = await api.fetcher('/api/v1/stream', {
        signal,
        redirect: 'error',
        cache: 'no-store',
        credentials: 'omit',
        headers: { Authorization: 'Bearer ' + api.token },
      })
      api.receiveScope(response)
      if (response.status === 401) {
        api.onUnauthorized?.()
        return
      }
      if (
        !response.ok ||
        !response.body ||
        !response.headers.get('Content-Type')?.startsWith('text/event-stream')
      )
        throw new APIError('实时连接暂时不可用')
      status('connected')
      invalidate()
      await readEvents(response.body, signal, () => invalidate())
    } catch {
      if (signal.aborted) break
    }
    if (signal.aborted) break
    status('reconnecting')
    invalidate()
    await wait(delay, signal)
    delay = Math.min(delay * 2, 15000)
  }
}
