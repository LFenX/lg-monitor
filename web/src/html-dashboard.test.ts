import { expect, it, vi } from 'vitest'
import { API } from './api'
import { createHTMLBridge, mergeHTMLParams } from './html-dashboard'
import type { LiveUpdate } from './html-dashboard'

function fixture(
  fetcher: typeof fetch = vi.fn(async () =>
    Response.json({ columns: ['value'], rows: [[7]], truncated: false }),
  ),
) {
  const sent: Record<string, unknown>[] = []
  const listeners = new Set<(event: LiveUpdate) => void>()
  const error = vi.fn()
  const api = new API('fake-parent-private-token', fetcher)
  const bridge = createHTMLBridge({
    api,
    context: () => ({
      title: '虚构面板',
      theme: 'dark',
      refresh: '10s',
      params: { range: '6h', collector: 'fake-host' },
    }),
    status: () => 'connected',
    send: (value) => sent.push(value as Record<string, unknown>),
    error,
    subscribe: (callback) => {
      listeners.add(callback)
      return () => {
        listeners.delete(callback)
      }
    },
  })
  return {
    bridge,
    sent,
    error,
    listeners,
    api,
    fetcher,
    emit: (event: LiveUpdate) => {
      for (const callback of listeners) callback(event)
    },
  }
}

it('merges explicit time parameters as a group and preserves null/all collectors', () => {
  const base = { from: 10, to: 20, collector: 'fake-host' }
  expect(mergeHTMLParams(base, { range: '1h', collector: null })).toEqual({ range: '1h', collector: null })
  expect(mergeHTMLParams({ range: '6h' }, { from: 30, to: 40 })).toEqual({ from: 30, to: 40 })
  expect(mergeHTMLParams(base, undefined)).toEqual(base)
  for (const invalid of [
    null,
    [],
    { path: '/config/targets' },
    { from: 1.1 },
    { collector: '' },
    { range: 123 },
  ])
    expect(() => mergeHTMLParams(base, invalid)).toThrow()
})

it('proxies only the query endpoint and keeps credentials in the parent request', async () => {
  const fetcher = vi.fn(async (path, init) => {
    expect(path).toBe('/api/v1/query')
    expect(init?.method).toBe('POST')
    expect((init?.headers as Record<string, string>).Authorization).toBe('Bearer fake-parent-private-token')
    expect(JSON.parse(String(init?.body))).toEqual({
      sql: 'SELECT 7 AS value',
      params: { from: 1, to: 2, collector: 'fake-host' },
    })
    return Response.json({ columns: ['value'], rows: [[7]], truncated: false })
  })
  const f = fixture(fetcher)
  f.bridge.receive({ type: 'query', id: 1, sql: 'SELECT 7 AS value', params: { from: 1, to: 2 } })
  await vi.waitFor(() =>
    expect(f.sent).toContainEqual({
      type: 'result',
      id: 1,
      ok: true,
      value: { columns: ['value'], rows: [[7]], truncated: false },
    }),
  )
  expect(JSON.stringify(f.sent)).not.toContain('fake-parent-private-token')
  f.bridge.receive({ type: 'write', id: 2, path: '/config/targets' })
  f.bridge.receive({ type: 'query', id: 3, sql: 'SELECT 1', path: '/config/targets' })
  f.bridge.receive({ type: 'query', id: 4, sql: 'SELECT 1', params: { method: 'PUT' } })
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(f.sent.filter((message) => message.ok === false)).toHaveLength(3)
  f.bridge.close()
})

it('bounds concurrent queries, frees cancelled slots, and aborts all work on close', async () => {
  const signals: AbortSignal[] = []
  const f = fixture(
    vi.fn(
      (_path, init) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = init?.signal as AbortSignal
          signals.push(signal)
          signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), {
            once: true,
          })
        }),
    ),
  )
  for (let id = 1; id <= 5; id++) f.bridge.receive({ type: 'query', id, sql: 'SELECT 1' })
  expect(signals).toHaveLength(4)
  expect(f.sent).toContainEqual(
    expect.objectContaining({ id: 5, ok: false, error: expect.objectContaining({ code: 'query_busy' }) }),
  )
  f.bridge.receive({ type: 'cancel', id: 1 })
  expect(signals[0].aborted).toBe(true)
  f.bridge.receive({ type: 'query', id: 6, sql: 'SELECT 1' })
  expect(signals).toHaveLength(5)
  f.bridge.close()
  expect(signals.every((signal) => signal.aborted)).toBe(true)
  f.bridge.receive({ type: 'query', id: 7, sql: 'SELECT 1' })
  expect(signals).toHaveLength(5)
})

it('shares one live subscription, respects event filters, handles ACKs and releases subscribers', () => {
  const f = fixture()
  f.bridge.receive({ type: 'subscribe', event: 'target' })
  f.bridge.receive({ type: 'subscribe', event: 'target' })
  f.bridge.receive({ type: 'subscribe', event: 'alert' })
  expect(f.listeners.size).toBe(1)
  f.emit({ event: 'sample', data: { value: 10 } })
  expect(f.sent).toHaveLength(0)
  f.emit({ event: 'target', data: { target: 'fake' } })
  expect(f.sent[0]).toEqual({ type: 'event', id: 1, event: 'target', data: { target: 'fake' } })
  f.bridge.receive({ type: 'event_ack', id: 1 })
  for (let i = 0; i < 100; i++) {
    f.emit({ event: 'target', data: i })
    f.bridge.receive({ type: 'event_ack', id: i + 2 })
  }
  expect(f.error).not.toHaveBeenCalled()
  f.bridge.receive({ type: 'unsubscribe', event: 'target' })
  f.bridge.receive({ type: 'unsubscribe', event: 'alert' })
  expect(f.listeners.size).toBe(0)
  f.bridge.receive({ type: 'subscribe', event: 'connection' })
  expect(f.sent.at(-1)).toEqual(expect.objectContaining({ event: 'connection', data: 'connected' }))
  f.bridge.close()
  expect(f.listeners.size).toBe(0)
})

it('stops a blocked frame after 64 unacknowledged events and rejects oversized query results', async () => {
  const f = fixture()
  f.bridge.receive({ type: 'subscribe', event: '*' })
  for (let i = 0; i < 100; i++) f.emit({ event: 'sample', data: i })
  expect(f.sent).toHaveLength(64)
  expect(f.listeners.size).toBe(0)
  expect(f.error).toHaveBeenCalledTimes(1)
  f.bridge.close()
  const large = fixture(vi.fn(async () => Response.json({ rows: [['x'.repeat(16 * 1024 * 1024)]] })))
  large.bridge.receive({ type: 'query', id: 1, sql: 'SELECT value' })
  await vi.waitFor(() =>
    expect(large.sent).toContainEqual(
      expect.objectContaining({
        id: 1,
        ok: false,
        error: expect.objectContaining({ code: 'result_too_large' }),
      }),
    ),
  )
  large.bridge.close()
})
