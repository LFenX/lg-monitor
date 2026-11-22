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
  const { update, actionError, handoff, checkNow, downloadUpdate } = useSystemUpdate(!dev)
  const display = version.startsWith('v') ? version : 'v' + version
  const available = update?.state === 'available'
  const action = available ? t('updateInWindow') : t('updateCheckInWindow')
  let hint = dev ? t('updateDevHint') : 'lgmon ' + display
  if (available && update.version) hint += ' · ' + t('updateAvailable', { version: 'v' + update.version })
  if (handoff === 'sent') hint = t('updateWindowSent')
  return (
    <Row title={lang === 'en' ? 'Version' : '版本'} hint={actionError || hint}>
      {!dev && (
        <button type="button" className="set-btn" disabled={handoff === 'pending'}
          onClick={() => gate('config', action, available ? downloadUpdate : checkNow)}>
          <Glyph name={available ? 'download' : 'refresh'} size={14} />
          {action}
        </button>
      )}
    </Row>
  )
}
