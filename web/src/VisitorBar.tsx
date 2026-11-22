/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useState } from 'react'
import { askAccess } from './access'
import { Avatar } from './avatar'
import { BohemiaMark } from './bohemia'
import { BrandMark } from './brand'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago } from './kit'
import { CopyButton, Dialog } from './parts'
import { useApp } from './state'
import type { GitHubStats, LANVisitor } from './types'
import './visitor.css'

const defaultRepoURL = 'https://github.com/LFenX/lg-monitor'

function GitHubMark({ size = 15 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 .5a12 12 0 0 0-3.79 23.39c.6.11.82-.26.82-.58v-2.04c-3.34.73-4.04-1.61-4.04-1.61-.55-1.39-1.33-1.76-1.33-1.76-1.09-.75.08-.73.08-.73 1.2.09 1.84 1.24 1.84 1.24 1.07 1.83 2.81 1.3 3.49 1 .11-.78.42-1.3.76-1.6-2.67-.3-5.47-1.33-5.47-5.93 0-1.31.47-2.38 1.24-3.22-.13-.3-.54-1.52.12-3.18 0 0 1.01-.32 3.3 1.23a11.5 11.5 0 0 1 6 0c2.29-1.55 3.3-1.23 3.3-1.23.66 1.66.25 2.88.12 3.18.77.84 1.24 1.91 1.24 3.22 0 4.61-2.81 5.62-5.49 5.92.43.37.82 1.1.82 2.22v3.29c0 .32.22.7.83.58A12 12 0 0 0 12 .5Z" />
    </svg>
  )
}

function formatCount(n: number) {
  if (n >= 10_000) return (n / 1000).toFixed(1) + 'k'
  return n.toLocaleString()
}

/*
 * Shown at the bottom of the dashboard when a teammate visits over the LAN in read-only mode:
 * identifies whose board this is, offers read-only Bohemia Q&A, live GitHub stats, a GitHub link,
 * and a direct download CTA for lgmon.
 */
