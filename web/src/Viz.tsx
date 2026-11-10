import { useId } from 'react'
import { display, level, parts } from './format'
import type { Unit } from './format'

const palette = ['var(--c1)', 'var(--c2)', 'var(--c3)', 'var(--c4)', 'var(--c5)', 'var(--muted)']

type Limits = { unit?: Unit; warn?: number | null; critical?: number | null }

export function Stat({ value, unit, warn, critical }: { value: unknown } & Limits) {
  const [figure, suffix] = parts(value, unit)
  return (
    <div className="viz-stat dashboard-stat" data-level={level(value, warn, critical)}>
      {figure}
      {suffix && <small>{suffix}</small>}
    </div>
  )
}

export function Gauge({
  value,
  unit,
  warn,
  critical,
  max,
  label,
}: { value: unknown; max?: number | null; label: string } & Limits) {
  const id = useId()
  const number = typeof value === 'number' && Number.isFinite(value) ? value : null
  const full = max ?? (unit === 'percent' ? 100 : Math.max(1, (number ?? 0) * 1.25))
  const ratio = number == null ? 0 : Math.max(0, Math.min(1, number / full))
  const [figure, suffix] = parts(value, unit)
  // A 180° arc of radius 80 around (100, 100).
  const length = Math.PI * 80
  const mark = (limit?: number | null) => {
    if (limit == null || limit <= 0 || limit >= full) return null
    const angle = Math.PI * (1 - limit / full)
    const x = 100 + Math.cos(angle) * 80,
      y = 100 - Math.sin(angle) * 80
    const outerX = 100 + Math.cos(angle) * 93,
      outerY = 100 - Math.sin(angle) * 93
    return (
      <line
        x1={x}
        y1={y}
        x2={outerX}
        y2={outerY}
        stroke="var(--muted)"
        strokeWidth="2"
        strokeLinecap="round"
      />
    )
  }
  return (
    <div
      className="viz-gauge"
      data-level={level(value, warn, critical)}
      role="img"
      aria-label={label + ' ' + display(value, unit)}
    >
      <svg viewBox="0 0 200 112">
        <defs>
          <linearGradient id={id} x1="0" x2="1">
            <stop offset="0" stopColor="var(--tone, var(--c1))" />
            <stop offset="1" stopColor="var(--tone, var(--c2))" />
          </linearGradient>
        </defs>
        <path
          d="M20 100 A80 80 0 0 1 180 100"
          fill="none"
          stroke="var(--track)"
          strokeWidth="14"
          strokeLinecap="round"
        />
        <path
          d="M20 100 A80 80 0 0 1 180 100"
          fill="none"
          stroke={'url(#' + id + ')'}
          strokeWidth="14"
          strokeLinecap="round"
          strokeDasharray={length}
          strokeDashoffset={length * (1 - ratio)}
          style={{ transition: 'stroke-dashoffset .6s' }}
        />
        {mark(warn)}
        {mark(critical)}
      </svg>
      <div className="value">
        {figure}
        {suffix && <small>{suffix}</small>}
      </div>
    </div>
  )
}

export function Trend({
  points,
  unit,
  warn,
  critical,
  label,
}: { points: [number, number | null][]; label: string } & Limits) {
  const id = useId()
  const known = points.filter((point): point is [number, number] => typeof point[1] === 'number')
  const last = known.length ? known[known.length - 1][1] : null
  const [figure, suffix] = parts(last, unit)
  let low = Math.min(...known.map((point) => point[1])),
    high = Math.max(...known.map((point) => point[1]))
  if (!known.length) low = high = 0
  if (high === low) {
    high += 1
    low -= 1
  }
  const from = known[0]?.[0] ?? 0,
    to = known[known.length - 1]?.[0] ?? 1
  const x = (ts: number) => (to === from ? 150 : ((ts - from) / (to - from)) * 300)
  const y = (value: number) => 52 - ((value - low) / (high - low)) * 46
  const line = known
    .map((point, index) => (index ? 'L' : 'M') + x(point[0]).toFixed(1) + ' ' + y(point[1]).toFixed(1))
    .join(' ')
  return (
    <div className="viz-trend" data-level={level(last, warn, critical)}>
      <div className="viz-stat">
        {figure}
        {suffix && <small>{suffix}</small>}
      </div>
      {known.length > 1 && (
        <svg viewBox="0 0 300 58" preserveAspectRatio="none" role="img" aria-label={label + ' 趋势'}>
          <defs>
            <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor="var(--tone, var(--c1))" stopOpacity="0.38" />
              <stop offset="1" stopColor="var(--tone, var(--c1))" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path d={line + ' L300 58 L0 58 Z'} fill={'url(#' + id + ')'} />
          <path
            d={line}
            fill="none"
            stroke="var(--tone, var(--c1))"
            strokeWidth="2"
            vectorEffect="non-scaling-stroke"
          />
        </svg>
      )}
    </div>
  )
}

