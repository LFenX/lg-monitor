/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useState } from 'react'
import type { ReactNode } from 'react'
import { Avatar, byOwner } from './avatar'
import { byDepartment } from './departments'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago } from './kit'
import { useRemoteBoard, useRemoteSummaries } from './remote-cache'
import type { RemoteSummaryEntry } from './remote-cache'
import { sharedHref } from './shared'
import { useApp } from './state'
import { Dot, ownIssue, percentText } from './status'
import type { DepartmentList, LANOutgoing, Monitor } from './types'
import './side.css'

/*
 * Boards in the sidebar fold open to their services, like lgmon's small window: a glance shows how every service
 * of every board is doing, yours and the ones other people share.
 */

const key = 'lgmon-side-open'
const deptFoldKey = 'lgmon-side-dept-collapsed'

function readMap(storageKey: string): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(storageKey) || '{}') as Record<string, boolean>
  } catch {
    return {}
  }
}

function useStoredToggle(storageKey: string) {
  const [map, setMap] = useState(() => readMap(storageKey))
  const toggle = (id: string) =>
    setMap((previous) => {
      const next = { ...previous, [id]: !previous[id] }
      try {
        localStorage.setItem(storageKey, JSON.stringify(next))
      } catch {
        // Without storage the fold still works until the page closes.
      }
      return next
    })
  return { map, toggle }
}

export function useSideFolds() {
  const { map: open, toggle } = useStoredToggle(key)
  return { open, toggle }
}

export function useDeptFolds() {
  const { map: collapsed, toggle } = useStoredToggle(deptFoldKey)
  return { collapsed, toggle }
}

const rank = (state: string) => (state === 'down' ? 0 : state === 'up' ? 2 : 1)