export function GuestVisitorBar({ openAssistant }: { openAssistant: () => void }) {
  const { guest, lan } = useApp()
  const { lang, t } = useI18n()
  const [stats, setStats] = useState<GitHubStats | null>(null)

  useEffect(() => {
    if (!guest) return
    let active = true
    let lastFetched = 0
    const load = (force = false) => {
      const now = Date.now()
      if (!force && now - lastFetched < 30_000) return
      lastFetched = now
      void fetch('/api/v1/download/stats', { cache: 'no-store' })
        .then((res) => (res.ok ? (res.json() as Promise<GitHubStats>) : null))
        .then((data) => {
          if (active && data && typeof data.downloads === 'number') setStats(data)
        })
        .catch(() => undefined)
    }
    load(true)
    const timer = setInterval(() => load(true), 60_000)
    const onVisible = () => {
      if (document.visibilityState === 'visible') load(false)
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      active = false
      clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [guest])

  if (!guest) return null
  const owner = lan?.name || lan?.hostname || 'lgmon'
  const repoURL = stats?.repoUrl || defaultRepoURL
  return (
    <aside className="lm-guest-bar" aria-label={lang === 'en' ? 'LAN Visitor Banner' : '内网访客提示'}>
      <div className="lm-guest-who">
        <Avatar value={lan?.avatar} seed={lan?.fingerprint || owner} size={38} label={owner} />
        <div className="lm-guest-copy">
          <div className="lm-guest-title">
            <span>
              {t('guestViewingPrefix')} <strong>{owner}</strong> {t('guestViewingSuffix')}
            </span>
            <span className="lm-guest-badge">{t('guestReadOnly')}</span>
            <button className="lm-guest-ask" type="button" onClick={() => askAccess('admin', '', true)}>
              <Glyph name="lock" size={13} />
              {t('lanAccessUnlock')}
            </button>
          </div>
          <div className="lm-guest-sub">
            <span>{t('guestBohemiaHint')}</span>
            <button type="button" className="lm-guest-ask" onClick={openAssistant}>
              <BohemiaMark size={14} />
              {t('guestAskBohemia')}
            </button>
          </div>
        </div>
      </div>
      <div className="lm-guest-right">
        {stats && (
          <a
            className="lm-guest-stats"
            href={repoURL}
            target="_blank"
            rel="noreferrer"
            title={t('downloadGithub')}
          >
            <span className="lm-guest-stat is-dl" title={t('statsDownloads')}>
              <Glyph name="download" size={12} />
              <b>{formatCount(stats.downloads)}</b>
              <small>{t('statsDownloads')}</small>
            </span>
            <span className="lm-guest-stat" title={t('statsStars')}>
              <span aria-hidden="true">★</span>
              <b>{formatCount(stats.stars)}</b>
            </span>
            <span className="lm-guest-stat" title={t('statsForks')}>
              <span aria-hidden="true">⑂</span>
              <b>{formatCount(stats.forks)}</b>
            </span>
          </a>
        )}
        <a
          className="lm-guest-gh"
          href={repoURL}
          target="_blank"
          rel="noreferrer"
          title={t('downloadGithub')}
        >
          <GitHubMark size={15} />
          <span>{t('downloadGithub')}</span>
        </a>
        <a
          className="lm-guest-dl"
          href={
            stats?.version
              ? `/api/v1/download/latest?channel=guest&v=${encodeURIComponent(stats.version)}`
              : '/api/v1/download/latest?channel=guest'
          }
          download
        >
          <span className="lm-guest-dl-mark">
            <BrandMark size={22} />
          </span>
          <span className="lm-guest-dl-text">
            <strong>
              {t('downloadTitle')}
              {stats?.version ? ` ${stats.version}` : ''}
            </strong>
            <small>{t('downloadSub')}</small>
          </span>
          <span className="lm-guest-dl-cta">
            <Glyph name="download" size={14} />
            {t('downloadCta')}
          </span>
        </a>
      </div>
    </aside>
  )
}

/*
 * Shown at the bottom of the host owner's sidebar when LAN web viewing is enabled or visitors are online:
 * displays active visitor avatars and opens a dialog listing each visitor's IP and activity.
 */
export function HostVisitorsPill() {
  const { api, guest, lan, now } = useApp()
  const { lang, t } = useI18n()
  const [visitors, setVisitors] = useState<LANVisitor[]>([])
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (guest || !lan?.web) {
      setVisitors([])
      return
    }
    const controller = new AbortController()
    const load = () =>
      api
        .request<LANVisitor[]>('/lan/visitors', { signal: controller.signal })
        .then((items) => {
          if (!controller.signal.aborted && Array.isArray(items)) setVisitors(items)
        })
        .catch(() => undefined)
    void load()
    const timer = setInterval(() => void load(), 10_000)
    return () => {
      controller.abort()
      clearInterval(timer)
    }
  }, [api, guest, lan?.web])

  if (guest || (!lan?.web && visitors.length === 0)) return null

  const label =
    visitors.length > 0
      ? lang === 'en'
        ? `${visitors.length} ${t('visitorsOnline')}`
        : `${visitors.length} ${t('visitorsOnline')}`
      : t('visitorsNone')

  return (
    <>
      <button
        type="button"
        className="lm-visitors-pill"
        data-active={visitors.length > 0 || undefined}
        onClick={() => setOpen(true)}
        title={t('visitorsTitle')}
      >
        {visitors.length > 0 ? (
          <span className="lm-visitors-faces" aria-hidden="true">
            {visitors.slice(0, 4).map((item) => (
              <Avatar key={item.ip} value={item.avatar} seed={item.ip} size={20} />
            ))}
          </span>
        ) : (
          <span className="lm-visitors-dot" aria-hidden="true" />
        )}
        <span className="lm-visitors-label">{label}</span>
        {visitors.length > 0 && <span className="lm-visitors-count">{visitors.length}</span>}
      </button>

      {open && (
        <Dialog
          title={t('visitorsTitle')}
          close={() => setOpen(false)}
          footer={
            <button type="button" className="lm-btn" onClick={() => setOpen(false)}>
              {t('visitorsClose')}
            </button>
          }
        >
          {visitors.length === 0 ? (
            <p className="lm-muted" style={{ margin: 0 }}>
              {t('visitorsEmpty')}
            </p>
          ) : (
            <ul className="lm-visitor-list">
              {visitors.map((item) => (
                <li key={item.ip} className="lm-visitor-row">
                  <Avatar value={item.avatar} seed={item.ip} size={38} label={item.ip} />
                  <div className="lm-visitor-info">
                    <div className="lm-visitor-head">
                      <code>{item.ip}</code>
                      <span className="lm-visitor-state" data-live={item.connections > 0 || undefined}>
                        {item.connections > 0 ? t('visitorsLive') : t('visitorsRecent')}
                      </span>
                    </div>
                    <small>
                      {t('visitorsFirstSeen')} {ago(item.firstSeen, now())} · {t('visitorsLastSeen')}{' '}
                      {ago(item.lastSeen, now())}
                    </small>
                  </div>
                </li>
              ))}
            </ul>
          )}
          {lan?.webAddresses && lan.webAddresses.length > 0 && (
            <div className="lm-visitor-share">
              <p className="lm-muted lm-small" style={{ margin: '0 0 8px' }}>
                {t('visitorsShareLinks')}
              </p>
              <div className="lm-visitor-urls">
                {lan.webAddresses.map((address) => (
                  <div key={address} className="lm-code">
                    <code>{address}</code>
                    <CopyButton text={address} />
                  </div>
                ))}
              </div>
            </div>
          )}
        </Dialog>
      )}
    </>
  )
}
