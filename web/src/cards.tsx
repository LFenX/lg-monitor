import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { useGate } from './access'
import { Glyph, kindGlyph } from './glyphs'
import { ago } from './kit'
import { Dialog } from './parts'
import { useApp } from './state'
import {
  Beats,
  Dot,
  EntryLink,
  UptimePill,
  dayText,
  healthText,
  latencyText,
  percentText,
  rowSlots,
} from './status'
import type { BoardView, CardStyle, Health, Monitor, StatusSlot } from './types'

/*
 * A service in the card view takes one of six looks. Each says what it is for, so people pick by need rather
 * than by taste alone; the board has a default and any service can have its own.
 */

export const cardStyles: { id: CardStyle; name: string; use: string }[] = [
  { id: 'pulse', name: '心跳', use: '可用率、24 小时心跳和响应时间都在。适合网站和接口，默认就是它。' },
  { id: 'minimal', name: '极简', use: '一个大状态点和一句话。适合只关心“在不在”的后台服务。' },
  { id: 'ring', name: '可用率环', use: '24 小时可用率的圆环，旁边是 7 天和 30 天。适合在意稳定性的服务。' },
  { id: 'latency', name: '响应曲线', use: '最近 24 小时的响应时间曲线和当前值。适合关心快慢的网站和接口。' },
  { id: 'days', name: '30 天', use: '30 个日格，一格一天。适合每天跑一次的计划任务。' },
  {
    id: 'number',
    name: '大数字',
    use: '只放一个大数：有响应时间时显示当前响应，否则显示可用率。适合挂在屏幕上远看。',
  },
]
export const styleName = (style: CardStyle) => cardStyles.find((item) => item.id === style)?.name || style

export function styleOf(board: Pick<BoardView, 'cardStyle' | 'cards'> | undefined, id: string): CardStyle {
  return board?.cards?.[id] || board?.cardStyle || 'pulse'
}

type Facts = { overall: Health; uptime: number | null | undefined; slots: StatusSlot[] | undefined }

/* A parent stands for its whole group, so its figures are the group's. */
function facts(item: Monitor): Facts {
  const grouped = !!item.groupBeats?.length
  return {
    overall: item.overall || item.state,
    uptime: item.groupUptime !== undefined ? item.groupUptime : item.uptime,
    slots: grouped ? item.groupBeatSlots : item.beatSlots,
  }
}

function lineText(item: Monitor) {
  const host = item.hosts[0]
  if (item.issue) return item.issue
  if (item.state === 'down') return host?.reason || '检测失败'
  if (item.state === 'unknown') return host?.reason || '等待第一次检查'
  return item.watching.join(' · ')
}

function checked(item: Monitor, now: number) {
  return item.hosts[0]?.ts ? ago(item.hosts[0].ts, now) + '检查' : '等待检查'
}

function Ring({ value, state }: { value: number | null | undefined; state: Health }) {
  const radius = 34,
    length = 2 * Math.PI * radius
  const shown = value == null ? 0 : Math.max(0, Math.min(100, value))
  return (
    <svg
      className="lm-ring"
      viewBox="0 0 84 84"
      role="img"
      aria-label={'24 小时可用率 ' + percentText(value)}
    >
      <circle cx="42" cy="42" r={radius} className="track" />
      <circle
        cx="42"
        cy="42"
        r={radius}
        className="value"
        data-state={value == null ? 'none' : state}
        strokeDasharray={length}
        strokeDashoffset={length * (1 - shown / 100)}
      />
      <text x="42" y="46" textAnchor="middle">
        {value == null ? '—' : percentText(value)}
      </text>
    </svg>
  )
}

function Spark({ points, now }: { points: { ts: number; ms: number }[]; now: number }) {
  const width = 260,
    height = 56,
    from = now - 24 * 3600 * 1000
  const top = Math.max(1, ...points.map((point) => point.ms)) * 1.2
  const x = (ts: number) => ((ts - from) / (now - from)) * width
  const y = (ms: number) => height - (ms / top) * (height - 4)
  const line = points
    .map((point, index) => (index ? 'L' : 'M') + x(point.ts).toFixed(1) + ' ' + y(point.ms).toFixed(1))
    .join(' ')
  const area =
    line +
    ' L' +
    x(points[points.length - 1].ts).toFixed(1) +
    ' ' +
    height +
    ' L' +
    x(points[0].ts).toFixed(1) +
    ' ' +
    height +
    ' Z'
  return (
    <svg
      className="lm-spark"
      viewBox={'0 0 ' + width + ' ' + height}
      preserveAspectRatio="none"
      role="img"
      aria-label="最近 24 小时响应时间"
    >
      <path d={area} className="area" />
      <path d={line} className="line" />
    </svg>
  )
}

