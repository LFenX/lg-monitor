/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useRef, useState } from 'react'
import type { HTMLAttributes, KeyboardEvent, PointerEvent as ReactPointerEvent, ReactNode } from 'react'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'

/*
 * Reordering the device cards: drag a card by its header or grip, or move it with the arrow keys on the grip.
 * The cards stay where they are while dragging and a line shows the landing place, so cards of very different
 * heights cannot flicker between two positions.
 */

type Drop = { id: string; side: 'before' | 'after' }

export type CardSort = {
  card: HTMLAttributes<HTMLElement> & Record<`data-${string}`, string | undefined>
  head: Pick<HTMLAttributes<HTMLElement>, 'onPointerDown' | 'onClickCapture'>
  grip: ReactNode
}

const startAfter = 6
const edge = 44

export function reorder(order: string[], id: string, drop: Drop): string[] {
  if (drop.id === id) return order
  const rest = order.filter((item) => item !== id)
  const at = rest.indexOf(drop.id)
  if (at < 0) return order
  rest.splice(drop.side === 'before' ? at : at + 1, 0, id)
  return rest
}

const same = (a: string[], b: string[]) => a.length === b.length && a.every((id, index) => id === b[index])

export function useDeviceOrder(ids: string[], save?: (order: string[]) => Promise<unknown>) {
  const { t } = useI18n()
  const list = useRef<HTMLDivElement>(null)
  // What was just asked for, until the list read back from lgmon says the same.
  const [asked, setAsked] = useState<string[] | null>(null)
  const [dragging, setDragging] = useState('')
  const [drop, setDrop] = useState<Drop | null>(null)
  const stop = useRef<(() => void) | null>(null)
  const suppressClick = useRef(false)

  const known = asked && ids.every((id) => asked.includes(id)) ? asked.filter((id) => ids.includes(id)) : null
  const order = known || ids
  const idsKey = ids.join('\n')
  useEffect(() => {
    setAsked((value) => (value && same(value, idsKey.split('\n')) ? null : value))
  }, [idsKey])
  useEffect(() => () => stop.current?.(), [])

  const commit = (next: string[]) => {
    if (!save || same(next, order)) return
    setAsked(next)
    save(next).catch(() => setAsked(null))
  }

  const begin = (event: ReactPointerEvent, id: string, label: string) => {
    if (!save || event.button !== 0 || order.length < 2 || !label) return
    const origin = { x: event.clientX, y: event.clientY }
    let y = origin.y
    let active = false
    let landing: Drop | null = null
    const scroller = list.current?.closest<HTMLElement>('.lm-side-scroll') || null
    let timer = 0

    const aim = () => {
      const cards = [...(list.current?.querySelectorAll<HTMLElement>('[data-device]') || [])]
      const before = cards.find((card) => {
        const box = card.getBoundingClientRect()
        return y < box.top + box.height / 2
      })
      const last = cards[cards.length - 1]
      landing = before
        ? { id: before.dataset.device || '', side: 'before' }
        : last
          ? { id: last.dataset.device || '', side: 'after' }
          : null
      setDrop(landing && !same(reorder(order, id, landing), order) ? landing : null)
    }
    const scroll = () => {
      if (!scroller) return
      const box = scroller.getBoundingClientRect()
      const step = y < box.top + edge ? -9 : y > box.bottom - edge ? 9 : 0
      if (!step) return
      scroller.scrollTop += step
      aim()
    }
    const move = (pointer: PointerEvent) => {
      y = pointer.clientY
      if (!active) {
        if (Math.hypot(pointer.clientX - origin.x, y - origin.y) < startAfter) return
        active = true
        setDragging(id)
        document.documentElement.classList.add('lm-sorting')
        timer = window.setInterval(scroll, 16)
      }
      pointer.preventDefault()
      aim()
    }
    const finish = (apply: boolean) => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', cancel)
      window.removeEventListener('keydown', key, true)
      window.clearInterval(timer)
      document.documentElement.classList.remove('lm-sorting')
      stop.current = null
      if (active) {
        // The release of a drag must not also fold the card.
        suppressClick.current = true
        window.setTimeout(() => (suppressClick.current = false), 0)
        setDragging('')
        setDrop(null)
        if (apply && landing) commit(reorder(order, id, landing))
      }
    }
    const up = () => finish(true)
    const cancel = () => finish(false)
    const key = (pressed: globalThis.KeyboardEvent) => {
      if (pressed.key !== 'Escape' || !active) return
      pressed.preventDefault()
      pressed.stopPropagation()
      finish(false)
    }
    stop.current?.()
    stop.current = cancel
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('keydown', key, true)
  }

  const nudge = (event: KeyboardEvent, id: string) => {
    const step = event.key === 'ArrowUp' ? -1 : event.key === 'ArrowDown' ? 1 : 0
    if (!step) return
    event.preventDefault()
    const target = order[order.indexOf(id) + step]
    if (target) commit(reorder(order, id, { id: target, side: step < 0 ? 'before' : 'after' }))
  }

  const sort = (id: string, name: string): CardSort => {
    const enabled = !!save && order.length > 1
    const label = t('sideDeviceMove', { name })
    return {
      card: {
        'data-device': id,
        'data-dragging': dragging === id ? '' : undefined,
        'data-drop': drop?.id === id ? drop.side : undefined,
      },
      head: enabled
        ? {
            // A finger on the header scrolls the sidebar; touch drags by the grip.
            onPointerDown: (event) => event.pointerType !== 'touch' && begin(event, id, label),
            onClickCapture: (event) => {
              if (!suppressClick.current) return
              event.preventDefault()
              event.stopPropagation()
            },
          }
        : {},
      grip: enabled ? (
        <button
          type="button"
          className="lm-dev-grip"
          aria-label={label}
          title={t('sideDeviceMoveHint')}
          onPointerDown={(event) => {
            event.stopPropagation()
            begin(event, id, label)
          }}
          onKeyDown={(event) => nudge(event, id)}
        >
          <Glyph name="grip" size={12} />
        </button>
      ) : null,
    }
  }

  return { list, order, sort }
}
