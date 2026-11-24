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

export function UpdateRow({ version }: { version: string }) {
  const { lang, t } = useI18n()
  const gate = useGate()
  const dev = !version || version === 'dev'
  const { update, actionError, handoff, checkNow, applyUpdate } = useSystemUpdate(!dev)
  const display = version.startsWith('v') ? version : 'v' + version
  const latest = update?.version ? (update.version.startsWith('v') ? update.version : 'v' + update.version) : ''
  const available = update?.state === 'available' && !!latest
  const checking = update?.state === 'checking'
  const action = available ? t('updateDownloadNow') : t('updateCheckNow')
  let hint = dev ? t('updateDevHint') : 'lgmon ' + display
  if (checking) hint += ' · ' + t('updateChecking')
  else if (available) hint += ' → ' + latest
  else if (update?.state === 'none') hint += ' · ' + t('updateUpToDate')
  if (handoff === 'sent') hint = t('updateWindowSent')
  return (
    <Row title={lang === 'en' ? 'Version' : '版本'} hint={actionError || hint}>
      {!dev && (
        <button
          type="button"
          className="set-btn"
          disabled={handoff === 'pending' || checking}
          onClick={() => gate('config', action, available ? applyUpdate : checkNow)}
        >
          <Glyph name={available ? 'download' : 'refresh'} size={14} />
          {action}
        </button>
      )}
    </Row>
  )
}