export function Donut({
  rows,
  unit,
  label,
}: {
  rows: [string, number | null][]
  unit?: Unit
  label: string
}) {
  const items = rows.filter((row): row is [string, number] => typeof row[1] === 'number' && row[1] > 0)
  const total = items.reduce((sum, row) => sum + row[1], 0)
  const length = 2 * Math.PI * 42
  let offset = 0
  return (
    <div className="viz-donut">
      <svg viewBox="0 0 120 120" role="img" aria-label={label}>
        <circle cx="60" cy="60" r="42" fill="none" stroke="var(--track)" strokeWidth="15" />
        {items.map((row, index) => {
          const share = total ? row[1] / total : 0
          const segment = (
            <circle
              key={row[0]}
              cx="60"
              cy="60"
              r="42"
              fill="none"
              stroke={palette[Math.min(index, palette.length - 1)]}
              strokeWidth="15"
              strokeDasharray={Math.max(0, share * length - 1.5) + ' ' + length}
              strokeDashoffset={-offset * length}
              transform="rotate(-90 60 60)"
            />
          )
          offset += share
          return segment
        })}
        <text x="60" y="60" textAnchor="middle" className="center">
          {display(total, unit === 'percent' ? 'count' : unit || 'count')}
        </text>
        <text x="60" y="76" textAnchor="middle" className="center-label">
          合计
        </text>
      </svg>
      <ul className="viz-legend">
        {rows.slice(0, 8).map((row, index) => (
          <li key={row[0] + index}>
            <i style={{ background: palette[Math.min(index, palette.length - 1)] }} />
            <span title={row[0]}>{row[0]}</span>
            <b>
              {display(row[1], unit || 'count')}
              {total && typeof row[1] === 'number' ? (
                <span className="muted"> · {((row[1] / total) * 100).toFixed(0)}%</span>
              ) : null}
            </b>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function TopList({ rows, unit, warn, critical }: { rows: [string, number | null][] } & Limits) {
  const numbers = rows.map((row) => (typeof row[1] === 'number' ? row[1] : 0))
  const full = unit === 'percent' ? 100 : Math.max(1, ...numbers)
  return (
    <ol className="viz-toplist">
      {rows.slice(0, 10).map((row, index) => (
        <li key={row[0] + index} data-level={level(row[1], warn, critical)}>
          <div>
            <span title={row[0]}>{row[0]}</span>
            <b>{display(row[1], unit)}</b>
          </div>
          <div className="bar">
            <i style={{ width: Math.max(0, Math.min(100, (numbers[index] / full) * 100)) + '%' }} />
          </div>
        </li>
      ))}
    </ol>
  )
}

const stateNames: Record<string, string> = { up: '正常', down: '异常', unknown: '未知' }

export function Timeline({
  rows,
  period,
}: {
  rows: [number, string, string][]
  period?: { from: number; to: number }
}) {
  const sorted = [...rows].sort((a, b) => a[0] - b[0])
  const from = period?.from ?? sorted[0]?.[0] ?? 0
  const to = Math.max(period?.to ?? 0, sorted[sorted.length - 1]?.[0] ?? 1, from + 1)
  const labels = [...new Set(sorted.map((row) => row[1]))].slice(0, 12)
  const clock = (ts: number) =>
    new Date(ts).toLocaleString(
      'zh-CN',
      to - from > 86400000
        ? { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }
        : { hour: '2-digit', minute: '2-digit', hour12: false },
    )
  return (
    <div className="viz-timeline">
      {labels.map((name) => {
        const own = sorted.filter((row) => row[1] === name)
        // Consecutive samples in the same state collapse into one segment.
        const segments: { start: number; state: string }[] = [{ start: from, state: 'unknown' }]
        for (const row of own) {
          const state = row[2] in stateNames ? row[2] : 'unknown'
          if (segments[segments.length - 1].state !== state)
            segments.push({ start: Math.max(from, row[0]), state })
        }
        return (
          <div className="row" key={name}>
            <span title={name}>{name}</span>
            <div className="strip" role="img" aria-label={name + ' 状态时间线'}>
              {segments.map((segment, index) => {
                const end = segments[index + 1]?.start ?? to
                const width = ((end - segment.start) / (to - from)) * 100
                return width > 0 ? (
                  <i
                    key={index}
                    className={'seg-' + segment.state}
                    style={{ width: width + '%' }}
                    title={stateNames[segment.state] + ' · ' + clock(segment.start) + ' — ' + clock(end)}
                  />
                ) : null
              })}
            </div>
          </div>
        )
      })}
      <div className="axis">
        <span>{clock(from)}</span>
        <span>{clock(to)}</span>
      </div>
    </div>
  )
}
