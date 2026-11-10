import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Icon } from './ui'
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

/* A decorative curve unique to each board, so cards are recognisable at a glance. */
export function BoardArt({ id, theme, icon }: { id: string; theme?: string; icon?: string }) {
  let seed = 0
  for (const char of id) seed = (seed * 31 + char.charCodeAt(0)) >>> 0
  const points: number[] = []
  for (let index = 0; index < 12; index++) {
    seed = (seed * 1664525 + 1013904223) >>> 0
    points.push(18 + (seed % 34))
  }
  const path = points.map((y, index) => (index ? 'L' : 'M') + (index * 300) / 11 + ' ' + (64 - y)).join(' ')
  return (
    <div className="board-art" data-board-theme={theme || 'aurora'}>
      <span className="glyph">
        <Icon name={boardIcon(icon)} size={20} />
      </span>
      <span className="theme-name">{themeNames[theme || 'aurora'] || theme}</span>
      <svg className="spark" viewBox="0 0 300 64" preserveAspectRatio="none" aria-hidden="true">
        <path d={path + ' L300 64 L0 64 Z'} fill="var(--board-accent-2)" opacity="0.16" />
        <path d={path} fill="none" stroke="var(--board-accent-2)" strokeWidth="2" />
      </svg>
    </div>
  )
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
