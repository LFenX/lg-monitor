import { act, render } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { revealMarker, useRevealWhenAsked } from './reveal'
import type { LiveUpdate } from './html-dashboard'

const listeners = new Set<(update: LiveUpdate) => void>()
vi.mock('./state', () => ({
  useApp: () => ({
    subscribeLive: (callback: (update: LiveUpdate) => void) => {
      listeners.add(callback)
      return () => listeners.delete(callback)
    },
  }),
}))

function Page() {
  useRevealWhenAsked()
  return null
}

const send = (data: unknown) =>
  act(() => listeners.forEach((listener) => listener({ event: 'reveal', data })))
const code = (text: string) => [...text].map((char) => char.codePointAt(0)!.toString(16)).join(' ')

beforeEach(() => {
  listeners.clear()
  document.title = '我的服务 · lgmon'
  window.location.hash = '#/'
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
  delete (window as unknown as { lgmonLauncher?: boolean }).lgmonLauncher
})

it('encodes the nonce exactly like the Go side', () => {
  // internal/desktop/reveal_test.go checks the same vector.
  expect(code(revealMarker('a5f0'))).toBe(
    '2060 200c 200b 200c 200b 200b 200c 200b 200c 200c 200c 200c 200c 200b 200b 200b 200b',
  )
  expect(revealMarker('A5F0')).toBe('')
  expect(revealMarker('a5f')).toBe('')
})

it('goes to the page it was asked for and marks the title for a few seconds', () => {
  render(<Page />)
  send({ hash: '#/boards/main', nonce: 'a5f0' })
  expect(window.location.hash).toBe('#/boards/main')
  expect(document.title).toBe('我的服务 · lgmon' + revealMarker('a5f0'))
  // A second click while the first mark is still there replaces it rather than stacking.
  send({ hash: '#/boards/main', nonce: '0001' })
  expect(document.title).toBe('我的服务 · lgmon' + revealMarker('0001'))
  act(() => vi.advanceTimersByTime(5000))
  expect(document.title).toBe('我的服务 · lgmon')
  // Anything that is not an lgmon page address is ignored.
  send({ hash: 'https://example.test/', nonce: 'zz' })
  expect(window.location.hash).toBe('#/boards/main')
  expect(document.title).toBe('我的服务 · lgmon')
})

it('leaves lgmon’s own window alone', () => {
  ;(window as unknown as { lgmonLauncher?: boolean }).lgmonLauncher = true
  render(<Page />)
  expect(listeners.size).toBe(0)
})
