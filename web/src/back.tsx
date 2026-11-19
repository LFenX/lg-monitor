import { useEffect, useRef, useState } from 'react'
import { Glyph } from './glyphs'
import { useApp } from './state'
import './back.css'

/*
 * Every page can go back to where the person came from, named: "← 返回 我的服务". The trail follows the pages
 * opened in this window; a page opened directly (from the small window, say) goes up to its natural parent.
 */

const trailKey = 'lgmon-trail'

function readTrail(): string[] {
  try {
    const value = JSON.parse(sessionStorage.getItem(trailKey) || '[]')
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

function writeTrail(trail: string[]) {
  try {
    sessionStorage.setItem(trailKey, JSON.stringify(trail.slice(-30)))
  } catch {
    // Without storage the bar still offers the natural parent.
  }
}

const clean = (hash: string) =>
  hash.replace(/^#\/?/, '').split('?')[0] ? '#/' + hash.replace(/^#\/?/, '') : '#/'

/* Where a page goes up to when there is no trail: a service to its board, a shared service to its board, and so on. */
function parentOf(hash: string): string | undefined {
  const parts = clean(hash).slice(2).split('/').filter(Boolean)
  if (!parts.length) return undefined
  if (parts[0] === 'lan' && parts[1] === 'remote' && parts[3] === 'monitors')
    return '#/lan/remote/' + parts[2]
  if (parts[0] === 'lan' && parts[1] === 'remote') return '#/lan'
  if (parts[0] === 'alerts' && parts[1]) return '#/alerts'
  if (parts[0] === 'settings' && parts[1]) return '#/settings'
  if (parts[0] === 'monitors' || parts[0] === 'boards' || parts.length === 1) return '#/'
  return '#/' + parts[0]
}

export function useTrail() {
  const [trail, setTrail] = useState(readTrail)
  const current = useRef(clean(window.location.hash))
  const goingBack = useRef(false)
  useEffect(() => {
    const change = () => {
      const next = clean(window.location.hash)
      const previous = current.current
      if (next === previous) return
      current.current = next
      setTrail((value) => {
        let updated: string[]
        if (goingBack.current || value[value.length - 1] === next) updated = value.slice(0, -1)
        else updated = [...value, previous]
        goingBack.current = false
        writeTrail(updated)
        return updated
      })
    }
    window.addEventListener('hashchange', change)
    return () => window.removeEventListener('hashchange', change)
  }, [])
  const back = (target: string) => {
    goingBack.current = trail[trail.length - 1] === target
    window.location.hash = target
  }
  return { trail, back }
}

export function BackBar() {
  const { boardList, monitors, remoteBoards } = useApp()
  const { trail, back } = useTrail()
  const here = clean(window.location.hash)
  const target = trail[trail.length - 1] || parentOf(here)
  if (!target || target === here || here === '#/' || here.startsWith('#/start')) return null
  const name = (hash: string) => {
    const [section, first, second, third, fourth] = hash.slice(2).split('/').map(decodeURIComponent)
    if (!section) return boardList[0]?.name || '面板'
    if (section === 'boards') return boardList.find((board) => board.id === first)?.name || '面板'
    if (section === 'monitors') return monitors.find((monitor) => monitor.id === first)?.name || '服务'
    if (section === 'lan' && first === 'remote') {
      const shared = remoteBoards.find((item) => item.id === second)
      if (third === 'monitors') return fourth || '服务'
      return shared ? shared.ownerName + ' 的「' + shared.boardName + '」' : '共享面板'
    }
    const pages: Record<string, string> = {
      lan: '内网',
      alerts: '告警',
      settings: '设置',
      views: '视图',
      teams: '团队',
      notifications: '通知',
      explore: '发现',
      connect: '接入',
    }
    return pages[section] || '上一页'
  }
  return (
    <nav className="lm-backbar" aria-label="返回">
      <button onClick={() => back(target)}>
        <Glyph name="back" size={15} />
        返回 {name(target)}
      </button>
    </nav>
  )
}
