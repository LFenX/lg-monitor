/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { act, renderHook, waitFor } from '@testing-library/react'
import { expect, it } from 'vitest'
import { useResource } from './state'

it('lets a slow request finish while coalescing refreshes, and aborts on identity changes', async () => {
  const calls: { signal: AbortSignal; resolve: (value: string) => void }[] = []
  const load = (signal: AbortSignal) => new Promise<string>((resolve) => calls.push({ signal, resolve }))
  const view = renderHook(({ revision, id }) => useResource(load, [revision, id], id), {
    initialProps: { revision: 0, id: 'first' },
  })
  await waitFor(() => expect(calls).toHaveLength(1))
  view.rerender({ revision: 1, id: 'first' })
  view.rerender({ revision: 2, id: 'first' })
  expect(calls[0].signal.aborted).toBe(false)
  expect(calls).toHaveLength(1)
  await act(async () => calls[0].resolve('first data'))
  await waitFor(() => expect(view.result.current.value).toBe('first data'))
  expect(calls).toHaveLength(2)
  view.rerender({ revision: 3, id: 'second' })
  expect(calls[1].signal.aborted).toBe(true)
  await waitFor(() => expect(calls).toHaveLength(3))
  expect(view.result.current.value).toBeUndefined()
  await act(async () => calls[1].resolve('stale first'))
  expect(view.result.current.value).toBeUndefined()
  await act(async () => calls[2].resolve('second data'))
  await waitFor(() => expect(view.result.current.value).toBe('second data'))
  view.unmount()
  expect(calls[2].signal.aborted).toBe(true)
})

it('starts from the copy of the last visit and replaces it once lgmon answers', async () => {
  const { render, screen } = await import('@testing-library/react')
  const { State, useApp } = await import('./state')
  const { API } = await import('./api')
  const saved = {
    at: 1_000,
    data: {
      collectors: [],
      targets: [],
      definitions: [],
      alerts: [],
      notifiers: {},
      summaries: [],
      pause: {},
      ruleErrors: [],
    },
    extra: {
      monitors: [{ id: 'kept', name: '上次的服务' }],
      boards: [],
      boardList: [],
      boardsLoaded: true,
      monitorsLoaded: true,
      remoteBoards: [],
    },
  }
  localStorage.setItem('lgmon-snapshot-v1', JSON.stringify(saved))
  let answer: (() => void) | undefined
  const gate = new Promise<void>((resolve) => (answer = resolve))
  const api = new API('fake-token')
  api.request = (async (path: string) => {
    await gate
    if (path === '/monitors') return [{ id: 'live', name: '现在的服务' }]
    if (path === '/query') return { columns: [], rows: [], truncated: false }
    return path.startsWith('/config/') ? {} : []
  }) as typeof api.request
  api.query = (async () => []) as typeof api.query
  function Probe() {
    const { monitors, staleSince } = useApp()
    return <p>{monitors.map((item) => item.name).join(',') + (staleSince ? ' 旧' : ' 新')}</p>
  }
  render(
    <State api={api} scope="read" theme="light" keep now={() => 2_000}>
      <Probe />
    </State>,
  )
  expect(screen.getByText('上次的服务 旧')).toBeTruthy()
  answer?.()
  expect(await screen.findByText('现在的服务 新')).toBeTruthy()
  localStorage.clear()
})
