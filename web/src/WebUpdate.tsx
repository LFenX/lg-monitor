/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useState } from 'react'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { inLauncher } from './launcher'
import { useApp } from './state'
import type { GitHubStats } from './types'

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

export type WebUpdateState = {
  checking: boolean
  latestVersion: string
  hasUpdate: boolean
  checkedAt: number
  checkNow: () => void
}

export function useWebUpdate(currentVersion?: string, enabled = true): WebUpdateState {
  const [checking, setChecking] = useState(false)
  const [latestVersion, setLatestVersion] = useState('')
  const [checkedAt, setCheckedAt] = useState(0)

  const checkNow = useCallback(() => {
    if (!enabled) return
    setChecking(true)
    void fetch('/api/v1/download/stats', { cache: 'no-store' })
      .then((res) => (res.ok ? (res.json() as Promise<GitHubStats>) : null))
      .then((stats) => {
        if (stats?.version) setLatestVersion(stats.version)
        setCheckedAt(Date.now())
      })
      .catch(() => undefined)
      .finally(() => setChecking(false))
  }, [enabled])

  useEffect(() => {
    if (!enabled) return
    let lastChecked = 0
    const run = (force: boolean) => {
      const now = Date.now()
      if (!force && now - lastChecked < WEB_FOCUS_RECHECK_MS) return
      lastChecked = now
      checkNow()
    }
    run(true)
    const timer = setInterval(() => run(true), WEB_AUTO_CHECK_MS)
    const onVisible = () => {
      if (document.visibilityState === 'visible') run(false)
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [enabled, checkNow])

  const hasUpdate = isNewerVersion(latestVersion, currentVersion)
  return { checking, latestVersion, hasUpdate, checkedAt, checkNow }
}

/*
 * Shown in the full browser dashboard sidebar footer so users who leave the web UI open
 * automatically see new releases without needing to click manual check or reopen the launcher.
 */
export function WebUpdatePill() {
  const { guide, guest } = useApp()
  const { t } = useI18n()
  const version = guide?.version || ''
  const dev = !version || version === 'dev'
  const enabled = !guest && !inLauncher() && !dev
  const { checking, latestVersion, hasUpdate, checkNow } = useWebUpdate(version, enabled)

  if (guest || inLauncher() || dev) return null

  if (hasUpdate && latestVersion) {
    const href = `/api/v1/download/latest?channel=web-update&v=${encodeURIComponent(latestVersion)}`
    return (
      <a className="lm-web-update is-available" href={href} download title={t('updateAutoHint')}>
        <span className="lm-web-update-dot" aria-hidden="true" />
        <span className="lm-web-update-text">{t('updateAvailable', { version: latestVersion })}</span>
        <span className="lm-web-update-cta">
          <Glyph name="download" size={12} />
          {t('updateDownloadNow')}
        </span>
      </a>
    )
  }

  return (
    <button
      type="button"
      className="lm-web-update"
      disabled={checking}
      onClick={checkNow}
      title={t('updateAutoHint')}
    >
      <span className="lm-web-update-text">
        {checking
          ? t('updateChecking')
          : `${version.startsWith('v') ? version : 'v' + version} · ${t('updateUpToDate')}`}
      </span>
      <Glyph name="refresh" size={12} />
    </button>
  )
}
