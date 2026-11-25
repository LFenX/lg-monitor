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

function fmtVer(raw?: string): string {
  const v = (raw || '').trim()
  if (!v) return ''
  return v.startsWith('v') ? v : 'v' + v
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
    async (action: 'check' | 'refresh' | 'download' | 'apply', force = false) => {
      if (!enabled) return null
      const isHandoff = action === 'refresh' || action === 'download' || action === 'apply'
      if (isHandoff || force) {
        setActionError('')
      }
      if (isHandoff) {
        setHandoff('pending')
      }
      try {
        const body = force ? { action, force: true } : { action }
        const next = await api.request<SystemUpdateState>('/system/update', {
          method: 'POST',
          body: JSON.stringify(body),
        })
        if (next.state === 'window') {
          setHandoff('sent')
        } else {
          setUpdate(next)
          if (isHandoff) setHandoff('idle')
        }
        return next
      } catch (err: unknown) {
        if (isHandoff || force) {
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

  // Poll until background release discovery settles.
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
    checkNow: () => void sendAction('check', true),
    downloadUpdate: () => void sendAction('apply'),
    applyUpdate: () => void sendAction('apply'),
    handoff,
  }
}

/* compact is the folded sidebar's version: one icon, there only while a new version waits. */
export function WebUpdatePill({ compact }: { compact?: boolean }) {
  const { guide, guest } = useApp()
  const { lang, t } = useI18n()
  const gate = useGate()
  const enabled = !guest && !inLauncher() && !!guide?.version && guide.version !== 'dev'
  const { update, actionError, handoff, checkNow, applyUpdate } = useSystemUpdate(enabled)
  const [linuxNotice, setLinuxNotice] = useState('')
  if (!enabled) return null

  const currentVer = fmtVer(update?.current || guide?.version)
  const latestVer = fmtVer(update?.version)
  const available = update?.state === 'available' && !!latestVer
  const checking = update?.state === 'checking'
  const upToDate = update?.state === 'none'
  const failed = update?.state === 'error'
  const isLinux = update?.platform === 'linux'

  const action = available ? t('updateDownloadNow') : t('updateCheckNow')
  let label = currentVer ? `${currentVer} · ${t('updateCheckNow')}` : t('updateCheckNow')
  if (checking) {
    label = currentVer ? `${currentVer} · ${t('updateChecking')}` : t('updateChecking')
  } else if (available) {
    label = currentVer ? `${currentVer} → ${latestVer}` : t('updateAvailable', { version: latestVer })
  } else if (upToDate) {
    label = currentVer ? `${currentVer} · ${t('updateUpToDate')}` : t('updateUpToDate')
  } else if (failed) {
    label = currentVer ? `${currentVer} · ${t('updateRetry')}` : t('updateRetry')
  }
  if (handoff === 'sent') {
    label = t('updateWindowSent')
  }

  const title = available
    ? isLinux
      ? `${currentVer} → ${latestVer} · sudo lgmon update -y`
      : lang === 'en'
        ? `Update ${currentVer} → ${latestVer} automatically in the lgmon window`
        : `点击弹出启动小窗并自动更新（${currentVer} → ${latestVer}）`
    : upToDate && latestVer
      ? `${currentVer} (${t('updateUpToDate')}) · ${t('updateAutoHint')}`
      : t('updateAutoHint')

  const handleClick = () => {
    setLinuxNotice('')
    if (available && isLinux) {
      const cmd = 'sudo lgmon update -y'
      try {
        void navigator.clipboard?.writeText(cmd)
      } catch {
        // Ignore clipboard errors in insecure contexts.
      }
      setLinuxNotice(lang === 'en' ? `Run on Linux: ${cmd}` : `请在 Linux 终端运行：${cmd}`)
      return
    }
    gate('config', action, available ? applyUpdate : checkNow)
  }

  if (compact) {
    if (!available) return null
    const notice = actionError || linuxNotice
    return (
      <button
        type="button"
        className="lm-btn ghost small icon lm-rail-update"
        disabled={handoff === 'pending'}
        onClick={handleClick}
        title={notice || title}
        aria-label={`${action} ${label}`}
      >
        <Glyph name="download" size={16} />
      </button>
    )
  }

  return (
    <div>
      <button
        type="button"
        className={'lm-web-update' + (available ? ' is-available' : '')}
        disabled={handoff === 'pending' || checking}
        onClick={handleClick}
        title={title}
      >
        {available && <span className="lm-web-update-dot" aria-hidden="true" />}
        <span className="lm-web-update-text">{label}</span>
        {available && handoff !== 'sent' && <span className="lm-web-update-cta">{action}</span>}
        <Glyph name={available ? 'download' : 'refresh'} size={12} />
      </button>
      {(actionError || linuxNotice) && <small role="alert">{actionError || linuxNotice}</small>}
    </div>
  )
}
