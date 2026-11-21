import { act, render } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { openBoardPage, resetBoardTabForTest, revealMarker, useRevealWhenAsked } from './reveal'
import type { LiveUpdate } from './html-dashboard'

const listeners = new Set<(update: LiveUpdate) => void>()
const refresh = vi.fn()
vi.mock('./state', () => ({
  useApp: () => ({
    refresh,
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
  refresh.mockReset()
  resetBoardTabForTest()
  document.title = '我的服务 · lgmon'
  window.location.hash = '#/'
  window.name = ''
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
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

it('goes to the page it was asked for, refreshes it, and marks the title for a few seconds', () => {
  render(<Page />)
  send({ hash: '#/boards/main', nonce: 'a5f0' })
  expect(window.location.hash).toBe('#/boards/main')
  expect(refresh).toHaveBeenCalledTimes(1)
  expect(document.title).toBe('我的服务 · lgmon' + revealMarker('a5f0'))
  // A second click while the first mark is still there replaces it rather than stacking.
  send({ hash: '#/boards/main', nonce: '0001' })
  expect(refresh).toHaveBeenCalledTimes(2)
  expect(document.title).toBe('我的服务 · lgmon' + revealMarker('0001'))
  act(() => vi.advanceTimersByTime(5000))
  expect(document.title).toBe('我的服务 · lgmon')
  // Anything that is not an lgmon page address is ignored.
  send({ hash: 'https://example.test/', nonce: 'zz' })
  expect(window.location.hash).toBe('#/boards/main')
  expect(document.title).toBe('我的服务 · lgmon')
})

it('leaves lgmon’s own window and #/start alone', () => {
  ;(window as unknown as { lgmonLauncher?: boolean }).lgmonLauncher = true
  const first = render(<Page />)
  expect(listeners.size).toBe(0)
  first.unmount()
  delete (window as unknown as { lgmonLauncher?: boolean }).lgmonLauncher
  window.location.hash = '#/start'
  render(<Page />)
  expect(listeners.size).toBe(0)
})

it('reuses and refreshes an already-open board tab instead of opening duplicates', () => {
  const channels = new Set<FakeChannel>()
  class FakeChannel {
    onmessage: ((event: MessageEvent) => void) | null = null
    constructor(public name: string) {
      channels.add(this)
    }
    postMessage(data: unknown) {
      for (const peer of channels) {
        if (peer !== this && peer.name === this.name) peer.onmessage?.({ data } as MessageEvent)
      }
    }
    close() {
      channels.delete(this)
    }
  }
  vi.stubGlobal('BroadcastChannel', FakeChannel)

  // Case 1: An open board tab answers on BroadcastChannel -> refresh it without calling window.open.
  const openSpy = vi.fn()
  vi.stubGlobal('open', openSpy)
  const board = render(<Page />)
  openBoardPage('#/alerts')
  act(() => vi.advanceTimersByTime(200))
  expect(openSpy).not.toHaveBeenCalled()
  expect(window.location.hash).toBe('#/alerts')
  expect(refresh).toHaveBeenCalledTimes(1)
  board.unmount()

  // Case 2: No board tab is open -> opens one named tab, and a second click reuses it instead of opening another.
  const fakeWindow = { closed: false, location: { hash: '#/' }, focus: vi.fn() }
  openSpy.mockReturnValue(fakeWindow)
  openBoardPage('#/')
  act(() => vi.advanceTimersByTime(150))
  expect(openSpy).toHaveBeenCalledTimes(1)
  expect(openSpy.mock.calls[0][1]).toBe('lgmon-board')
  openBoardPage('#/settings')
  expect(openSpy).toHaveBeenCalledTimes(1)
  expect(fakeWindow.location.hash).toBe('#/settings')
  expect(fakeWindow.focus).toHaveBeenCalled()
})