function note(monitor: Monitor) {
  const state = monitor.state
  if (state === 'down') return '异常'
  if (state !== 'up') return '无数据'
  const uptime = monitor.uptime
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
      type="button"
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
  activeMonitorId,
}: {
  items: Monitor[]
  all: Monitor[]
  href: (id: string) => string
  label: string
  activeMonitorId?: string
}) {
  const { t } = useI18n()
  if (!items.length) return <p className="lm-side-empty">{t('sideNoServices')}</p>
  return (
    <ul className="lm-side-svcs" aria-label={label}>
      {items.map((monitor) => {
        const kids = all.filter((item) => item.parent === monitor.id)
        return (
          <li key={monitor.id} data-state={monitor.state}>
            <a
              href={href(monitor.id)}
              aria-current={activeMonitorId === monitor.id ? 'page' : undefined}
              title={ownIssue(monitor) || monitor.name}
            >
              <Dot state={monitor.state} />
              <span className="name">{monitor.name}</span>
              {kids.length > 0 && (
                <span className="lm-side-kids" aria-label={kids.length + ' 个子服务'}>
                  {/* The ones in trouble come first, so a fold of eight still shows the red one. */}
                  {[...kids]
                    .sort((a, b) => rank(a.state) - rank(b.state))
                    .slice(0, kids.length > 6 ? 5 : 6)
                    .map((kid) => (
                      <i key={kid.id} data-state={kid.state} />
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
export function OwnServices({
  ids,
  name,
  activeMonitorId,
}: {
  ids: string[]
  name: string
  activeMonitorId?: string
}) {
  const { monitors } = useApp()
  const byID = new Map(monitors.map((monitor) => [monitor.id, monitor]))
  const items = ids.map((id) => byID.get(id)).filter((item): item is Monitor => !!item)
  return (
    <Rows
      items={items}
      all={monitors}
      href={(id) => '#/monitors/' + encodeURIComponent(id)}
      label={name + ' 的服务'}
      activeMonitorId={activeMonitorId}
    />
  )
}

/* Someone else's board, folded open: read through this computer only while it is open. */
export function RemoteServices({
  id,
  name,
  activeMonitorId,
}: {
  id: string
  name: string
  activeMonitorId?: string
}) {
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
        activeMonitorId={activeMonitorId}
      />
    )
  } else if (copy.gone || copy.error) body = <p className="lm-side-empty">连不上对方</p>
  else body = <p className="lm-side-empty">正在读取…</p>
  return <>{body}</>
}

function remoteTooltip(item: LANOutgoing, entry: RemoteSummaryEntry | undefined, nowTs: number) {
  const parts = [item.ownerName + ' 的「' + item.boardName + '」']
  const summary = entry?.board?.board.summary
  if (summary) {
    const total = summary.services + summary.children
    if (summary.down > 0) parts.push(`${summary.down} 异常 / 共 ${total} 项`)
    else if (total > 0) parts.push(`共 ${total} 项正常`)
    if (typeof summary.uptime === 'number') parts.push(`24h ${percentText(summary.uptime)}`)
  }
  if (entry?.board?.fetchedAt) parts.push(`更新于 ${ago(entry.board.fetchedAt, nowTs)}`)
  if (entry?.offline) parts.push('当前连不上对方')
  return parts.join(' · ')
}

function RemoteSingleBoard({
  item,
  entry,
  activeRemoteId,
  activeRemoteMonitorId,
  folds,
}: {
  item: LANOutgoing
  entry?: RemoteSummaryEntry
  activeRemoteId?: string
  activeRemoteMonitorId?: string
  folds: { open: Record<string, boolean>; toggle: (id: string) => void }
}) {
  const { now } = useApp()
  const { t } = useI18n()
  const summary = entry?.board?.board.summary
  const offline = !!entry?.offline
  const totalSvcs = summary ? summary.services + summary.children : 0
  const down = summary?.down || 0
  const dotState = offline && !summary ? 'empty' : summary ? summary.state : 'empty'
  const sameName = item.boardName.trim().toLowerCase() === item.ownerName.trim().toLowerCase()
  const isCurrent = activeRemoteId === item.id && !activeRemoteMonitorId
  const foldId = 'remote:' + item.id
  const open = !!folds.open[foldId]

  let subtitle: ReactNode
  if (sameName) {
    if (offline && !summary) subtitle = t('sideOffline')
    else if (summary) {
      if (down > 0) {
        subtitle = (
          <>
            <strong className="is-bad">{t('sideDownCount', { count: down })}</strong>
            {totalSvcs > 0 && ` · ${t('sideServiceCount', { count: totalSvcs })}`}
            {offline && ` · ${t('sideOffline')}`}
          </>
        )
      } else if (totalSvcs > 0) {
        subtitle = (
          <>
            {t('sideServiceCount', { count: totalSvcs })}
            {typeof summary.uptime === 'number' && ` · ${percentText(summary.uptime)}`}
            {offline && ` · ${t('sideOffline')}`}
          </>
        )
      } else {
        subtitle = offline ? t('sideOffline') : t('sideNoServices')
      }
    } else {
      subtitle = t('sideOpenBoard')
    }
  } else {
    let statusPart: ReactNode = null
    if (offline && !summary) statusPart = t('sideOffline')
    else if (summary) {
      if (down > 0) statusPart = <strong className="is-bad">{t('sideDownCount', { count: down })}</strong>
      else if (typeof summary.uptime === 'number' && totalSvcs > 0) statusPart = percentText(summary.uptime)
      else if (totalSvcs > 0) statusPart = t('sideServiceCount', { count: totalSvcs })
      if (offline) {
        statusPart = statusPart ? (
          <>
            {statusPart} · {t('sideOffline')}
          </>
        ) : (
          t('sideOffline')
        )
      }
    }
    subtitle = (
      <>
        <span>{item.ownerName}</span>
        {statusPart && (
          <>
            <span className="lm-remote-sep" aria-hidden="true">
              ·
            </span>
            <span>{statusPart}</span>
          </>
        )}
      </>
    )
  }

  return (
    <div
      className="lm-nav-board lm-remote-single"
      data-state={summary?.state || undefined}
      data-offline={offline || undefined}
    >
      <a
        className="lm-nav-remote is-single"
        href={'#/lan/remote/' + encodeURIComponent(item.id)}
        aria-current={isCurrent ? 'page' : undefined}
        data-branch={activeRemoteId === item.id ? 'active' : undefined}
        title={remoteTooltip(item, entry, now())}
      >
        <span className="lm-remote-ava">
          <Avatar value={item.ownerAvatar} seed={item.fingerprint} size={24} />
          <span className="lm-ava-dot" data-state={dotState} aria-hidden="true" />
        </span>
        <span className="lm-remote-copy">
          <span className="name">{item.boardName}</span>
          <span className="lm-remote-sub">{subtitle}</span>
        </span>
        {down > 0 ? (
          <span className="count bad">{down}</span>
        ) : totalSvcs > 0 ? (
          <span className="count">{totalSvcs}</span>
        ) : null}
      </a>
      <SideFold id={foldId} name={item.boardName} open={open} toggle={folds.toggle} />
      {open && (
        <RemoteServices
          id={item.id}
          name={item.boardName}
          activeMonitorId={activeRemoteId === item.id ? activeRemoteMonitorId : undefined}
        />
      )}
    </div>
  )
}

/*
 * Shared boards from colleagues on the LAN, structured cleanly by department and person with live health
 * summaries so even narrow sidebars communicate status at a glance.
 */
export function SharedBoardsNav({
  remoteBoards,
  departments,
  activeRemoteId,
  activeRemoteMonitorId,
  folds,
}: {
  remoteBoards: LANOutgoing[]
  departments?: DepartmentList
  activeRemoteId?: string
  activeRemoteMonitorId?: string
  folds: { open: Record<string, boolean>; toggle: (id: string) => void }
}) {
  const { now } = useApp()
  const { t } = useI18n()
  const deptFolds = useDeptFolds()
  const summaries = useRemoteSummaries(remoteBoards.map((item) => item.id))

  if (!remoteBoards.length) return null

  const groups = byDepartment(remoteBoards, departments)
  const hasDepartments = groups.some((group) => !!group.department)
  let totalDown = 0
  for (const item of remoteBoards) {
    totalDown += summaries.get(item.id)?.board?.board.summary.down || 0
  }

  return (
    <section className="lm-side-sec lm-side-shared" aria-label={t('sharedBoards')}>
      <div className="lm-sec-head">
        <span className="lm-sec-title">{t('sharedBoards')}</span>
        <span className="lm-sec-meta">
          {totalDown > 0 && (
            <span className="lm-sec-pill is-bad">{t('sideDownCount', { count: totalDown })}</span>
          )}
          <span className="lm-sec-count">{remoteBoards.length}</span>
        </span>
      </div>
      <nav className="lm-nav lm-shared-nav" aria-label="别人的面板">
        {groups.map((group) => {
          const dept = group.department
          const deptKey = dept?.key || 'none'
          const collapsed = !!dept && !!deptFolds.collapsed[deptKey]
          const owners = byOwner(group.items)
          let deptDown = 0
          for (const item of group.items) {
            deptDown += summaries.get(item.id)?.board?.board.summary.down || 0
          }
          return (
            <div
              key={deptKey}
              role="group"
              aria-label={dept ? dept.name : '其他'}
              className={'lm-dept-card' + (dept ? '' : hasDepartments ? ' is-unassigned' : ' is-flat')}
              data-collapsed={collapsed || undefined}
              data-bad={deptDown > 0 || undefined}
            >
              {dept ? (
                <button
                  type="button"
                  className="lm-dept-head"
                  aria-expanded={!collapsed}
                  aria-label={t(collapsed ? 'sideExpandDept' : 'sideCollapseDept', { name: dept.name })}
                  onClick={() => deptFolds.toggle(deptKey)}
                  title={`${dept.name} · ${t('sideSharedSummary', { owners: owners.length, boards: group.items.length })}`}
                >
                  <Avatar value={dept.avatar} seed={dept.key} size={20} />
                  <span className="lm-dept-name">{dept.name}</span>
                  <span className="lm-dept-meta">
                    {deptDown > 0 ? (
                      <span className="lm-mini-badge is-bad">{deptDown}</span>
                    ) : (
                      <span className="lm-mini-count">{group.items.length}</span>
                    )}
                    <Glyph name="chevron" size={12} className="lm-dept-chevron" />
                  </span>
                </button>
              ) : hasDepartments ? (
                <div className="lm-dept-sub">
                  <span>{t('sideUnassignedDept')}</span>
                  <span className="lm-mini-count">{group.items.length}</span>
                </div>
              ) : null}
              {!collapsed && (
                <div className="lm-dept-body">
                  {owners.map(([ownerKey, boards]) => {
                    const ownerName = boards[0].ownerName
                    const ownerAvatar = boards[0].ownerAvatar
                    const ownerSeed = boards[0].fingerprint
                    if (boards.length === 1) {
                      return (
                        <div
                          key={ownerKey}
                          role="group"
                          aria-label={ownerName + ' 的面板'}
                          className="lm-owner-block is-single"
                        >
                          <RemoteSingleBoard
                            item={boards[0]}
                            entry={summaries.get(boards[0].id)}
                            activeRemoteId={activeRemoteId}
                            activeRemoteMonitorId={activeRemoteMonitorId}
                            folds={folds}
                          />
                        </div>
                      )
                    }
                    let ownerDown = 0
                    for (const item of boards) {
                      ownerDown += summaries.get(item.id)?.board?.board.summary.down || 0
                    }
                    return (
                      <div
                        key={ownerKey}
                        role="group"
                        aria-label={ownerName + ' 的面板'}
                        className="lm-owner-block is-multi"
                      >
                        <div className="lm-owner-head">
                          <Avatar value={ownerAvatar} seed={ownerSeed} size={18} />
                          <span className="lm-owner-name">{ownerName}</span>
                          {ownerDown > 0 ? (
                            <span className="lm-mini-badge is-bad">{ownerDown}</span>
                          ) : (
                            <span className="lm-owner-count">
                              {t('sideBoardCount', { count: boards.length })}
                            </span>
                          )}
                        </div>
                        <div className="lm-owner-boards">
                          {boards.map((item) => {
                            const entry = summaries.get(item.id)
                            const summary = entry?.board?.board.summary
                            const offline = !!entry?.offline
                            const totalSvcs = summary ? summary.services + summary.children : 0
                            const down = summary?.down || 0
                            const foldId = 'remote:' + item.id
                            const open = !!folds.open[foldId]
                            return (
                              <div key={item.id} className="lm-nav-board lm-remote-nested">
                                <a
                                  className="lm-nav-remote is-nested"
                                  href={'#/lan/remote/' + encodeURIComponent(item.id)}
                                  aria-current={
                                    activeRemoteId === item.id && !activeRemoteMonitorId ? 'page' : undefined
                                  }
                                  title={remoteTooltip(item, entry, now())}
                                >
                                  <Dot state={offline && !summary ? 'empty' : summary?.state || 'empty'} />
                                  <span className="name">{item.boardName}</span>
                                  {down > 0 ? (
                                    <span className="count bad">{down}</span>
                                  ) : totalSvcs > 0 ? (
                                    <span className="count">{totalSvcs}</span>
                                  ) : offline ? (
                                    <span className="lm-remote-tag">{t('sideOffline')}</span>
                                  ) : null}
                                </a>
                                <SideFold
                                  id={foldId}
                                  name={item.boardName}
                                  open={open}
                                  toggle={folds.toggle}
                                />
                                {open && (
                                  <RemoteServices
                                    id={item.id}
                                    name={item.boardName}
                                    activeMonitorId={
                                      activeRemoteId === item.id ? activeRemoteMonitorId : undefined
                                    }
                                  />
                                )}
                              </div>
                            )
                          })}
                        </div>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          )
        })}
      </nav>
    </section>
  )
}
