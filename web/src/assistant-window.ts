import { useCallback, useEffect, useRef, useState } from 'react'
import type { PointerEvent as ReactPointerEvent } from 'react'

/*
 * Where 波西米亚's panel sits and how big it is, as in the owner's 留白 (margin.lfen.ink). It opens docked at the
 * bottom right; dragging the header moves it, any edge or corner resizes it, and the expand button (or a double
 * click on the header) makes it tall and wide for long answers, then returns to the small size. A placed frame is
 * remembered in this browser and always kept inside the window. On a narrow window it stays a docked sheet.
 */

export type Frame = { x: number; y: number; w: number; h: number }
export type Edge = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw'
export const edges: Edge[] = ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw']
// `expanded` is the last button choice; dragging only changes `custom`, never the presets.
type Saved = { custom: Frame | null; expanded: boolean }

const key = 'lgmon-ai-frame'
const gap = 12
export const minWidth = 360
export const minHeight = 420

function valid(value: unknown): Frame | null {
  const frame = value as Frame | null
  return frame && [frame.x, frame.y, frame.w, frame.h].every((n) => Number.isFinite(n)) ? frame : null
}

function read(): Saved {
  try {
    const value = JSON.parse(localStorage.getItem(key) || 'null')
    if (value && typeof value === 'object') return { custom: valid(value.custom), expanded: !!value.expanded }
  } catch {
    // A private window or a bad value: start docked.
  }
  return { custom: null, expanded: false }
}

function write(saved: Saved) {
  try {
    localStorage.setItem(key, JSON.stringify(saved))
  } catch {
    // Not kept; it still works for this visit.
  }
}

export function docked(): Frame {
  const w = Math.min(440, innerWidth - 2 * gap),
    h = Math.min(740, innerHeight - 92)
  return { x: innerWidth - w - 20, y: innerHeight - h - 20, w, h }
}

function expandedFrame(): Frame {
  const w = Math.min(960, innerWidth - 48),
    h = innerHeight - 48
  return { x: innerWidth - w - 24, y: 24, w, h }
}

/* Kept inside the window and no smaller than the minimum, unless the window itself is smaller. */
export function clamp(frame: Frame): Frame {
  const w = Math.max(Math.min(minWidth, innerWidth - 2 * gap), Math.min(frame.w, innerWidth - 2 * gap))
  const h = Math.max(Math.min(minHeight, innerHeight - 2 * gap), Math.min(frame.h, innerHeight - 2 * gap))
  const x = Math.min(Math.max(gap, frame.x), innerWidth - w - gap)
  const y = Math.min(Math.max(gap, frame.y), innerHeight - h - gap)
  return { x: Math.round(x), y: Math.round(y), w: Math.round(w), h: Math.round(h) }
}

export function useFrame() {
  const [saved, setSaved] = useState<Saved>(read)
  const [, setTick] = useState(0)
  const [moving, setMoving] = useState<null | 'move' | Edge>(null)
  const live = useRef(saved)
  useEffect(() => {
    live.current = saved
  })
  const keep = useCallback((next: Saved) => {
    live.current = next
    setSaved(next)
    write(next)
  }, [])
  // The window changed size: a docked panel follows by itself; a placed one is kept inside.
  useEffect(() => {
    const resize = () => {
      const current = live.current
      if (current.custom) setSaved({ ...current, custom: clamp(current.custom) })
      setTick((n) => n + 1)
    }
    addEventListener('resize', resize)
    return () => removeEventListener('resize', resize)
  }, [])
  const desk = typeof window !== 'undefined' && innerWidth >= 720
  const frame = !desk
    ? null
    : saved.custom
      ? clamp(saved.custom)
      : saved.expanded
        ? clamp(expandedFrame())
        : null

  /* A move (from the header) or a resize (from an edge), following the pointer until it is let go. */
  const begin = useCallback(
    (event: ReactPointerEvent, mode: 'move' | Edge) => {
      if (event.button !== 0 || innerWidth < 720) return
      const current = live.current
      const start = current.custom
        ? clamp(current.custom)
        : current.expanded
          ? clamp(expandedFrame())
          : docked()
      const from = { x: event.clientX, y: event.clientY }
      const target = event.currentTarget as HTMLElement
      target.setPointerCapture?.(event.pointerId)
      event.preventDefault()
      setMoving(mode)
      document.documentElement.classList.add('ai-dragging')
      let next = start
      const move = (pointer: PointerEvent) => {
        const dx = pointer.clientX - from.x,
          dy = pointer.clientY - from.y
        if (mode === 'move') next = clamp({ ...start, x: start.x + dx, y: start.y + dy })
        else {
          let { x, y, w, h } = start
          if (mode.includes('e')) w = start.w + dx
          if (mode.includes('s')) h = start.h + dy
          if (mode.includes('w')) {
            w = start.w - dx
            x = start.x + dx
          }
          if (mode.includes('n')) {
            h = start.h - dy
            y = start.y + dy
          }
          // Shrinking past the minimum from the left or top stops the edge rather than pushing the panel.
          if (w < minWidth) {
            if (mode.includes('w')) x -= minWidth - w
            w = minWidth
          }
          if (h < minHeight) {
            if (mode.includes('n')) y -= minHeight - h
            h = minHeight
          }
          if (x < gap) {
            w -= gap - x
            x = gap
          }
          if (y < gap) {
            h -= gap - y
            y = gap
          }
          next = clamp({ x, y, w: Math.min(w, innerWidth - x - gap), h: Math.min(h, innerHeight - y - gap) })
        }
        setSaved((value) => ({ ...value, custom: next }))
      }
      const up = () => {
        target.removeEventListener('pointermove', move)
        target.removeEventListener('pointerup', up)
        target.removeEventListener('pointercancel', up)
        document.documentElement.classList.remove('ai-dragging')
        setMoving(null)
        // A click on the header that did not move leaves the panel as it was.
        if (next !== start) keep({ ...live.current, custom: next })
      }
      target.addEventListener('pointermove', move)
      target.addEventListener('pointerup', up)
      target.addEventListener('pointercancel', up)
    },
    [keep],
  )
  const toggle = useCallback(() => keep({ custom: null, expanded: !live.current.expanded }), [keep])
  const reset = useCallback(() => keep({ custom: null, expanded: false }), [keep])
  return { frame, desk, expanded: saved.expanded && !saved.custom, moving, begin, toggle, reset }
}
