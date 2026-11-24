/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useState } from 'react'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { inLauncher } from './launcher'
import type { Host } from './launcher'

/*
 * Updates happen in lgmon's own window: it finds a newer release automatically in the background, downloads and
 * verifies it, and on "重启并更新" hands over to the new installer. The page only shows what the window reports
 * and asks it to act.
 */

export type HostUpdate = {
  type: 'update'
  state: 'dev' | 'checking' | 'none' | 'available' | 'downloading' | 'ready' | 'applying' | 'error'
  during?: 'check' | 'download' | 'apply'
  message?: string
  current?: string
  version?: string
  notes?: string
  hasPage?: boolean
  size?: number
  done?: number
  total?: number
}

const AUTO_CHECK_MS = 20 * 60 * 1000
const FOCUS_RECHECK_MS = 10 * 60 * 1000

const host = () => window as unknown as Host

function ask(type: string) {
  host().chrome?.webview?.postMessage(JSON.stringify({ type }))
}

/* One subscription for the whole page: the card and the footer link share it, so the window is asked once. */
let latest: HostUpdate | null = null
let asked = false
let lastAutoCheck = 0
let pollTimer: ReturnType<typeof setInterval> | null = null
const listeners = new Set<(update: HostUpdate) => void>()

function receive(event: Event) {
  const detail = (event as CustomEvent).detail
  if (detail?.type !== 'update') return
  latest = detail
  listeners.forEach((listener) => listener(detail))
}

function canAutoRefresh() {
  if (!latest) return true
  return (
    latest.state !== 'dev' &&
    latest.state !== 'checking' &&
    latest.state !== 'downloading' &&
    latest.state !== 'ready'
  )
}

function triggerAutoRefresh(minGapMs: number) {
  if (!canAutoRefresh()) return
  const now = Date.now()
  if (now - lastAutoCheck < minGapMs) return
  lastAutoCheck = now
  ask('update-refresh')
}

function onVisibilityOrFocus() {
  if (document.visibilityState === 'hidden') return
  triggerAutoRefresh(FOCUS_RECHECK_MS)
}

export function useHostUpdate(): HostUpdate | null {
  const [update, setUpdate] = useState<HostUpdate | null>(latest)
  useEffect(() => {
    if (!inLauncher()) return
    if (!listeners.size) {
      window.addEventListener('lgmon-host', receive)
      document.addEventListener('visibilitychange', onVisibilityOrFocus)
      window.addEventListener('focus', onVisibilityOrFocus)
      pollTimer = setInterval(() => triggerAutoRefresh(AUTO_CHECK_MS - 1000), AUTO_CHECK_MS)
    }
    listeners.add(setUpdate)
    if (!asked) {
      asked = true
      lastAutoCheck = Date.now()
      ask('update-check')
    }
    return () => {
      listeners.delete(setUpdate)
      if (!listeners.size) {
        window.removeEventListener('lgmon-host', receive)
        document.removeEventListener('visibilitychange', onVisibilityOrFocus)
        window.removeEventListener('focus', onVisibilityOrFocus)
        if (pollTimer) {
          clearInterval(pollTimer)
          pollTimer = null
        }
        asked = false
        latest = null
      }
    }
  }, [])
  return update
}

/* The card shows up only when there is something to do: a new version, its download, applying, or a failed download/apply. */
const actionable = (update: HostUpdate) =>
  update.state === 'available' ||
  update.state === 'downloading' ||
  update.state === 'ready' ||
  update.state === 'applying' ||
  (update.state === 'error' && (update.during === 'download' || update.during === 'apply'))

/* Automatic background check status lives in the footer, and clicking triggers an immediate manual check. */
export function UpdateLink() {
  const update = useHostUpdate()
  const { t } = useI18n()
  if (!update || update.state === 'dev' || actionable(update)) return null
  const failed = update.state === 'error'
  const label =
    update.state === 'checking'
      ? t('updateChecking')
      : failed
        ? t('updateRetry')
        : update.current
          ? 'v' + update.current + ' · ' + t('updateAutoActive')
          : t('updateAutoActive')
  return (
    <button
      className="lm-start-check"
      data-state={update.state}
      disabled={update.state === 'checking'}
      title={failed ? update.message : t('updateAutoHint')}
      onClick={() => {
        lastAutoCheck = Date.now()
        ask('update-refresh')
      }}
    >
      {label}
    </button>
  )
}
const megabytes = (bytes = 0) => (bytes / 1e6).toFixed(1)

export function UpdateCard() {
  const update = useHostUpdate()
  const { lang, t } = useI18n()
  if (!update || !actionable(update)) return null
  const failedDownload = update.state === 'error' && (update.during === 'download' || update.during === 'apply')
  const hasPage = update.hasPage ?? !!update.notes
  const percent = update.total ? Math.min(100, Math.round(((update.done || 0) / update.total) * 100)) : 0
  return (
    <section
      className="lm-start-update"
      data-state={update.state}
      aria-label={lang === 'en' ? 'Update' : '更新'}
    >
      <span className="lm-start-update-ic" aria-hidden="true">
        <Glyph name={update.state === 'error' ? 'refresh' : 'sparkle'} size={16} />
      </span>
      <div className="lm-start-update-t">
        {update.state === 'available' && (
          <>
            <b>{t('updateAvailable', { version: update.version || '' })}</b>
            <small>
              {hasPage && (
                <button className="lm-start-update-link" onClick={() => ask('update-notes')}>
                  {t('updateViewNotes')}
                </button>
              )}
              {hasPage && update.size ? ' · ' : ''}
              {update.size ? (lang === 'en' ? '~' : '约 ') + megabytes(update.size) + ' MB' : ''}
            </small>
          </>
        )}
        {update.state === 'downloading' && (
          <>
            <b>{t('updateDownloading', { version: update.version || '' })}</b>
            <small>
              {megabytes(update.done)} / {megabytes(update.total)} MB
            </small>
            <span className="lm-start-update-bar" role="progressbar" aria-valuenow={percent}>
              <i style={{ width: percent + '%' }} />
            </span>
          </>
        )}
        {update.state === 'ready' && (
          <>
            <b>{t('updateReadyTitle', { version: update.version || '' })}</b>
            <small>{update.message || t('updateReadyBody')}</small>
          </>
        )}
        {update.state === 'applying' && (
          <>
            <b>{t('updateApplying')}</b>
            <small>{update.version ? 'v' + update.version : ''}</small>
          </>
        )}
        {failedDownload && (
          <>
            <b>{t('updateDownloadFailed')}</b>
            <small>{update.message}</small>
          </>
        )}
      </div>
      {update.state === 'available' && (
        <button className="lm-start-update-btn" onClick={() => ask('update-download')}>
          {t('updateDownloadNow')}
        </button>
      )}
      {update.state === 'ready' && (
        <button className="lm-start-update-btn primary" onClick={() => ask('update-apply')}>
          {t('updateRestartNow')}
        </button>
      )}
      {failedDownload && (
        <button className="lm-start-update-btn" onClick={() => ask('update-download')}>
          {t('aiRetry')}
        </button>
      )}
    </section>
  )
}
