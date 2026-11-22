/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { API } from './api'
import type { SystemRestartResult } from './types'

/*
 * Polls /api/v1/session until lgmon comes back after a restart or in-place update, then reloads
 * the page so the UI picks up the new version or migrated storage directory automatically.
 */
export function waitForReconnectAndReload(delayBeforePollMs = 2200): () => void {
  let stopped = false
  let timer: ReturnType<typeof setTimeout> | null = null
  const deadline = Date.now() + 90_000

  const probe = () => {
    if (stopped) return
    if (Date.now() > deadline) {
      window.location.reload()
      return
    }
    void fetch('/api/v1/session', { cache: 'no-store' })
      .then((res) => {
        if (!stopped && res.ok) {
          window.location.reload()
          return
        }
        if (!stopped) timer = setTimeout(probe, 1200)
      })
      .catch(() => {
        if (!stopped) timer = setTimeout(probe, 1200)
      })
  }

  timer = setTimeout(probe, delayBeforePollMs)
  return () => {
    stopped = true
    if (timer) clearTimeout(timer)
  }
}

export function useSystemRestart(api: API) {
  const [restarting, setRestarting] = useState(false)
  const [restartMessage, setRestartMessage] = useState('')
  const [restartError, setRestartError] = useState('')
  const cancelWaitRef = useRef<(() => void) | null>(null)

  useEffect(() => {
    return () => {
      cancelWaitRef.current?.()
    }
  }, [])

  const triggerRestart = useCallback(async () => {
    setRestarting(true)
    setRestartError('')
    try {
      const res = await api.request<SystemRestartResult>('/system/restart', { method: 'POST' })
      setRestartMessage(res?.message || '')
      cancelWaitRef.current?.()
      cancelWaitRef.current = waitForReconnectAndReload(2200)
      return true
    } catch (err: unknown) {
      setRestarting(false)
      setRestartError(err instanceof Error ? err.message : '重启 lgmon 失败')
      return false
    }
  }, [api])

  return { restarting, restartMessage, restartError, triggerRestart }
}
