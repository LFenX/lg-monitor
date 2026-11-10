import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { IconName } from './ui'
import type { Scope } from './types'

export function canConfigure(scope: Scope) {
  return scope === 'config' || scope === 'admin'
}

export function useCopy() {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = (text: string) => {
    const done = () => {
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 1600)
    }
    const fallback = () => {
      // Clipboard access is unavailable on plain HTTP origins other than localhost.
      const area = document.createElement('textarea')
      area.value = text
      area.style.position = 'fixed'
      area.style.opacity = '0'
      document.body.appendChild(area)
      area.select()
      try {
        document.execCommand('copy')
        done()
      } catch {
        /* The text stays visible for manual selection. */
      }
      area.remove()
    }
    if (navigator.clipboard?.writeText) navigator.clipboard.writeText(text).then(done, fallback)
    else fallback()
  }
  return { copied, copy }
}

export function CopyBox({ text, prose = false, label }: { text: string; prose?: boolean; label?: string }) {
  const { copied, copy } = useCopy()
  return (
    <div className={'copy-box' + (prose ? ' prose' : '')} aria-label={label}>
      {text}
      <button type="button" onClick={() => copy(text)}>
        {copied ? '已复制' : '复制'}
      </button>
    </div>
  )
}

const boardIcons: Record<string, IconName> = {
  pulse: 'pulse',
  server: 'server',
  globe: 'globe',
  cpu: 'cpu',
  database: 'database',
  shield: 'shield',
  bell: 'bell',
  layers: 'layers',
  rocket: 'rocket',
  terminal: 'terminal',
}
export function boardIcon(name?: string): IconName {
  return (name && boardIcons[name]) || 'pulse'
}

export const themeNames: Record<string, string> = {
  aurora: '极光',
  midnight: '午夜',
  terminal: '终端',
  sunset: '落日',
  ocean: '深海',
  forest: '森林',
  paper: '纸张',
}

export const panelTypeNames: Record<string, string> = {
  stat: '数字',
  gauge: '仪表',
  trend: '趋势',
  line: '折线',
  area: '面积',
  bar: '柱状',
  toplist: '排行',
  pie: '环图',
  status_grid: '状态格',
  timeline: '时间线',
  table: '表格',
  alerts: '告警',
  log: '日志',
}

/* A thumbnail of a board drawn from its real cards, so a page is recognisable before it is opened. */
export function LayoutPreview({ layout }: { layout?: { type: string; width: number }[] }) {
  const cells = (layout || []).slice(0, 18)
  const tall = (type: string) =>
    ['line', 'area', 'bar', 'timeline', 'table', 'log', 'alerts', 'status_grid'].includes(type) ? 2 : 1
  return (
    <div className="layout-preview" aria-hidden="true">
      {cells.length ? (
        cells.map((cell, index) => (
          <i
            key={index}
            className={'lp-' + cell.type}
            style={{
              gridColumn: 'span ' + Math.max(1, Math.min(12, cell.width)),
              gridRow: 'span ' + tall(cell.type),
            }}
          >
            <b />
          </i>
        ))
      ) : (
        <i className="lp-html" style={{ gridColumn: 'span 12', gridRow: 'span 3' }}>
          <b />
        </i>
      )}
    </div>
  )
}

/* 48 half-hour slots over the last day, newest on the right. */
export function Heartbeat({ beats, size = 'md' }: { beats: string[]; size?: 'sm' | 'md' }) {
  const shown = size === 'sm' ? beats.slice(-24) : beats
  const labels: Record<string, string> = { up: '正常', down: '异常', unknown: '未知', '': '无数据' }
  return (
    <div
      className={'heartbeat ' + size}
      role="img"
      aria-label={'最近 ' + (size === 'sm' ? 12 : 24) + ' 小时状态'}
    >
      {shown.map((beat, index) => (
        <i key={index} className={'hb-' + (beat || 'none')} title={labels[beat] || beat} />
      ))}
    </div>
  )
}

export function stateName(state: string) {
  return state === 'up' ? '正常' : state === 'down' ? '异常' : '未知'
}
export function ago(ts: number, now: number) {
  if (!ts) return '暂无'
  const seconds = Math.max(0, Math.round((now - ts) / 1000))
  if (seconds < 5) return '刚刚'
  if (seconds < 60) return seconds + ' 秒前'
  if (seconds < 3600) return Math.floor(seconds / 60) + ' 分钟前'
  if (seconds < 86400) return Math.floor(seconds / 3600) + ' 小时前'
  return Math.floor(seconds / 86400) + ' 天前'
}

export function SectionTitle({
  title,
  count,
  children,
}: {
  title: string
  count?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className="section-title">
      <h2>{title}</h2>
      {count != null && <span className="count">{count}</span>}
      {children}
    </div>
  )
}

/* What to say to an agent; shown wherever the page itself lacks the permission to act. */
export function AskAgent({ prompt, note }: { prompt: string; note?: string }) {
  return (
    <div>
      <p className="muted small">{note || '把这句话交给已接入的 Agent，它会替你完成：'}</p>
      <CopyBox text={prompt} prose />
      <p className="muted small" style={{ marginTop: 10 }}>
        还没接入 Agent？<a href="#/connect">一分钟接好</a>
      </p>
    </div>
  )
}
