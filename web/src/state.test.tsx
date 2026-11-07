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
