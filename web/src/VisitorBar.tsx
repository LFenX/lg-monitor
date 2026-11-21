import { useEffect, useState } from 'react'
import { Avatar } from './avatar'
import { BohemiaMark } from './bohemia'
import { BrandMark } from './brand'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago } from './kit'
import { CopyButton, Dialog } from './parts'
import { useApp } from './state'
import type { LANVisitor } from './types'
import './visitor.css'

/*
 * Shown at the bottom of the dashboard when a teammate visits over the LAN in read-only mode:
 * identifies whose board this is, offers read-only Bohemia Q&A, and presents a direct download CTA for lgmon.
 */
export function GuestVisitorBar({ openAssistant }: { openAssistant: () => void }) {
  const { guest, lan } = useApp()
  const { lang, t } = useI18n()
  if (!guest) return null
  const owner = lan?.name || lan?.hostname || 'lgmon'
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
      <a className="lm-guest-dl" href="/api/v1/download/latest" download>
        <span className="lm-guest-dl-mark">
          <BrandMark size={22} />
        </span>
        <span className="lm-guest-dl-text">
          <strong>{t('downloadTitle')}</strong>
          <small>{t('downloadSub')}</small>
        </span>
        <span className="lm-guest-dl-cta">
          <Glyph name="download" size={14} />
          {t('downloadCta')}
        </span>
      </a>
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
