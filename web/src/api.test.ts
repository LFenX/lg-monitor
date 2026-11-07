import { describe, expect, it, vi } from 'vitest'
import { API, APIError, quoteSQL, readEvents, stream } from './api'

describe('HTTP authentication and errors', () => {
  it('keeps credentials in the header and learns the actual scope', async () => {
    const fetcher = vi.fn(
        async (_input: RequestInfo | URL, _init?: RequestInit) =>
          new Response('[]', { headers: { 'X-Lgmon-Scope': 'config' } }),
      ),
      api = new API('fake-private', fetcher)
    await api.request('/collectors')
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/collectors',
      expect.objectContaining({
        redirect: 'error',
        credentials: 'omit',
        headers: { Authorization: 'Bearer fake-private' },
      }),
    )
    expect(api.scope).toBe('config')
    expect(fetcher.mock.calls[0][0]).not.toContain('fake-private')
  })
  it('uses Chinese network and malformed-response errors without showing upstream bodies', async () => {
    const offline = new API(
      'fake',
      vi.fn(async () => {
        throw new Error('FAKE_PRIVATE_NETWORK')
      }),
    )
    await expect(offline.request('/collectors')).rejects.toThrow('无法连接监控器')
    const malformed = new API(
      'fake',
      vi.fn(async () => new Response('FAKE_PRIVATE_BODY')),
    )
    await expect(malformed.request('/collectors')).rejects.toThrow('数据格式无效')
    const unauthorized = new API(
        'fake',
        vi.fn(async () => new Response('{"error":{"message":"访问令牌无效"}}', { status: 401 })),
      ),
      callback = vi.fn()
    unauthorized.onUnauthorized = callback
    await expect(unauthorized.request('/collectors')).rejects.toEqual(new APIError('访问令牌无效', 401))
    expect(callback).toHaveBeenCalledOnce()
  })
  it('quotes collector identities as a single SQL literal', () => {
    expect(quoteSQL("node' OR 1=1 --")).toBe("'node'' OR 1=1 --'")
  })
})

describe('SSE transport', () => {
  it('decodes fragmented UTF-8, CRLF, multiline data, heartbeat and reset', async () => {
    const content = new TextEncoder().encode(
      'event: alert\r\ndata: 中文\r\ndata: second\r\n\r\n: heartbeat\r\n\r\nevent: reset\r\ndata: {}\r\n\r\n',
    )
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (let i = 0; i < content.length; i++) controller.enqueue(content.slice(i, i + 1))
        controller.close()
      },
    })
    const events: [string, string][] = []
    await readEvents(body, new AbortController().signal, (name, data) => events.push([name, data]))
    expect(events).toEqual([
      ['alert', '中文\nsecond'],
      ['heartbeat', ''],
      ['reset', '{}'],
    ])
  })
  it('cancels a pending reader and does not reconnect after abort', async () => {
    const controller = new AbortController(),
      cancel = vi.fn(),
      fetcher = vi.fn(
        async () =>
          new Response(new ReadableStream({ cancel }), { headers: { 'Content-Type': 'text/event-stream' } }),
      ),
      invalidate = vi.fn()
    const running = stream(
      new API('fake', fetcher),
      controller.signal,
      (value) => {
        if (value === 'connected') queueMicrotask(() => controller.abort())
      },
      invalidate,
    )
    await running
    expect(fetcher).toHaveBeenCalledOnce()
    expect(cancel).toHaveBeenCalled()
    expect(invalidate).toHaveBeenCalledOnce()
  })
  it('rejects oversized unterminated events', async () => {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array(1024 * 1024 + 1))
        controller.close()
      },
    })
    await expect(readEvents(body, new AbortController().signal, () => {})).rejects.toThrow('实时事件过大')
  })
})