function Body({ item, style }: { item: Monitor; style: CardStyle }) {
  const { now } = useApp()
  const current = now()
  const { overall, uptime, slots } = facts(item)
  switch (style) {
    case 'minimal':
      return (
        <div className="lm-card-minimal">
          <span className="lm-orb" data-state={overall} />
          <div>
            <b>{healthText[overall] || '无数据'}</b>
            <span>{lineText(item)}</span>
          </div>
        </div>
      )
    case 'ring':
      return (
        <div className="lm-card-ring">
          <Ring value={uptime} state={overall} />
          <dl>
            <dt>7 天</dt>
            <dd>{percentText(item.uptime7d)}</dd>
            <dt>30 天</dt>
            <dd>{percentText(item.uptime30d)}</dd>
          </dl>
        </div>
      )
    case 'latency': {
      const points = item.latency || []
      if (!points.length)
        return (
          <p className="lm-card-note">这个服务没有网络探测，所以没有响应时间。换成“心跳”或“极简”更合适。</p>
        )
      const average = points.reduce((sum, point) => sum + point.ms, 0) / points.length
      return (
        <div className="lm-card-latency">
          <div className="figures">
            <b>{latencyText(item.latencyMs)}</b>
            <span>24 小时平均 {latencyText(average)}</span>
          </div>
          <Spark points={points} now={current} />
        </div>
      )
    }
    case 'days': {
      const days = item.days || []
      const up30 = item.uptime30d
      return (
        <div className="lm-card-days">
          <div className="grid" role="img" aria-label="最近 30 天状态">
            {days.map((day) => (
              <i
                key={day.from}
                data-state={day.state || 'none'}
                title={
                  dayText(day.from) +
                  ' · ' +
                  (day.uptime == null ? '无数据' : '可用率 ' + percentText(day.uptime))
                }
              />
            ))}
          </div>
          <span>30 天可用率 {percentText(up30)}</span>
        </div>
      )
    }
    case 'number': {
      const probed = item.latencyMs != null
      return (
        <div className="lm-card-number" data-state={overall}>
          <b>{probed ? latencyText(item.latencyMs) : percentText(uptime)}</b>
          <span>
            {probed ? '当前响应（最近 5 分钟）' : '24 小时可用率'} · {healthText[overall] || '无数据'}
          </span>
        </div>
      )
    }
  }
  return (
    <div className="lm-card-pulse">
      <div className="line">
        <UptimePill value={uptime} state={overall} />
        <span className="sub" data-state={overall}>
          {lineText(item)}
        </span>
      </div>
      <Beats
        slots={rowSlots(item.groupBeats?.length ? item.groupBeats : item.beats, current, slots)}
        liveState={overall}
        label={item.name + ' 最近 24 小时'}
      />
      <div className="meta">
        <span>{latencyText(item.latencyMs)}</span>
        <span>{checked(item, current)}</span>
      </div>
    </div>
  )
}

function StylePicker({
  value,
  pick,
  more,
}: {
  value: CardStyle
  pick: (style: CardStyle) => void
  more: () => void
}) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => !box.current?.contains(event.target as Node) && setOpen(false)
    const key = (event: KeyboardEvent) => event.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', outside)
    window.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', outside)
      window.removeEventListener('keydown', key)
    }
  }, [open])
  return (
    <div className="lm-menu lm-card-style" ref={box}>
      <button
        className="lm-btn ghost small icon"
        aria-label="换卡片样式"
        title={'卡片样式：' + styleName(value)}
        aria-expanded={open}
        onClick={() => setOpen((shown) => !shown)}
      >
        <Glyph name="cards" size={15} />
      </button>
      {open && (
        <div className="lm-menu-list" role="menu" onClick={() => setOpen(false)}>
          {cardStyles.map((style) => (
            <button
              key={style.id}
              role="menuitemradio"
              aria-checked={style.id === value}
              onClick={() => pick(style.id)}
            >
              <Glyph name={style.id === value ? 'check' : 'cards'} size={15} />
              {style.name}
            </button>
          ))}
          <button onClick={more}>
            <Glyph name="eye" size={15} />
            看全部样式…
          </button>
        </div>
      )}
    </div>
  )
}

export function ServiceCard({
  item,
  below = [],
  style,
  remote = false,
  pick,
  more,
  sample = false,
}: {
  item: Monitor
  below?: Monitor[]
  style: CardStyle
  remote?: boolean
  pick?: (style: CardStyle) => void
  more?: () => void
  sample?: boolean
}) {
  const overall = facts(item).overall
  const name: ReactNode =
    remote || sample ? item.name : <a href={'#/monitors/' + encodeURIComponent(item.id)}>{item.name}</a>
  return (
    <article
      className="lm-card"
      data-style={style}
      data-overall={overall}
      role={sample ? undefined : 'listitem'}
      aria-label={item.name}
    >
      <header className="lm-card-head">
        <Glyph name={kindGlyph[item.kind] || 'other'} size={15} />
        <strong>{name}</strong>
        {!sample && <EntryLink href={item.entry} name={item.name} />}
        {pick && more && <StylePicker value={style} pick={pick} more={more} />}
      </header>
      <Body item={item} style={style} />
      {below.length > 0 && (
        <div className="lm-chips" aria-label="子服务">
          {below.map((child) => (
            <a
              className="lm-chip"
              key={child.id}
              href={remote || sample ? undefined : '#/monitors/' + encodeURIComponent(child.id)}
              title={child.watching.join(' · ')}
            >
              <Dot state={child.overall || child.state} />
              <span>{child.name}</span>
            </a>
          ))}
        </div>
      )}
    </article>
  )
}

