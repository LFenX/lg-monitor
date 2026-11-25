/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useState } from 'react'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { useApp } from './state'

const key = 'lgmon-port-notice'

function dismissed() {
  try {
    return sessionStorage.getItem(key) || ''
  } catch {
    return ''
  }
}

/* Windows reserved some of lgmon's ports this time: which ones it uses instead, and how to get them back now. */
export function PortNotice() {
  const { guide, guest } = useApp()
  const { t } = useI18n()
  const [closed, setClosed] = useState(dismissed)
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const ports = guide?.ports
  if (guest || !ports?.moved?.length) return null
  const moved = ports.moved.map((item) => `${item.configured} → ${item.actual}`).join('、')
  if (closed === moved) return null
  const fix = ports.fix.join('\n')
  return (
    <div className="lm-note" data-tone="accent" role="status" style={{ flexWrap: 'wrap' }}>
      <Glyph name="plug" size={16} />
      <span style={{ flex: '1 1 320px' }}>{t('portNotice', { moved })}</span>
      <button type="button" className="lm-btn small" aria-expanded={open} onClick={() => setOpen(!open)}>
        {t('portNoticeFix')}
      </button>
      <button
        type="button"
        className="lm-btn small ghost"
        onClick={() => {
          try {
            sessionStorage.setItem(key, moved)
          } catch {
            // Without storage the notice is gone until the page reloads.
          }
          setClosed(moved)
        }}
      >
        {t('portNoticeClose')}
      </button>
      {open && (
        <div style={{ flex: '1 1 100%' }}>
          <p className="lm-small" style={{ margin: '4px 0 6px' }}>
            {t('portNoticeFixBody')}
          </p>
          <pre style={{ margin: 0, overflowX: 'auto' }}>{fix}</pre>
          <button
            type="button"
            className="lm-btn small"
            style={{ marginTop: 6 }}
            onClick={() => {
              void navigator.clipboard
                ?.writeText(fix)
                .then(() => setCopied(true))
                .catch(() => undefined)
            }}
          >
            {copied ? t('portNoticeCopied') : t('portNoticeCopy')}
          </button>
        </div>
      )}
    </div>
  )
}
