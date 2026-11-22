/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useRef, useState } from 'react'
import { useGate } from './access'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { inLauncher } from './launcher'
import { waitForReconnectAndReload } from './restart'
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
  const cancelWaitRef = useRef<(() => void) | null>(null)

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
    async (action: 'check' | 'refresh' | 'download' | 'apply') => {
      if (!enabled) return null
      setActionError('')
      try {
        const next = await api.request<SystemUpdateState>('/system/update', {
          method: 'POST',
          body: JSON.stringify({ action }),
        })
        setUpdate(next)
        if (action === 'apply' && next?.state === 'applying') {
          cancelWaitRef.current?.()
          cancelWaitRef.current = waitForReconnectAndReload(4000)
        }
        return next
      } catch (err: unknown) {
        setActionError(err instanceof Error ? err.message : '更新操作失败')
        return null
      }
    },
    [api, enabled],
  )

  useEffect(() => {
    return () => {
      cancelWaitRef.current?.()
    }
  }, [])

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

  // Poll live status while checking or downloading installer bytes.
  const active = update?.state === 'checking' || update?.state === 'downloading'
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
    applyUpdate: () => void sendAction('apply'),
  }
}

/*
 * Shown in the full browser dashboard sidebar footer: triggers the exact same in-app
 * check -> SHA-256 verified download -> restart & apply update flow as lgmon's Start window.
 */
export function WebUpdatePill() {
  const { guide, guest } = useApp()
  const { t } = useI18n()
  const gate = useGate()
  const version = guide?.version || ''
  const dev = !version || version === 'dev'
  const enabled = !guest && !inLauncher() && !dev
  const { update, actionError, checkNow, downloadUpdate, applyUpdate } = useSystemUpdate(enabled)

  if (guest || inLauncher() || dev) return null

  const state = update?.state || 'idle'
  const percent =
    update?.total && update.total > 0
      ? Math.min(100, Math.round(((update.done || 0) / update.total) * 100))
      : 0

  if (state === 'downloading') {
    return (
      <div className="lm-web-update is-downloading" role="status" aria-live="polite">
        <div className="lm-web-update-row">
          <span className="lm-web-update-text">
            {t('updateDownloading', { version: update?.version ? 'v' + update.version : '' })}
          </span>
          <strong className="lm-web-update-pct">{percent}%</strong>
        </div>
        <span className="lm-web-update-bar" role="progressbar" aria-valuenow={percent}>
          <i style={{ width: percent + '%' }} />
        </span>
      </div>
    )
  }

  if (state === 'ready') {
    return (
      <button
        type="button"
        className="lm-web-update is-ready"
        onClick={() => gate('config', t('updateRestartNow'), applyUpdate)}
        title={t('updateReadyBody')}
      >
        <span className="lm-web-update-dot is-ready" aria-hidden="true" />
        <span className="lm-web-update-text">
          {t('updateReadyTitle', { version: update?.version ? 'v' + update.version : '' })}
        </span>
        <span className="lm-web-update-cta is-ready">{t('updateRestartNow')}</span>
      </button>
    )
  }

  if (state === 'applying') {
    return (
      <div className="lm-web-update is-ready" role="status" aria-live="polite">
        <span className="lm-web-update-text">{t('updateApplying')}</span>
      </div>
    )
  }

  if (state === 'available' || (state === 'error' && update?.during === 'download')) {
    const label =
      state === 'error'
        ? t('updateDownloadFailed')
        : t('updateAvailable', { version: update?.version ? 'v' + update.version : '' })
    return (
      <button
        type="button"
        className="lm-web-update is-available"
        onClick={() => gate('config', t('updateStartDownload'), downloadUpdate)}
        title={update?.message || t('updateAutoHint')}
      >
        <span className="lm-web-update-dot" aria-hidden="true" />
        <span className="lm-web-update-text">{label}</span>
        <span className="lm-web-update-cta">
          <Glyph name="download" size={12} />
          {state === 'error' ? t('aiRetry') : t('updateDownloadNow')}
        </span>
      </button>
    )
  }

  const failed = state === 'error' || !!actionError
  const display = version.startsWith('v') ? version : 'v' + version
  return (
    <button
      type="button"
      className="lm-web-update"
      disabled={state === 'checking'}
      onClick={() => gate('config', t('updateCheckNow'), checkNow)}
      title={actionError || update?.message || t('updateAutoHint')}
    >
      <span className="lm-web-update-text">
        {state === 'checking'
          ? t('updateChecking')
          : failed
            ? t('updateFailedCheck')
            : `${display} · ${t('updateUpToDate')}`}
      </span>
      <Glyph name="refresh" size={12} />
    </button>
  )
}
