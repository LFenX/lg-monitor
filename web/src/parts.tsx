/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago, useCopy } from './kit'
import { useApp } from './state'
import { agentSetup, quote } from './prompts'

export function Dialog({
  title,
  close,
  children,
  footer,
  action,
  wide = false,
  full = false,
}: {
  title: string
  close: () => void
  children: ReactNode
  footer?: ReactNode
  action?: ReactNode
  wide?: boolean
  /* Nearly the whole window, for writing at length: the body becomes a column that fills the height. */
  full?: boolean
}) {
  const { t } = useI18n()
  const box = useRef<HTMLDivElement>(null)
  // Callers pass a fresh close on every render; keyed on it, the focus below ran on each keystroke and live
  // refresh and pulled the caret back to the first field. The latest close lives in a ref instead.
  const closing = useRef(close)
  useEffect(() => {
    closing.current = close
  })
  useEffect(() => {
    const key = (event: KeyboardEvent) => event.key === 'Escape' && closing.current()
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [])
  useEffect(() => {
    if (box.current?.contains(document.activeElement)) return
    const first = box.current?.querySelector<HTMLElement>(
      'input, select, textarea, button.lm-kind, button.primary',
    )
    first?.focus({ preventScroll: true })
  }, [])
  return createPortal(
    <div className="lm-overlay" onMouseDown={(event) => event.target === event.currentTarget && close()}>
      <div
        className={'lm-dialog' + (wide ? ' wide' : '') + (full ? ' full' : '')}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        ref={box}
      >
        <header>
          <h2>{title}</h2>
          {action && <div className="lm-dialog-action">{action}</div>}
          <button className="lm-btn ghost icon" onClick={close} aria-label={t('close')}>
            <Glyph name="close" />
          </button>
        </header>
        <div className="body">{children}</div>
        {footer && <footer>{footer}</footer>}
      </div>
    </div>,
    document.body,
  )
}

export function Menu({ children, label }: { children: ReactNode; label?: string }) {
  const [open, setOpen] = useState(false)
  const { t } = useI18n()
  const resolvedLabel = label ?? t('moreActions')
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => {
      if (!box.current?.contains(event.target as Node)) setOpen(false)
    }
    const key = (event: KeyboardEvent) => event.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', outside)
    window.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', outside)
      window.removeEventListener('keydown', key)
    }
  }, [open])
  return (
    <div className="lm-menu" ref={box}>
      <button
        className="lm-btn icon"
        aria-label={resolvedLabel}
        title={resolvedLabel}
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <Glyph name="more" />
      </button>
      {open && (
        <div className="lm-menu-list" role="menu" onClick={() => setOpen(false)}>
          {children}
        </div>
      )}
    </div>
  )
}

export function CopyButton({
  text,
  label,
  primary = false,
}: {
  text: string
  label?: string
  primary?: boolean
}) {
  const { t } = useI18n()
  const { copied, copy } = useCopy()
  const resolvedLabel = label ?? t('copy')
  return (
    <button className={'lm-btn small' + (primary ? ' primary' : '')} onClick={() => copy(text)}>
      <Glyph name={copied ? 'check' : 'copy'} size={15} />
      {copied ? t('copied') : resolvedLabel}
    </button>
  )
}

export {
  agentSetup,
  alertPrompt,
  boardPrompt,
  changePrompt,
  childPrompt,
  dependencyPrompt,
  diagnosePrompt,
  docPrompt,
  docsPrompt,
  editServicesPrompt,
  feishuPrompt,
  firstPrompt,
  lanPrompt,
  promptChoices,
  quote,
  skillText,
  startPrompt,
} from './prompts'

export function useAgentSetup(tools: 'essential' | 'all' = 'all') {
  const { lang } = useI18n()
  return agentSetup(useApp().guide?.install, tools, lang)
}

/* How an agent gets the MCP tools; the command uses this install's real program and data paths. */
export function ConnectSteps({ compact = false, last }: { compact?: boolean; last?: string }) {
  const { guide, now } = useApp()
  const { t } = useI18n()
  if (!guide) return null
  const seen = guide.state.agentSeenAt
  const resolvedLast = last ?? t('connectLastDefault')
  const command =
    '& ' + quote(guide.install.executable) + ' agent connect --home ' + quote(guide.install.home)
  if (seen && compact)
    return <p className="lm-muted lm-small">{t('connectSeenCompact', { ago: ago(seen, now()) })}</p>
  return (
    <details className="lm-steps-wrap">
      <summary>{t('manualSetupOptional')}</summary>
      <ol className="lm-steps">
        <li>
          <div>
            <span>{t('connectRunPs', { admin: guide.install.personal ? '' : t('connectAdminNote') })}</span>
            <div className="lm-code">
              <code>{command}</code>
              <CopyButton text={command} />
            </div>
          </div>
        </li>
        <li>
          <div>{t('connectGiveConfig')}</div>
        </li>
        <li>
          <div>{resolvedLast}</div>
        </li>
      </ol>
      {seen ? <p className="lm-muted lm-small">{t('connectSeenBefore', { ago: ago(seen, now()) })}</p> : null}
    </details>
  )
}