/* Sample data that makes each style show what it is for. */
function sampleMonitor(now: number): { item: Monitor; below: Monitor[] } {
  const hour = 3600 * 1000,
    day = 24 * hour
  const beats = Array.from(
    { length: 48 },
    (_, index) => (index === 31 ? 'down' : 'up') as Monitor['beats'][number],
  )
  const base = {
    collector: 'fake',
    hosts: [{ collectorId: 'fake', hostname: '这台电脑', state: 'up', ts: now - 20000 }],
    rules: [],
    firing: 0,
    kind: 'url' as const,
    children: [],
  }
  const item: Monitor = {
    ...base,
    id: 'sample',
    name: '示例网站',
    watching: ['网址 https://example.com'],
    state: 'up',
    overall: 'up',
    beats,
    uptime: 99.4,
    latencyMs: 86,
    uptime7d: 99.8,
    uptime30d: 99.9,
    days: Array.from({ length: 30 }, (_, index) => ({
      from: now - (29 - index) * day,
      state: index === 12 ? 'down' : 'up',
      uptime: index === 12 ? 96.5 : 100,
    })),
    latency: Array.from({ length: 96 }, (_, index) => ({
      ts: now - 24 * hour + index * 15 * 60 * 1000,
      ms: 70 + 25 * Math.sin(index / 7) + (index % 11 === 0 ? 40 : 0),
    })),
  }
  const below: Monitor[] = [
    {
      ...base,
      id: 'sample-tunnel',
      name: 'Cloudflare 隧道',
      watching: ['进程 cloudflared.exe'],
      state: 'up',
      beats,
      uptime: 100,
      latencyMs: null,
      kind: 'process',
      depth: 1,
    },
  ]
  return { item, below }
}

export function CardGallery({
  board,
  close,
  saved,
}: {
  board: BoardView
  close: () => void
  saved: () => void
}) {
  const { api, now, refresh } = useApp()
  const gate = useGate()
  const [busy, setBusy] = useState(''),
    [error, setError] = useState('')
  const { item, below } = sampleMonitor(now())
  const save = (body: Record<string, unknown>, key: string) =>
    gate('config', '修改卡片样式', () => {
      setBusy(key)
      setError('')
      api
        .request('/boards/' + encodeURIComponent(board.id), { method: 'PUT', body: JSON.stringify(body) })
        .then(() => {
          refresh()
          saved()
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
        .finally(() => setBusy(''))
    })
  const top = board.items.filter((service) => !service.depth)
  const fallback = board.cardStyle || 'pulse'
  return (
    <Dialog title="卡片样式" close={close} wide>
      <p className="lm-muted" style={{ margin: 0 }}>
        卡片视图里每个服务都可以用不同的样式。下面是同一个示例服务的六种样子：点“设为默认”让整个面板用它，或在最下面给单个服务换。
      </p>
      {error && <p className="lm-error">{error}</p>}
      <div className="lm-gallery">
        {cardStyles.map((style) => (
          <section key={style.id} className="lm-gallery-item" aria-label={'样式 ' + style.name}>
            <ServiceCard item={item} below={below} style={style.id} sample />
            <div className="about">
              <strong>{style.name}</strong>
              <span>{style.use}</span>
              {fallback === style.id ? (
                <span className="lm-tag">当前默认</span>
              ) : (
                <button
                  className="lm-btn small"
                  disabled={!!busy}
                  onClick={() => save({ cardStyle: style.id }, 'default')}
                >
                  设为默认
                </button>
              )}
            </div>
          </section>
        ))}
      </div>
      {top.length > 0 && (
        <section className="lm-gallery-services" aria-label="按服务选择样式">
          <h3>给单个服务换样式</h3>
          {top.map((service) => (
            <label key={service.id} className="lm-field-row">
              <span>{service.name}</span>
              <select
                className="lm-input"
                value={board.cards?.[service.id] || ''}
                disabled={!!busy}
                onChange={(event) => save({ cards: { [service.id]: event.target.value } }, service.id)}
              >
                <option value="">跟随默认（{styleName(fallback)}）</option>
                {cardStyles.map((style) => (
                  <option key={style.id} value={style.id}>
                    {style.name}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </section>
      )}
    </Dialog>
  )
}
