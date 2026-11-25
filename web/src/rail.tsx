/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useState } from 'react'
import type { ReactNode } from 'react'
import { Avatar } from './avatar'
import { BrandMark } from './brand'
import { Glyph } from './glyphs'
import type { GlyphName } from './glyphs'
import { useI18n } from './i18n'
import { useRemoteSummaries } from './remote-cache'
import type { BoardInfo, CloudDeviceSummary, LANOutgoing } from './types'
import './rail.css'

/*
 * The sidebar folded to a strip of icons: every board, device and shared board keeps one tile with its state,
 * so a fault still shows without opening the sidebar.
 */

const key = 'lgmon-side-rail'

export function useSideRail() {
  const [rail, setRail] = useState(() => {
    try {
      return localStorage.getItem(key) === '1'
    } catch {
      return false
    }
  })
  const toggle = () =>
    setRail((value) => {
      try {
        localStorage.setItem(key, value ? '' : '1')
      } catch {
        // Without storage the choice lasts until the page closes.
      }
      return !value
    })
  return { rail, toggle }
}

export function SideToggle({ rail, toggle }: { rail: boolean; toggle: () => void }) {
  const { t } = useI18n()
  const label = t(rail ? 'sideExpand' : 'sideCollapse')
  return (
    <button type="button" className="lm-side-toggle" aria-label={label} title={label} onClick={toggle}>
      <Glyph name="sidebar" size={17} />
    </button>
  )
}

type Tone = 'up' | 'down' | 'unknown' | 'empty'

function Tile({
  href,
  label,
  current,
  tone,
  count,
  accent,
  children,
}: {
  href: string
  label: string
  current?: boolean
  tone?: Tone
  count?: number
  accent?: boolean
  children: ReactNode
}) {
  return (
    <a
      className="lm-rail-tile"
      href={href}
      aria-label={label}
      aria-current={current ? 'page' : undefined}
      title={label}
    >
      {children}
      {tone && !count && <i className="lm-rail-dot" data-tone={tone} aria-hidden="true" />}
      {count ? (
        <b className={'lm-rail-count' + (accent ? ' is-accent' : '')} aria-hidden="true">
          {count > 99 ? '99+' : count}
        </b>
      ) : null}
    </a>
  )
}

const initial = (name: string) => Array.from(name.trim())[0] || '·'

const toneOf = (state?: string): Tone =>
  state === 'up' || state === 'down' || state === 'unknown' ? state : 'empty'

export type RailPage = {
  href: string
  glyph: GlyphName
  label: string
  count?: number
  match: boolean
}

export function SideRail({
  boards,
  currentBoard,
  devices,
  activeDeviceId,
  remoteBoards,
  activeRemoteId,
  pages,
  ownDevices,
  account,
  expand,
  tools,
}: {
  boards: BoardInfo[]
  currentBoard?: string
  devices: CloudDeviceSummary[]
  activeDeviceId?: string
  remoteBoards: LANOutgoing[]
  activeRemoteId?: string
  pages: RailPage[]
  /* This computer's Devices page, which the full sidebar keeps inside its device card. */
  ownDevices?: RailPage
  account?: { label: string; tone?: Tone }
  expand: () => void
  tools: ReactNode
}) {
  const { t } = useI18n()
  const summaries = useRemoteSummaries(remoteBoards.map((item) => item.id))
  return (
    <>
      <div className="lm-rail-head">
        <a href="#/" className="lm-rail-brand" aria-label="lgmon 首页">
          <BrandMark size={28} />
        </a>
        <SideToggle rail toggle={expand} />
      </div>
      <div className="lm-rail-scroll">
        <nav className="lm-rail-nav" aria-label="面板">
          {boards.map((board) => (
            <Tile
              key={board.id}
              href={'#/boards/' + encodeURIComponent(board.id)}
              label={
                board.summary.down > 0
                  ? board.name + ' · ' + t('sideDownCount', { count: board.summary.down })
                  : board.name
              }
              current={board.id === currentBoard}
              tone={toneOf(board.summary.state)}
              count={board.summary.down}
            >
              <span className="lm-rail-letter">{initial(board.name)}</span>
            </Tile>
          ))}
          {ownDevices && (
            <Tile
              href={ownDevices.href}
              label={ownDevices.label}
              current={ownDevices.match}
              count={ownDevices.count}
            >
              <Glyph name={ownDevices.glyph} size={18} />
            </Tile>
          )}
          {devices.length > 0 && <hr />}
          {devices.map((dev) => {
            const state = dev.downCount > 0 ? t('sideDownCount', { count: dev.downCount }) : ''
            return (
              <Tile
                key={dev.id}
                href={
                  dev.boards.length
                    ? '#/cloud/' +
                      encodeURIComponent(dev.id) +
                      '/boards/' +
                      encodeURIComponent(dev.boards[0].id)
                    : '#/cloud/' + encodeURIComponent(dev.id) + '/devices'
                }
                label={[dev.name, dev.online ? t('cloudDeviceOnline') : t('cloudDeviceOffline'), state]
                  .filter(Boolean)
                  .join(' · ')}
                current={activeDeviceId === dev.id}
                tone={dev.online ? 'up' : 'empty'}
                count={dev.downCount}
              >
                <Avatar value={dev.avatar} seed={dev.id} size={26} />
              </Tile>
            )
          })}
        </nav>
        {remoteBoards.length > 0 && (
          <nav className="lm-rail-nav" aria-label="别人的面板">
            <hr />
            {remoteBoards.map((item) => {
              const entry = summaries.get(item.id)
              const summary = entry?.board?.board.summary
              const down = summary?.down || 0
              return (
                <Tile
                  key={item.id}
                  href={'#/lan/remote/' + encodeURIComponent(item.id)}
                  label={[
                    item.ownerName + ' 的「' + item.boardName + '」',
                    down > 0 ? t('sideDownCount', { count: down }) : entry?.offline ? t('sideOffline') : '',
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                  current={activeRemoteId === item.id}
                  tone={entry?.offline && !summary ? 'empty' : toneOf(summary?.state)}
                  count={down}
                >
                  <Avatar value={item.ownerAvatar} seed={item.fingerprint} size={26} />
                </Tile>
              )
            })}
          </nav>
        )}
      </div>
      <div className="lm-rail-bottom">
        <nav className="lm-rail-nav" aria-label="主导航">
          {pages.map((page) => (
            <Tile
              key={page.href}
              href={page.href}
              label={page.label}
              current={page.match}
              count={page.count}
              accent={page.href !== '#/alerts' && page.href !== '#/devices'}
            >
              <Glyph name={page.glyph} size={18} />
            </Tile>
          ))}
          {account && (
            <Tile href="#/settings/account" label={account.label} tone={account.tone}>
              <Glyph name="cloud" size={18} />
            </Tile>
          )}
        </nav>
        <div className="lm-rail-tools">{tools}</div>
      </div>
    </>
  )
}
