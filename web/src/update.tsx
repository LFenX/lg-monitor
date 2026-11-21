/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useState } from 'react'
import { Glyph } from './glyphs'
import { inLauncher } from './launcher'
import type { Host } from './launcher'

/*
 * Updates happen in lgmon's own window: it finds a newer release, downloads and checks it, and on "重启并更新"
 * hands over to the new installer. The page only shows what the window reports and asks it to act.
 */

export type HostUpdate = {
  type: 'update'
  state: 'dev' | 'checking' | 'none' | 'available' | 'downloading' | 'ready' | 'error'
  during?: 'check' | 'download'
  message?: string
  current?: string
  version?: string
  notes?: string
  hasPage?: boolean
  size?: number
  done?: number
  total?: number
}

const host = () => window as unknown as Host

function ask(type: string) {
  host().chrome?.webview?.postMessage(JSON.stringify({ type }))
}

/* One subscription for the whole page: the card and the footer link share it, so the window is asked once. */
let latest: HostUpdate | null = null
let asked = false
const listeners = new Set<(update: HostUpdate) => void>()
function receive(event: Event) {
  const detail = (event as CustomEvent).detail
  if (detail?.type !== 'update') return
  latest = detail
  listeners.forEach((listener) => listener(detail))
}

export function useHostUpdate(): HostUpdate | null {
  const [update, setUpdate] = useState<HostUpdate | null>(latest)
  useEffect(() => {
    if (!inLauncher()) return
    if (!listeners.size) window.addEventListener('lgmon-host', receive)
    listeners.add(setUpdate)
    if (!asked) {
      asked = true
      ask('update-check')
    }
    return () => {
      listeners.delete(setUpdate)
      if (!listeners.size) {
        window.removeEventListener('lgmon-host', receive)
        asked = false
        latest = null
      }
    }
  }, [])
  return update
}

/* The card shows up only when there is something to do: a new version, its download, or a failed download. */
const actionable = (update: HostUpdate) =>
  update.state === 'available' ||
  update.state === 'downloading' ||
  update.state === 'ready' ||
  (update.state === 'error' && update.during === 'download')

/* Checking by hand lives in the footer: quiet, and it says what it found without taking the top of the window. */
export function UpdateLink() {
  const update = useHostUpdate()
  if (!update || update.state === 'dev' || actionable(update)) return null
  const failed = update.state === 'error'
  const label =
    update.state === 'checking'
      ? '正在检查更新…'
      : failed
        ? '检查更新失败，重试'
        : update.current
          ? 'v' + update.current + ' · 检查更新'
          : '检查更新'
  return (
    <button
      className="lm-start-check"
      data-state={update.state}
      disabled={update.state === 'checking'}
      title={failed ? update.message : update.state === 'none' ? '已是最新版本' : undefined}
      onClick={() => ask('update-refresh')}
    >
      {label}
    </button>
  )
}
const megabytes = (bytes = 0) => (bytes / 1e6).toFixed(1)

export function UpdateCard() {
  const update = useHostUpdate()
  if (!update || !actionable(update)) return null
  const failedDownload = update.state === 'error' && update.during === 'download'
  const hasPage = update.hasPage ?? !!update.notes
  const percent = update.total ? Math.min(100, Math.round(((update.done || 0) / update.total) * 100)) : 0
  return (
    <section className="lm-start-update" data-state={update.state} aria-label="更新">
      <span className="lm-start-update-ic" aria-hidden="true">
        <Glyph name={update.state === 'error' ? 'refresh' : 'sparkle'} size={16} />
      </span>
      <div className="lm-start-update-t">
        {update.state === 'available' && (
          <>
            <b>lgmon {update.version} 可以更新了</b>
            <small>
              {hasPage && (
                <button className="lm-start-update-link" onClick={() => ask('update-notes')}>
                  更新内容
                </button>
              )}
              {hasPage && update.size ? ' · ' : ''}
              {update.size ? '约 ' + megabytes(update.size) + ' MB' : ''}
            </small>
          </>
        )}
        {update.state === 'downloading' && (
          <>
            <b>正在下载 {update.version}</b>
            <small>
              {megabytes(update.done)} / {megabytes(update.total)} MB
            </small>
            <span className="lm-start-update-bar" role="progressbar" aria-valuenow={percent}>
              <i style={{ width: percent + '%' }} />
            </span>
          </>
        )}
        {update.state === 'ready' && (
          <>
            <b>{update.version} 已经下载好</b>
            <small>重启 lgmon 就会更新，配置和数据都保留。</small>
          </>
        )}
        {failedDownload && (
          <>
            <b>更新没有下载好</b>
            <small>{update.message}</small>
          </>
        )}
      </div>
      {update.state === 'available' && (
        <button className="lm-start-update-btn" onClick={() => ask('update-download')}>
          下载更新
        </button>
      )}
      {update.state === 'ready' && (
        <button className="lm-start-update-btn primary" onClick={() => ask('update-apply')}>
          重启并更新
        </button>
      )}
      {failedDownload && (
        <button className="lm-start-update-btn" onClick={() => ask('update-download')}>
          重试
        </button>
      )}
    </section>
  )
}
