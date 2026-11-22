/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useState } from 'react'
import { useGate } from './access'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { inLauncher } from './launcher'
import { useApp } from './state'
import type { SystemUpdateState } from './types'

const WEB_AUTO_CHECK_MS = 20 * 60 * 1000
const WEB_FOCUS_RECHECK_MS = 10 * 60 * 1000

function parseSemver(raw?: string): [number, number, number] | null {
  if (!raw) return null
  const clean = raw.trim().replace(/^v/i, '').split(/[-+]/)[0]
  if (!clean || clean === 'dev') return null
  const parts = clean.split('.').map((n) => Number.parseInt(n, 10))
  if (parts.length < 2 || parts.some((n) => !Number.isFinite(n) || n < 0)) return null
  return [parts[0] || 0, parts[1] || 0, parts[2] || 0]
}

export function isNewerVersion(remote?: string, current?: string): boolean {
  const r = parseSemver(remote)
  const c = parseSemver(current)
  if (!r || !c) return false
  for (let i = 0; i < 3; i++) {
    if (r[i] !== c[i]) return r[i] > c[i]
  }
  return false
}

export function useSystemUpdate(enabled = true) {
  const { api } = useApp()
  const [update, setUpdate] = useState<SystemUpdateState | null>(null)
  const [actionError, setActionError] = useState('')
  const [handoff, setHandoff] = useState<'idle' | 'pending' | 'sent'>('idle')

  const refreshState = useCallback(async () => {
    if (!enabled) return null
    try {
      const next = await api.request<SystemUpdateState>('/system/update')
      setUpdate(next)
      return next
    } catch {
      return null
    }
  }, [api, enabled])

  const sendAction = useCallback(
    async (action: 'check' | 'refresh' | 'download') => {
      if (!enabled) return null
      if (action !== 'check') {
        setActionError('')
        setHandoff('pending')
      }
      try {
        const next = await api.request<SystemUpdateState>('/system/update', {
          method: 'POST',
          body: JSON.stringify({ action }),
        })
        if (next.state === 'window') {
          setHandoff('sent')
        } else {
          setUpdate(next)
          if (action !== 'check') setHandoff('idle')
        }
        return next
      } catch (err: unknown) {
        if (action !== 'check') {
          setHandoff('idle')
          setActionError(err instanceof Error ? err.message : String(err))
        }
        return null
      }
    },
    [api, enabled],
  )

  // Initial read + periodic background check every 20 minutes + focus recheck (throttled to 10 min).
  useEffect(() => {
    if (!enabled) return
    let lastCheck = 0
    const runCheck = (force: boolean) => {
      const now = Date.now()
      if (!force && now - lastCheck < WEB_FOCUS_RECHECK_MS) return
      lastCheck = now
      void sendAction('check')
    }
    void refreshState().then((cur) => {
      if (!cur || cur.state === 'idle' || cur.state === 'none' || cur.state === 'error') {
        runCheck(true)
      }
    })
    const timer = setInterval(() => runCheck(true), WEB_AUTO_CHECK_MS)
    const onVisible = () => {
      if (document.visibilityState === 'visible') runCheck(false)
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [enabled, refreshState, sendAction])

  // Only release discovery runs in the service; download progress belongs to the desktop window.
  const active = update?.state === 'checking'
  useEffect(() => {
    if (!enabled || !active) return
    const timer = setInterval(() => {
      void refreshState()
    }, 450)
    return () => clearInterval(timer)
  }, [enabled, active, refreshState])

  return {
    update,
    actionError,
    checkNow: () => void sendAction('refresh'),
    downloadUpdate: () => void sendAction('download'),
    handoff,
  }
}

export function WebUpdatePill() {
  const { guide, guest } = useApp()
  const { t } = useI18n()
  const gate = useGate()
  const enabled = !guest && !inLauncher() && !!guide?.version && guide.version !== 'dev'
  const { update, actionError, handoff, checkNow, downloadUpdate } = useSystemUpdate(enabled)
  if (!enabled) return null

  const available = update?.state === 'available'
  const action = available ? t('updateInWindow') : t('updateCheckInWindow')
  let label = action
  if (available && update.version) label = t('updateAvailable', { version: 'v' + update.version })
  if (handoff === 'sent') label = t('updateWindowSent')
  return (
    <div>
      <button
        type="button"
        className={'lm-web-update' + (available ? ' is-available' : '')}
        disabled={handoff === 'pending'}
        onClick={() => gate('config', action, available ? downloadUpdate : checkNow)}
        title={action}
      >
        <span className="lm-web-update-text">{label}</span>
        {available && <span className="lm-web-update-cta">{action}</span>}
        <Glyph name={available ? 'download' : 'refresh'} size={12} />
      </button>
      {actionError && <small role="alert">{actionError}</small>}
    </div>
  )
}
