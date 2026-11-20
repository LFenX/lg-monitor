import { useState } from 'react'
import type { ReactNode } from 'react'
import { Glyph } from './glyphs'
import { useRemoteBoard } from './Lan'
import { sharedHref } from './shared'
import { useApp } from './state'
import { Dot } from './status'
import type { Monitor } from './types'
import './side.css'

/*
 * Boards in the sidebar fold open to their services, like lgmon's small window: a glance shows how every service
 * of every board is doing, yours and the ones other people share.
 */

const key = 'lgmon-side-open'

function readOpen(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(key) || '{}') as Record<string, boolean>
  } catch {
    return {}
  }
}

export function useSideFolds() {
  const [open, setOpen] = useState(readOpen)
  const toggle = (id: string) =>
    setOpen((previous) => {
      const next = { ...previous, [id]: !previous[id] }
      try {
        localStorage.setItem(key, JSON.stringify(next))
      } catch {
        // Without storage the fold still works until the page closes.
      }
      return next
    })
  return { open, toggle }
}

const health = (monitor: Monitor) => monitor.overall || monitor.state
const rank = (state: string) => (state === 'down' ? 0 : state === 'up' ? 2 : 1)

function note(monitor: Monitor) {
  const state = health(monitor)
  if (state === 'down') return '异常'
  if (state !== 'up') return '无数据'
  const uptime = monitor.groupUptime ?? monitor.uptime
  return typeof uptime === 'number' ? (uptime >= 99.95 ? '100%' : uptime.toFixed(1) + '%') : ''
}

/* The fold button beside a board's link. */
export function SideFold({
  id,
  name,
  open,
  toggle,
}: {
  id: string
  name: string
  open: boolean
  toggle: (id: string) => void
}) {
  return (
    <button
      className="lm-side-fold"
      aria-expanded={open}
      aria-label={(open ? '收起' : '展开') + name + '的服务'}
      onClick={(event) => {
        event.preventDefault()
        toggle(id)
      }}
    >
      <Glyph name="chevron" size={13} />
    </button>
  )
}

function Rows({
  items,
  all,
  href,
  label,
}: {
  items: Monitor[]
  all: Monitor[]
  href: (id: string) => string
  label: string
}) {
  if (!items.length) return <p className="lm-side-empty">还没有服务</p>
  return (
    <ul className="lm-side-svcs" aria-label={label}>
      {items.map((monitor) => {
        const kids = all.filter((item) => item.parent === monitor.id)
        return (
          <li key={monitor.id} data-state={health(monitor)}>
            <a href={href(monitor.id)} title={monitor.issue || monitor.name}>
              <Dot state={health(monitor)} />
              <span className="name">{monitor.name}</span>
              {kids.length > 0 && (
                <span className="lm-side-kids" aria-label={kids.length + ' 个子服务'}>
                  {/* The ones in trouble come first, so a fold of eight still shows the red one. */}
                  {[...kids]
                    .sort((a, b) => rank(health(a)) - rank(health(b)))
                    .slice(0, kids.length > 6 ? 5 : 6)
                    .map((kid) => (
                      <i key={kid.id} data-state={health(kid)} />
                    ))}
                  {kids.length > 6 && <b>+{kids.length - 5}</b>}
                </span>
              )}
              <span className="note">{note(monitor)}</span>
            </a>
          </li>
        )
      })}
    </ul>
  )
}

/* A board of this computer, folded open: its services from the live list. */
export function OwnServices({ ids, name }: { ids: string[]; name: string }) {
  const { monitors } = useApp()
  const byID = new Map(monitors.map((monitor) => [monitor.id, monitor]))
  const items = ids.map((id) => byID.get(id)).filter((item): item is Monitor => !!item)
  return (
    <Rows
      items={items}
      all={monitors}
      href={(id) => '#/monitors/' + encodeURIComponent(id)}
      label={name + ' 的服务'}
    />
  )
}

/* Someone else's board, folded open: read through this computer only while it is open. */
export function RemoteServices({ id, name }: { id: string; name: string }) {
  const copy = useRemoteBoard(id)
  const board = copy.board?.board
  let body: ReactNode
  if (board) {
    const items = board.items.filter((item) => !item.parent)
    body = (
      <Rows
        items={items}
        all={board.items}
        href={(monitor) => sharedHref(id, monitor)}
        label={name + ' 的服务'}
      />
    )
  } else if (copy.gone || copy.error) body = <p className="lm-side-empty">连不上对方</p>
  else body = <p className="lm-side-empty">正在读取…</p>
  return <>{body}</>
}
