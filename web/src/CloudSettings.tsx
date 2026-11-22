/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useState } from 'react'
import { useGate } from './access'
import { Avatar } from './avatar'
import { CloudAuthForm } from './CloudAuth'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago } from './kit'
import { Dialog } from './parts'
import { Group, Row } from './settings-rows'
import { useApp } from './state'
import type { CloudDeviceSummary } from './types'
import './cloud.css'

function formatBytes(n: number) {
  if (!n || n < 1024) return `${Math.max(0, n || 0)} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(2)} MB`
}

function PasswordDialog({
  hasPassword,
  close,
  done,
}: {
  hasPassword: boolean
  close: () => void
  done: () => void
}) {
  const { api } = useApp()
  const { t } = useI18n()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (next.length < 8) {
      setError(t('cloudErrNewPassword'))
      return
    }
    setBusy(true)
    setError('')
    api
      .request('/cloud/auth/password', {
        method: 'POST',
        body: JSON.stringify({ currentPassword: current, newPassword: next }),
      })
      .then(() => {
        done()
        close()
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrSave')))
      .finally(() => setBusy(false))
  }

  return (
    <Dialog
      title={hasPassword ? t('cloudChangePasswordBtn') : t('cloudSetPasswordBtn')}
      close={close}
      footer={
        <>
          <button type="button" className="lm-btn" onClick={close}>
            {t('cancel')}
          </button>
          <button type="submit" form="lm-cloud-pwd-form" className="lm-btn primary" disabled={busy}>
            {busy ? t('saving') : t('save')}
          </button>
        </>
      }
    >
      <form id="lm-cloud-pwd-form" className="lm-cloud-auth" onSubmit={submit}>
        {hasPassword && (
          <div className="lm-cloud-field">
            <label htmlFor="lm-pwd-cur">{t('cloudCurrentPasswordLabel')}</label>
            <input
              id="lm-pwd-cur"
              type="password"
              className="lm-input"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              required
            />
          </div>
        )}
        <div className="lm-cloud-field">
          <label htmlFor="lm-pwd-new">{t('cloudNewPasswordLabel')}</label>
          <input
            id="lm-pwd-new"
            type="password"
            className="lm-input"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            minLength={8}
            required
          />
        </div>
        {error && (
          <p className="lm-error" style={{ margin: 0 }}>
            {error}
          </p>
        )}
      </form>
    </Dialog>
  )
}

export function CloudSettings() {
  const { api, cloudStatus, cloudDevices, now, refresh } = useApp()
  const { t } = useI18n()
  const gate = useGate()

  // null until edited; an empty draft saves as "use the built-in server".
  const [serverDraft, setServerDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [pwdOpen, setPwdOpen] = useState(false)
  const [logoutOpen, setLogoutOpen] = useState(false)
  const [unbindOnLogout, setUnbindOnLogout] = useState(false)
  const [renaming, setRenaming] = useState<CloudDeviceSummary | null>(null)
  const [renameDraft, setRenameDraft] = useState('')
  const [unbinding, setUnbinding] = useState<CloudDeviceSummary | null>(null)

  const syncNow = () =>
    gate('config', t('cloudSyncNow'), () => {
      setBusy(true)
      setError('')
      setMessage('')
      api
        .request('/cloud/sync', { method: 'POST' })
        .then(() => {
          refresh()
          setMessage(t('cloudSyncDone'))
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrSync')))
        .finally(() => setBusy(false))
    })

  const togglePause = () => {
    if (!cloudStatus) return
    const nextPaused = !cloudStatus.paused
    gate('config', nextPaused ? t('cloudPauseSync') : t('cloudResumeSync'), () => {
      setBusy(true)
      setError('')
      api
        .request('/cloud/settings', { method: 'PUT', body: JSON.stringify({ paused: nextPaused }) })
        .then(() => refresh())
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrSettings')))
        .finally(() => setBusy(false))
    })
  }

  const saveServer = () =>
    gate('config', t('cloudServerLabel'), () => {
      setBusy(true)
      setError('')
      setMessage('')
      api
        .request('/cloud/settings', {
          method: 'PUT',
          body: JSON.stringify({ serverUrl: (serverDraft ?? cloudStatus?.serverUrl ?? '').trim() }),
        })
        .then(() => {
          setServerDraft(null)
          refresh()
          setMessage(t('cloudServerSaved'))
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrSave')))
        .finally(() => setBusy(false))
    })

  const doLogout = () =>
    gate('config', t('cloudLogoutBtn'), () => {
      setBusy(true)
      setError('')
      api
        .request('/cloud/auth/logout', {
          method: 'POST',
          body: JSON.stringify({ unbindDevice: unbindOnLogout }),
        })
        .then(() => {
          setLogoutOpen(false)
          setUnbindOnLogout(false)
          refresh()
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrLogout')))
        .finally(() => setBusy(false))
    })

  const doRename = (event: React.FormEvent) => {
    event.preventDefault()
    if (!renaming || !renameDraft.trim()) return
    gate('config', t('cloudDeviceRename'), () => {
      setBusy(true)
      setError('')
      api
        .request('/cloud/devices/' + encodeURIComponent(renaming.id), {
          method: 'PUT',
          body: JSON.stringify({ name: renameDraft.trim() }),
        })
        .then(() => {
          setRenaming(null)
          refresh()
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrRename')))
        .finally(() => setBusy(false))
    })
  }

  const doUnbind = () => {
    if (!unbinding) return
    gate('config', t('cloudDeviceUnbind'), () => {
      setBusy(true)
      setError('')
      api
        .request('/cloud/devices/' + encodeURIComponent(unbinding.id), { method: 'DELETE' })
        .then(() => {
          setUnbinding(null)
          refresh()
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrUnbind')))
        .finally(() => setBusy(false))
    })
  }

  const loggedIn = !!cloudStatus?.loggedIn
  const lastSyncAt = Math.max(cloudStatus?.lastPushAt || 0, cloudStatus?.lastPullAt || 0)

  return (
    <>
      {!loggedIn ? (
        <Group title={t('cloudLoginTitle')}>
          <div style={{ padding: '14px 16px' }}>
            {cloudStatus?.lastError && (
              <p className="lm-note" data-tone="down" style={{ margin: '0 0 12px' }}>
                {cloudStatus.lastError}
              </p>
            )}
            <CloudAuthForm onSuccess={() => refresh()} />
          </div>
        </Group>
      ) : (
        <>
          <div className="lm-cloud-card">
            <div className="lm-cloud-account-head">
              <div className="lm-cloud-identity">
                <span className="lm-badge" style={{ width: 38, height: 38, justifyContent: 'center' }}>
                  <Glyph name="mail" size={18} />
                </span>
                <div className="lm-cloud-identity-copy">
                  <strong>{cloudStatus.email}</strong>
                  <div className="lm-cloud-identity-meta">
                    <span className={'lm-cloud-tag ' + (cloudStatus.hasPassword ? 'is-up' : '')}>
                      {cloudStatus.hasPassword ? t('cloudPasswordSetTag') : t('cloudPasswordUnsetTag')}
                    </span>
                    {cloudStatus.paused && (
                      <span className="lm-cloud-tag is-warn">{t('cloudPausedTag')}</span>
                    )}
                    <span>{cloudStatus.deviceName}</span>
                  </div>
                </div>
              </div>
              <div className="lm-actions">
                <button type="button" className="set-btn" disabled={busy} onClick={syncNow}>
                  <Glyph name="refresh" size={14} />
                  {busy || cloudStatus.syncing ? t('cloudSyncing') : t('cloudSyncNow')}
                </button>
                <button type="button" className="set-btn" disabled={busy} onClick={togglePause}>
                  {cloudStatus.paused ? t('cloudResumeSync') : t('cloudPauseSync')}
                </button>
                <button
                  type="button"
                  className="set-btn"
                  onClick={() =>
                    gate(
                      'config',
                      cloudStatus.hasPassword ? t('cloudChangePasswordBtn') : t('cloudSetPasswordBtn'),
                      () => setPwdOpen(true),
                    )
                  }
                >
                  <Glyph name="key" size={14} />
                  {cloudStatus.hasPassword ? t('cloudChangePasswordBtn') : t('cloudSetPasswordBtn')}
                </button>
                <button type="button" className="set-btn danger" onClick={() => setLogoutOpen(true)}>
                  {t('cloudLogoutBtn')}
                </button>
              </div>
            </div>
            {cloudStatus.lastError && (
              <p className="lm-error" style={{ margin: 0 }}>
                {cloudStatus.lastError}
              </p>
            )}
            {message && (
              <p className="lm-note" data-tone="info" style={{ margin: 0 }}>
                {message}
              </p>
            )}
            {error && (
              <p className="lm-error" style={{ margin: 0 }}>
                {error}
              </p>
            )}
          </div>

          <Group title={t('cloudDevicesTitle')}>
            <div style={{ padding: '12px 16px' }}>
              {cloudDevices.length === 0 ? (
                <p className="lm-muted lm-small" style={{ margin: 0 }}>
                  {t('cloudNeverSynced')}
                </p>
              ) : (
                <ul className="lm-cloud-device-list" aria-label={t('cloudDevicesTitle')}>
                  {cloudDevices.map((dev) => (
                    <li key={dev.id} className="lm-cloud-device-row">
                      <div className="lm-cloud-device-main">
                        <Avatar value={dev.avatar} seed={dev.id} size={32} />
                        <div className="lm-cloud-device-text">
                          <div className="lm-cloud-device-title">
                            <strong>{dev.name}</strong>
                            {dev.isCurrent && (
                              <span className="lm-cloud-tag is-accent">{t('cloudDeviceThis')}</span>
                            )}
                            <span className={'lm-cloud-tag ' + (dev.online ? 'is-up' : '')}>
                              {dev.online ? t('cloudDeviceOnline') : t('cloudDeviceOffline')}
                            </span>
                          </div>
                          <span className="lm-cloud-device-sub">
                            {t('sideBoardCount', { count: dev.boards.length })} ·{' '}
                            {t('sideServiceCount', { count: dev.totalCount })}
                            {dev.downCount > 0 ? ` · ${t('sideDownCount', { count: dev.downCount })}` : ''}
                            {/* 304 pulls leave lastSeen as it was, so it only means something once offline. */}
                            {!dev.online && dev.lastSeen ? ` · ${ago(dev.lastSeen, now())}` : ''}
                            {dev.version ? ` · v${dev.version.replace(/^v/i, '')}` : ''}
                          </span>
                        </div>
                      </div>
                      <div className="lm-actions">
                        <button
                          type="button"
                          className="lm-btn small ghost"
                          onClick={() => {
                            setRenaming(dev)
                            setRenameDraft(dev.name)
                          }}
                        >
                          {t('cloudDeviceRename')}
                        </button>
                        <button
                          type="button"
                          className="lm-btn small ghost danger"
                          onClick={() => setUnbinding(dev)}
                        >
                          {t('cloudDeviceUnbind')}
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </Group>
        </>
      )}

      <Group title={t('cloudServerLabel')} note={t('cloudServerHint')}>
        <Row
          title={t('cloudLastSyncLabel')}
          hint={lastSyncAt ? ago(lastSyncAt, now()) : t('cloudNeverSynced')}
        >
          {cloudStatus?.stats && (
            <span className="lm-cloud-stats-line">
              {t('cloudTrafficSummary', {
                sent: formatBytes(cloudStatus.stats.bytesSent),
                recv: formatBytes(cloudStatus.stats.bytesReceived),
                pushes: cloudStatus.stats.pushCount,
                beats: cloudStatus.stats.heartbeatCount,
                docs: cloudStatus.stats.docUploads,
              })}
            </span>
          )}
        </Row>
        <div style={{ padding: '12px 16px' }}>
          <div className="lm-cloud-server-row">
            <input
              type="url"
              className="lm-input"
              aria-label={t('cloudServerLabel')}
              placeholder="https://sync.lfen.ink"
              value={serverDraft ?? cloudStatus?.serverUrl ?? ''}
              onChange={(e) => setServerDraft(e.target.value)}
            />
            <button type="button" className="lm-btn" disabled={busy} onClick={saveServer}>
              {t('save')}
            </button>
          </div>
        </div>
      </Group>

      {pwdOpen && cloudStatus && (
        <PasswordDialog
          hasPassword={!!cloudStatus.hasPassword}
          close={() => setPwdOpen(false)}
          done={() => {
            setMessage(t('cloudPasswordSaved'))
            refresh()
          }}
        />
      )}

      {logoutOpen && (
        <Dialog
          title={t('cloudLogoutConfirmTitle')}
          close={() => setLogoutOpen(false)}
          footer={
            <>
              <button type="button" className="lm-btn" onClick={() => setLogoutOpen(false)}>
                {t('cancel')}
              </button>
              <button type="button" className="lm-btn danger solid" disabled={busy} onClick={doLogout}>
                {t('cloudLogoutBtn')}
              </button>
            </>
          }
        >
          <p style={{ margin: '0 0 12px' }}>{t('cloudLogoutConfirmBody')}</p>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
            <input
              type="checkbox"
              checked={unbindOnLogout}
              onChange={(e) => setUnbindOnLogout(e.target.checked)}
            />
            <span>{t('cloudLogoutUnbindCheck')}</span>
          </label>
        </Dialog>
      )}

      {renaming && (
        <Dialog
          title={t('cloudDeviceRename')}
          close={() => setRenaming(null)}
          footer={
            <>
              <button type="button" className="lm-btn" onClick={() => setRenaming(null)}>
                {t('cancel')}
              </button>
              <button type="submit" form="lm-cloud-rename-form" className="lm-btn primary" disabled={busy}>
                {t('save')}
              </button>
            </>
          }
        >
          <form id="lm-cloud-rename-form" onSubmit={doRename}>
            <input
              className="lm-input"
              maxLength={40}
              value={renameDraft}
              onChange={(e) => setRenameDraft(e.target.value)}
              autoFocus
              required
            />
          </form>
        </Dialog>
      )}

      {unbinding && (
        <Dialog
          title={t('cloudDeviceUnbindConfirm', { name: unbinding.name })}
          close={() => setUnbinding(null)}
          footer={
            <>
              <button type="button" className="lm-btn" onClick={() => setUnbinding(null)}>
                {t('cancel')}
              </button>
              <button type="button" className="lm-btn danger solid" disabled={busy} onClick={doUnbind}>
                {t('cloudDeviceUnbind')}
              </button>
            </>
          }
        >
          <p style={{ margin: 0 }}>{t('cloudDeviceUnbindBody')}</p>
        </Dialog>
      )}
    </>
  )
}
