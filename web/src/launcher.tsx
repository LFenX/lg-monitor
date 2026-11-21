/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { Row } from './settings-rows'
import { useWebUpdate } from './WebUpdate'

/*
 * Whether this page runs inside lgmon's own window. Kept apart from the update code so a browser, and the
 * settings page, never load it.
 */
export type Host = {
  chrome?: { webview?: { postMessage: (message: string) => void } }
  lgmonLauncher?: boolean
}

export const inLauncher = () => {
  const host = window as unknown as Host
  return !!host.lgmonLauncher && !!host.chrome?.webview
}

/* The version row in settings: automatically checks for newer GitHub releases every 20 minutes. */
export function UpdateRow({ version }: { version: string }) {
  const { lang, t } = useI18n()
  const dev = !version || version === 'dev'
  const { checking, latestVersion, hasUpdate, checkNow } = useWebUpdate(version, !dev)
  const display = version ? (version.startsWith('v') ? version : 'v' + version) : 'dev'
  const hint = dev
    ? t('updateDevHint')
    : hasUpdate && latestVersion
      ? `lgmon ${display} · ${t('updateAvailable', { version: latestVersion })}`
      : `lgmon ${display} · ${t('updateVersionRowHint')}`
  return (
    <Row title={lang === 'en' ? 'Version' : '版本'} hint={hint}>
      {!dev && (
        <div style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
          {hasUpdate && latestVersion && (
            <a
              className="lm-btn primary"
              href={`/api/v1/download/latest?v=${encodeURIComponent(latestVersion)}`}
              download
            >
              <Glyph name="download" size={14} />
              {t('updateDownloadNow')} {latestVersion}
            </a>
          )}
          <button type="button" className="set-btn" disabled={checking} onClick={checkNow}>
            <Glyph name="refresh" size={14} />
            {checking ? t('updateChecking') : t('updateCheckNow')}
          </button>
        </div>
      )}
    </Row>
  )
}
