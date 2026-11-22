/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useGate } from './access'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { Row } from './settings-rows'
import { useSystemUpdate } from './WebUpdate'

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

const megabytes = (bytes = 0) => (bytes / 1e6).toFixed(1)

/* The version row in settings: triggers the exact same in-app check -> verified download -> apply flow. */
export function UpdateRow({ version }: { version: string }) {
  const { lang, t } = useI18n()
  const gate = useGate()
  const dev = !version || version === 'dev'
  const { update, actionError, checkNow, downloadUpdate, applyUpdate } = useSystemUpdate(!dev)
  const display = version ? (version.startsWith('v') ? version : 'v' + version) : 'dev'
  const state = update?.state || 'idle'
  const percent =
    update?.total && update.total > 0
      ? Math.min(100, Math.round(((update.done || 0) / update.total) * 100))
      : 0

  let hint = dev ? t('updateDevHint') : `lgmon ${display} · ${t('updateVersionRowHint')}`
  if (!dev) {
    if (state === 'available' && update?.version) {
      hint = `lgmon ${display} · ${t('updateAvailable', { version: 'v' + update.version })}`
    } else if (state === 'downloading') {
      hint = `${t('updateDownloading', { version: update?.version ? 'v' + update.version : '' })} · ${megabytes(update?.done)} / ${megabytes(update?.total)} MB (${percent}%)`
    } else if (state === 'ready' && update?.version) {
      hint = `${t('updateReadyTitle', { version: 'v' + update.version })} · ${t('updateReadyBody')}`
    } else if (state === 'applying') {
      hint = t('updateApplying')
    } else if (state === 'error' || actionError) {
      hint = actionError || update?.message || t('updateFailedCheck')
    }
  }

  return (
    <Row title={lang === 'en' ? 'Version' : '版本'} hint={hint}>
      {!dev && (
        <div style={{ display: 'inline-flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          {state === 'available' && update?.version && (
            <button
              type="button"
              className="lm-btn primary"
              onClick={() => gate('config', t('updateStartDownload'), downloadUpdate)}
            >
              <Glyph name="download" size={14} />
              {t('updateStartDownload')} v{update.version}
            </button>
          )}
          {state === 'ready' && (
            <button
              type="button"
              className="lm-btn primary"
              onClick={() => gate('config', t('updateRestartNow'), applyUpdate)}
            >
              <Glyph name="refresh" size={14} />
              {t('updateRestartNow')}
            </button>
          )}
          {state === 'error' && update?.during === 'download' && (
            <button
              type="button"
              className="lm-btn primary"
              onClick={() => gate('config', t('updateStartDownload'), downloadUpdate)}
            >
              <Glyph name="refresh" size={14} />
              {t('aiRetry')}
            </button>
          )}
          {state !== 'downloading' && state !== 'applying' && state !== 'ready' && (
            <button
              type="button"
              className="set-btn"
              disabled={state === 'checking'}
              onClick={() => gate('config', t('updateCheckNow'), checkNow)}
            >
              <Glyph name="refresh" size={14} />
              {state === 'checking' ? t('updateChecking') : t('updateCheckNow')}
            </button>
          )}
        </div>
      )}
    </Row>
  )
}
