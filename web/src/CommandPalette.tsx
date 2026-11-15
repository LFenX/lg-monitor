import { useEffect, useMemo, useRef, useState } from 'react'
import { useApp } from './state'
import type { ExploreItem, ExploreResult } from './types'
import { Icon } from './ui'
import type { IconName } from './ui'

type Entry = {
  group: string
  icon: IconName
  title: string
  secondary?: string
  hint?: string
  run: () => void
}

const go = (hash: string) => () => {
  window.location.hash = hash
}

export function CommandPalette({ close, askAssistant }: { close: () => void; askAssistant: () => void }) {
  const { api, monitors, boards, boardList, data } = useApp()
  const [query, setQuery] = useState(''),
    [selected, setSelected] = useState(0),
    [remote, setRemote] = useState<ExploreItem[]>([])
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => input.current?.focus(), [])
  const keyword = query.trim().toLowerCase()
  useEffect(() => {
    setRemote([])
    if (keyword.length < 2 || !data?.collectors.length) return
    const controller = new AbortController()
    // Services and ports live on the collector, so they are searched only after the user pauses.
    const timer = setTimeout(() => {
      for (const kind of ['services', 'ports'] as const)
        void api
          .request<ExploreResult>(
            '/explore?' + new URLSearchParams({ kind, q: keyword, limit: '6' }).toString(),
            {
              signal: controller.signal,
            },
          )
          .then((result) => {
            if (!controller.signal.aborted)
              setRemote((items) => [
                ...items.filter((item) => item.kind !== result.items[0]?.kind),
                ...result.items,
              ])
          })
          .catch(() => {})
    }, 250)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [api, keyword, data?.collectors.length])
  const entries = useMemo(() => {
    const match = (...values: (string | undefined)[]) =>
      !keyword || values.some((value) => value?.toLowerCase().includes(keyword))
    const result: Entry[] = []
    const pages: [string, string, IconName][] = [
      ['告警', '#/alerts', 'alert'],
      ['设置', '#/settings', 'settings'],
      ['通知与飞书', '#/notifications', 'bell'],
      ['接入 Agent', '#/connect', 'plug'],
      ['自定义视图', '#/views', 'board'],
      ['探索这台电脑', '#/explore', 'compass'],
    ]
    for (const board of boardList)
      if (match(board.name, board.id, board.description))
        result.push({
          group: '面板',
          icon: 'board',
          title: board.name,
          secondary: board.description,
          hint: board.summary.down ? board.summary.down + ' 个异常' : '',
          run: go('#/boards/' + encodeURIComponent(board.id)),
        })
    for (const [title, hash, icon] of pages)
      if (match(title)) result.push({ group: '页面', icon, title, run: go(hash) })
    for (const item of monitors)
      if (match(item.name, item.id, ...item.watching)) {
        result.push({
          group: '服务',
          icon: 'radar',
          title: item.name,
          secondary: item.watching.join(' · '),
          hint: item.state === 'up' ? '正常' : item.state === 'down' ? '异常' : '等待',
          run: go('#/monitors/' + encodeURIComponent(item.id)),
        })
      }
    for (const page of boards)
      if (match(page.title, page.id, page.description))
        result.push({
          group: '自定义视图',
          icon: 'board',
          title: page.title,
          secondary: page.description,
          run: go('#/views/' + encodeURIComponent(page.id)),
        })
    for (const host of data?.collectors || [])
      if (match(host.hostname, host.id))
        result.push({
          group: '电脑',
          icon: 'server',
          title: host.hostname || host.id,
          hint: host.online ? '在线' : '离线',
          run: go('#/hosts/' + encodeURIComponent(host.id)),
        })
    for (const item of remote)
      result.push({
        group: item.kind === 'service' ? 'Windows 服务' : '端口',
        icon: item.kind === 'service' ? 'layers' : 'globe',
        title:
          item.kind === 'service'
            ? item.displayName || item.name
            : ':' + item.port + ' ' + (item.process || ''),
        secondary: item.kind === 'service' ? item.name : item.address,
        hint: item.monitoredBy
          ? '已监控'
          : item.kind === 'service'
            ? item.state === 'running'
              ? '运行中'
              : '已停止'
            : '',
        run: go(
          '#/explore?' +
            new URLSearchParams({
              kind: item.kind === 'service' ? 'services' : 'ports',
              q: item.kind === 'service' ? item.name : String(item.port),
            }).toString(),
        ),
      })
    if (keyword)
      result.push({
        group: 'AI',
        icon: 'sparkles',
        title: '问波西米亚：' + query.trim(),
        secondary: '让助手查询并回答',
        run: () => {
          askAssistant()
          try {
            sessionStorage.setItem('lgmon-assistant-draft', query.trim())
          } catch {
            /* The question just needs retyping. */
          }
          window.dispatchEvent(new CustomEvent('lgmon-ask'))
        },
      })
    return result.slice(0, 40)
  }, [keyword, query, monitors, boards, data?.collectors, remote, askAssistant])
  useEffect(() => setSelected(0), [keyword])
  const choose = (entry?: Entry) => {
    if (!entry) return
    entry.run()
    close()
  }
  let group = ''
  return (
    <div
      className="palette-backdrop"
      onMouseDown={(event) => event.target === event.currentTarget && close()}
    >
      <div className="palette" role="dialog" aria-label="搜索">
        <div className="palette-input">
          <Icon name="search" size={18} />
          <input
            ref={input}
            value={query}
            placeholder="搜索监控、面板、电脑、Windows 服务、端口…"
            aria-label="搜索"
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') close()
              if (event.key === 'ArrowDown') {
                event.preventDefault()
                setSelected((value) => Math.min(entries.length - 1, value + 1))
              }
              if (event.key === 'ArrowUp') {
                event.preventDefault()
                setSelected((value) => Math.max(0, value - 1))
              }
              if (event.key === 'Enter') choose(entries[selected])
            }}
          />
          <kbd>Esc</kbd>
        </div>
        {entries.length ? (
          <ul className="palette-results" role="listbox">
            {entries.map((entry, index) => {
              const heading = entry.group !== group
              group = entry.group
              return (
                <li key={entry.group + entry.title + index} role="presentation">
                  {heading && <div className="palette-group">{entry.group}</div>}
                  <div
                    className="palette-item"
                    role="option"
                    aria-selected={index === selected}
                    onMouseEnter={() => setSelected(index)}
                    onClick={() => choose(entry)}
                  >
                    <Icon name={entry.icon} size={17} />
                    <span>
                      {entry.title}
                      {entry.secondary && <span className="secondary"> · {entry.secondary}</span>}
                    </span>
                    {entry.hint && <small>{entry.hint}</small>}
                  </div>
                </li>
              )
            })}
          </ul>
        ) : (
          <div className="palette-empty">没有找到。按 Enter 也可以直接问波西米亚。</div>
        )}
      </div>
    </div>
  )
}
