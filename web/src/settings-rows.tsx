/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import type { ReactNode } from 'react'
import { Glyph } from './glyphs'

/* A group of settings: an optional small heading over one rounded card of rows. */
export function Group({ title, note, children }: { title?: string; note?: ReactNode; children: ReactNode }) {
  return (
    <section className="set-group" aria-label={title}>
      {title && <h3>{title}</h3>}
      <div className="set-card">{children}</div>
      {note && <p className="set-note">{note}</p>}
    </section>
  )
}

/* One setting: what it is on the left, its control on the right. A row with href is a link to somewhere else. */
export function Row({
  title,
  hint,
  href,
  external,
  children,
}: {
  title: ReactNode
  hint?: ReactNode
  href?: string
  external?: boolean
  children?: ReactNode
}) {
  const text = (
    <span className="set-row-t">
      <b>{title}</b>
      {hint && <small>{hint}</small>}
    </span>
  )
  if (href)
    return (
      <a
        className="set-row set-link"
        href={href}
        target={external ? '_blank' : undefined}
        rel={external ? 'noreferrer' : undefined}
      >
        {text}
        {children && <span className="set-row-c">{children}</span>}
        <Glyph name={external ? 'external' : 'chevron'} size={15} />
      </a>
    )
  return (
    <div className="set-row">
      {text}
      {children && <div className="set-row-c">{children}</div>}
    </div>
  )
}
