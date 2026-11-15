import { useEffect, useId, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import type { Beat, Health, StatusSegment, StatusSlot } from './types'

export const healthText: Record<string, string> = { up: '正常', down: '异常', unknown: '等待', '': '无数据' }

export function percentText(value: number | null | undefined) {
  if (value == null) return '—'
  if (value >= 99.995) return '100%'
  return value.toFixed(value >= 99 ? 2 : 1) + '%'
}

export function latencyText(value: number | null | undefined) {
  if (value == null) return '—'
  if (value < 1) return '<1 ms'
  return value >= 1000 ? (value / 1000).toFixed(2) + ' s' : Math.round(value) + ' ms'
}

export function clock(ts: number) {
  return new Date(ts).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
}

export function dayText(ts: number) {
  const date = new Date(ts)
  return date.getMonth() + 1 + '月' + date.getDate() + '日'
}

export function moment(ts: number, now: number) {
  const date = new Date(ts)
  const today = new Date(now)
  const sameDay = date.toDateString() === today.toDateString()
  return sameDay ? clock(ts) : dayText(ts) + ' ' + clock(ts)
}

/* Abnormal first, then waiting, then healthy; ties keep the order the board was arranged in. */
export function severity(state?: string) {
  return state === 'down' ? 0 : state === 'unknown' ? 1 : 2
}

export function Dot({ state, label }: { state: Health | Beat | 'empty' | undefined; label?: string }) {
  return (
    <span
      className="lm-dot"
      data-state={state || 'none'}
      role={label ? 'img' : undefined}
      aria-label={label}
    />
  )
}

export function StateBadge({ state, children }: { state: Health | undefined; children?: string }) {
  return (
    <span className="lm-badge" data-state={state || 'unknown'}>
      {children || healthText[state || 'unknown']}
    </span>
  )
}

/* Like the classic uptime list: the pill's colour is the service's state now, its text the last 24 hours. */
export function UptimePill({ value, state }: { value: number | null | undefined; state?: Health }) {
  const tone = state || (value == null ? 'none' : value >= 99.9 ? 'up' : value >= 95 ? 'unknown' : 'down')
  return (
    <span
      className="lm-pill"
      data-state={tone}
      title={'现在' + healthText[state || ''] + '，最近 24 小时可用率 ' + percentText(value)}
    >
      {value == null ? healthText[state || ''] : percentText(value)}
    </span>
  )
}

export type BeatSlot = {
  state: Beat
  from: number
  span: number
  uptime?: number | null
  segments?: StatusSegment[]
}

function segmentTitle(segment: StatusSegment) {
  const time = (ts: number) =>
    new Date(ts).toLocaleTimeString('zh-CN', {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    })
  const duration = segment.to - segment.from
  return (
    dayText(segment.from) +
    ' ' +
    time(segment.from) +
    '–' +
    time(segment.to) +
    ' ' +
    healthText[segment.state] +
    (duration ? ' · ' + (duration / 1000).toFixed(3).replace(/\.?0+$/, '') + ' 秒' : ' · 观测时点')
  )
}

/* Kuma-style heartbeat bar: rounded pills, newest on the right. Only as many as fit are drawn,
   so a narrow column shows the most recent hours instead of squeezing every pill. */
export function Beats({
  slots,
  size = 'row',
  label,
  liveState,
}: {
  slots: BeatSlot[]
  size?: 'row' | 'large' | 'day'
  label?: string
  liveState?: Health
}) {
  return (
    <div className="lm-beats" data-size={size} role="img" aria-label={label || '状态历史'}>
      {slots.map((slot, slotIndex) => {
        const end = slot.from + slot.span
        const range = size === 'day' ? dayText(slot.from) : clock(slot.from) + '–' + clock(end)
        // Worst state records an incident; known availability says how much of the bucket was healthy.
        const uptime =
          (slot.state === 'up' || slot.state === 'down') &&
          slot.uptime != null &&
          Number.isFinite(slot.uptime)
            ? Math.max(0, Math.min(100, slot.uptime))
            : null
        const text =
          uptime == null
            ? healthText[slot.state]
            : '正常 ' + percentText(uptime) + ' · 异常 ' + percentText(100 - uptime)
        const timed = !!slot.segments?.length
        const live = liveState === 'down' && slotIndex === slots.length - 1
        return (
          <i
            key={slot.from}
            data-state={live ? 'down' : slot.state || 'none'}
            data-live={live || undefined}
            data-history-state={slot.state || 'none'}
            data-uptime={uptime ?? undefined}
            data-timed={timed || undefined}
            style={uptime == null ? undefined : ({ '--beat-up': uptime + '%' } as CSSProperties)}
            title={
              (live ? '当前异常；' : '') +
              range +
              ' ' +
              text +
              (uptime != null && !timed ? ' · 汇总占比，未保留精确时点' : '')
            }
          >
            {slot.segments?.map((segment, index) => (
              <b
                key={index}
                className="lm-beat-segment"
                data-state={segment.state}
                data-from={segment.from}
                data-to={segment.to}
                title={segmentTitle(segment)}
                style={
                  {
                    '--segment-left':
                      Math.max(0, Math.min(100, ((segment.from - slot.from) * 100) / slot.span)) + '%',
                    '--segment-width':
                      Math.max(0, Math.min(100, ((segment.to - segment.from) * 100) / slot.span)) + '%',
                  } as CSSProperties
                }
              />
            ))}
          </i>
        )
      })}
    </div>
  )
}

/* The board rows carry 48 half-hour beats ending now; this places them on the clock. */
export function rowSlots(beats: Beat[], now: number, slots?: StatusSlot[]): BeatSlot[] {
  const span = 30 * 60 * 1000
  if (slots?.length) return slots.map((slot) => ({ ...slot, span }))
  return beats.map((state, index) => ({ state, from: now - (beats.length - index) * span, span }))
}

export function LatencyChart({
  points,
  from,
  to,
}: {
  points: { ts: number; ms: number }[]
  from: number
  to: number
}) {
  const id = useId().replace(/[^a-zA-Z0-9]/g, '')
  const box = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(640)
  const [hover, setHover] = useState<number | null>(null)
  useEffect(() => {
    const element = box.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => setWidth(Math.max(240, element.clientWidth)))
    observer.observe(element)
    setWidth(Math.max(240, element.clientWidth))
    return () => observer.disconnect()
  }, [])
  const height = 168
  const pad = { top: 12, bottom: 22, left: 4, right: 4 }
  if (!points.length)
    return (
      <div className="lm-chart-empty" ref={box}>
        这个服务没有网络探测，所以没有响应时间。
      </div>
    )
  const top = Math.max(1, ...points.map((point) => point.ms)) * 1.15
  const x = (ts: number) => pad.left + ((ts - from) / Math.max(1, to - from)) * (width - pad.left - pad.right)
  const y = (ms: number) => pad.top + (1 - ms / top) * (height - pad.top - pad.bottom)
  const line = points
    .map((point, index) => (index ? 'L' : 'M') + x(point.ts).toFixed(1) + ' ' + y(point.ms).toFixed(1))
    .join(' ')
  const area =
    line +
    ' L' +
    x(points[points.length - 1].ts).toFixed(1) +
    ' ' +
    (height - pad.bottom) +
    ' L' +
    x(points[0].ts).toFixed(1) +
    ' ' +
    (height - pad.bottom) +
    ' Z'
  const active = hover == null ? null : points[hover]
  return (
    <div className="lm-chart" ref={box}>
      <svg
        width={width}
        height={height}
        role="img"
        aria-label="最近 24 小时响应时间"
        onMouseMove={(event) => {
          const rect = event.currentTarget.getBoundingClientRect()
          const ts =
            from + ((event.clientX - rect.left - pad.left) / (width - pad.left - pad.right)) * (to - from)
          let best = 0
          points.forEach((point, index) => {
            if (Math.abs(point.ts - ts) < Math.abs(points[best].ts - ts)) best = index
          })
          setHover(best)
        }}
        onMouseLeave={() => setHover(null)}
      >
        <defs>
          <linearGradient id={'fill' + id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="var(--up)" stopOpacity="0.28" />
            <stop offset="1" stopColor="var(--up)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {[0.25, 0.5, 0.75].map((ratio) => (
          <line
            key={ratio}
            x1={pad.left}
            x2={width - pad.right}
            y1={pad.top + ratio * (height - pad.top - pad.bottom)}
            y2={pad.top + ratio * (height - pad.top - pad.bottom)}
            className="grid"
          />
        ))}
        <path d={area} fill={'url(#fill' + id + ')'} />
        <path d={line} className="line" fill="none" />
        {active && (
          <g>
            <line
              x1={x(active.ts)}
              x2={x(active.ts)}
              y1={pad.top}
              y2={height - pad.bottom}
              className="cursor"
            />
            <circle cx={x(active.ts)} cy={y(active.ms)} r={3.5} className="point" />
          </g>
        )}
        <text x={pad.left} y={height - 6} className="axis">
          24 小时前
        </text>
        <text x={width - pad.right} y={height - 6} className="axis" textAnchor="end">
          现在
        </text>
        <text x={width - pad.right} y={pad.top + 10} className="axis" textAnchor="end">
          {latencyText(top / 1.15)}
        </text>
      </svg>
      {active && (
        <div className="lm-chart-tip" style={{ left: Math.min(width - 120, Math.max(0, x(active.ts) - 60)) }}>
          {clock(active.ts)} · {latencyText(active.ms)}
        </div>
      )}
    </div>
  )
}
