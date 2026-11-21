/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect } from 'react'
import { useApp, useBoardPage } from './state'

/*
 * lgmon's window, start page and tray reuse a browser tab that is already open:
 * - Inside lgmon-window.exe / tray: lgmon sends an SSE reveal so the open tab navigates, refreshes,
 *   and marks its title for a few seconds; the native window brings that browser window to the front.
 * - Inside a browser tab (#/start): BroadcastChannel + a named window reference reuse and refresh the
 *   existing board tab, opening a new tab only when none is open.
 */

const digits = '0123456789abcdef'
const [joiner, zero, one] = [0x2060, 0x200b, 0x200c].map((code) => String.fromCharCode(code))
const channelName = 'lgmon-pages'
const boardWindowName = 'lgmon-board'

// Must match internal/desktop.RevealMarker: a word joiner, then each bit of the nonce as a zero-width space (0)
// or zero-width non-joiner (1). Browsers keep these in the window title, and nobody sees them.
export function revealMarker(nonce: string) {
  if (!/^[0-9a-f]{4}$/.test(nonce)) return ''
  let marker = joiner
  for (const digit of nonce) {
    const value = digits.indexOf(digit)
    for (let bit = 3; bit >= 0; bit--) marker += (value >> bit) & 1 ? one : zero
  }
  return marker
}

const isStartHash = () => /^#\/start(?:\/|\?|$)/.test(location.hash)

let boardTab: Window | null = null
let cancelPending: (() => void) | undefined

export function resetBoardTabForTest() {
  cancelPending?.()
  boardTab = null
}

/* Reuses and refreshes an already-open lgmon board tab, opening a new tab only when none exists. */
export function openBoardPage(href: string) {
  const target = new URL(href, location.href)
  const hash = target.hash && target.hash.startsWith('#/') ? target.hash : '#/'
  const url = target.href
  cancelPending?.()

  const open = () => {
    boardTab = window.open(url, boardWindowName)
    if (boardTab) boardTab.focus()
    else location.assign(url)
  }

  const notify = (id?: string) => {
    if (typeof BroadcastChannel !== 'function') return null
    try {
      const channel = new BroadcastChannel(channelName)
      channel.postMessage({ type: 'reveal', hash, id })
      return channel
    } catch {
      return null
    }
  }

  if (boardTab && !boardTab.closed) {
    try {
      if (boardTab.location.hash !== hash) boardTab.location.hash = hash
    } catch {
      open()
    }
    const channel = notify()
    channel?.close()
    boardTab?.focus?.()
    return
  }

  if (typeof BroadcastChannel === 'function') {
    const id = Math.random().toString(36).slice(2)
    let channel: BroadcastChannel | null = null
    try {
      channel = new BroadcastChannel(channelName)
    } catch {
      channel = null
    }
    if (channel) {
      const finish = () => {
        clearTimeout(timer)
        channel?.close()
        cancelPending = undefined
      }
      const timer = setTimeout(() => {
        finish()
        open()
      }, 120)
      cancelPending = finish
      channel.onmessage = (event: MessageEvent) => {
        const message = event.data as { type?: unknown; id?: unknown } | null
        if (message?.type === 'ack' && message.id === id) {
          finish()
        }
      }
      channel.postMessage({ type: 'reveal', hash, id })
      return
    }
  }

  open()
}

/* Listens for a reveal while the page is open in a browser on this computer. */
export function useRevealWhenAsked() {
  const { subscribeLive, refresh } = useApp()
  const board = useBoardPage()
  useEffect(() => {
    if (!board) return
    if (!window.name) window.name = boardWindowName
    let restore: ReturnType<typeof setTimeout> | undefined
    const apply = (hash: unknown, nonce?: unknown) => {
      if (typeof hash === 'string' && hash.startsWith('#/') && hash !== location.hash) location.hash = hash
      refresh()
      const marker = typeof nonce === 'string' ? revealMarker(nonce) : ''
      if (!marker) return
      clearTimeout(restore)
      const title = document.title.replace(new RegExp(joiner + '[' + zero + one + ']*$'), '')
      document.title = title + marker
      restore = setTimeout(() => {
        if (document.title === title + marker) document.title = title
      }, 5000)
    }
    const stop = subscribeLive((update) => {
      if (update.event !== 'reveal' || isStartHash()) return
      const data = update.data as { hash?: unknown; nonce?: unknown }
      apply(data.hash, data.nonce)
    })
    let channel: BroadcastChannel | null = null
    if (typeof BroadcastChannel === 'function') {
      try {
        channel = new BroadcastChannel(channelName)
        channel.onmessage = (event: MessageEvent) => {
          if (isStartHash()) return
          const message = event.data as { type?: unknown; hash?: unknown; id?: unknown } | null
          if (message?.type !== 'reveal') return
          apply(message.hash)
          window.focus?.()
          if (typeof message.id === 'string') channel?.postMessage({ type: 'ack', id: message.id })
        }
      } catch {
        channel = null
      }
    }
    return () => {
      stop()
      channel?.close()
      clearTimeout(restore)
    }
  }, [subscribeLive, refresh, board])
}
