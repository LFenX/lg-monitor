/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useRef, useState } from 'react'
import { useGate } from './access'
import { HTTPWarning } from './HTTPWarning'
import { useI18n } from './i18n'
import { CopyButton, Dialog } from './parts'
import { useApp, useResource } from './state'

type KeyStatus = {
  recoverable?: boolean
  onHost: boolean
  configured: boolean
  active: boolean
  expiresAt: number
}

export function LANAccessSettings({ tick }: { tick: number }) {
  const { api, now } = useApp()
  const { lang, t } = useI18n()
  const gate = useGate()
  const status = useResource((signal) => api.request<KeyStatus>('/lan/access-key', { signal }), [api, tick])
  const [mode, setMode] = useState<'generate' | 'expiry' | 'revoke' | 'copy' | null>(null)
  const [duration, setDuration] = useState('86400'),
    [custom, setCustom] = useState('')
  const [key, setKey] = useState(''),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  const close = () => {
    if (!busy) {
      setMode(null)
      setKey('')
      setError('')
    }
  }
  const current = status.value
  if (!current?.onHost)
    return status.error ? (
      <p className="lm-error" role="alert">
        {status.error}
      </p>
    ) : null
  const configured = current.configured
  const expired = configured && current.expiresAt > 0 && current.expiresAt <= now()
  const actionLabel = configured ? t('lanAccessRotate') : t('lanAccessGenerate')
  const save = async () => {
    const expiresAt =
      duration === 'custom'
        ? new Date(custom).getTime()
        : duration === '0'
          ? 0
          : now() + Number(duration) * 1000
    if (duration === 'custom' && (!Number.isFinite(expiresAt) || expiresAt <= now())) {
      setError(t('lanAccessInvalidTime'))
      return
    }
    setBusy(true)
    setError('')
    try {
      const body = JSON.stringify({ expiresAt })
      if (mode === 'expiry') {
        await api.request('/lan/access-key', { method: 'PUT', body })
        setMode(null)
      } else {
        const result = await api.request<{ key: string }>('/lan/access-key', { method: 'POST', body })
        setKey(result.key)
        setMode('copy')
      }
      status.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('lanAccessFailed'))
    } finally {
      setBusy(false)
    }
  }
  const reveal = async () => {
    setBusy(true)
    setError('')
    try {
      const result = await api.request<{ key: string }>('/lan/access-key/reveal', { method: 'POST' })
      setKey(result.key)
      setMode('copy')
    } catch (err) {
      setError(err instanceof Error ? err.message : t('lanAccessFailed'))
    } finally {
      setBusy(false)
    }
  }
  const revoke = async () => {
    setBusy(true)
    setError('')
    try {
      await api.request('/lan/access-key', { method: 'DELETE' })
      setMode(null)
      setKey('')
      status.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('lanAccessFailed'))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="lm-section" aria-label={t('lanAccessTitle')}>
      <header>
        <h2 className="lm-h2">{t('lanAccessTitle')}</h2>
        <small>
          {!configured
            ? t('lanAccessUnset')
            : expired || !current.active
              ? t('lanAccessExpired')
              : current.expiresAt
                ? t('lanAccessUntil') + ' ' + new Date(current.expiresAt).toLocaleString(lang)
                : t('lanAccessForever')}
        </small>
      </header>
      <p className="lm-muted lm-small">{t('lanAccessOwnerHint')}</p>
      {!mode && error && (
        <p role="alert" className="lm-error">
          {error}
        </p>
      )}
      {configured && current.active && current.recoverable === false && (
        <p className="lm-muted lm-small">{t('lanAccessLegacyKey')}</p>
      )}
      <div className="lm-actions">
        {current.recoverable && current.active && (
          <button
            className="lm-btn small"
            disabled={busy}
            onClick={() => gate('admin', t('lanAccessShow'), () => void reveal())}
          >
            {t('lanAccessShow')}
          </button>
        )}
        <button
          className="lm-btn small"
          onClick={() => gate('admin', actionLabel, () => setMode('generate'))}
        >
          {actionLabel}
        </button>
        {configured && !expired && current.active && (
          <button
            className="lm-btn small"
            onClick={() => gate('admin', t('lanAccessEditExpiry'), () => setMode('expiry'))}
          >
            {t('lanAccessEditExpiry')}
          </button>
        )}
        {configured && (
          <button
            className="lm-btn small"
            onClick={() => gate('admin', t('lanAccessRevoke'), () => setMode('revoke'))}
          >
            {t('lanAccessRevoke')}
          </button>
        )}
      </div>
      {(mode === 'generate' || mode === 'expiry') && (
        <Dialog
          title={mode === 'expiry' ? t('lanAccessEditExpiry') : actionLabel}
          close={close}
          footer={
            <button form="lan-key-settings" className="lm-btn primary" disabled={busy}>
              {mode === 'expiry' ? t('save') : actionLabel}
            </button>
          }
        >
          <form
            className="lm-lan-key-form"
            id="lan-key-settings"
            onSubmit={(event) => {
              event.preventDefault()
              void save()
            }}
          >
            <label>
              {t('lanAccessValidity')}
              <select className="lm-input" value={duration} onChange={(e) => setDuration(e.target.value)}>
                <option value="3600">{t('lanAccessHour')}</option>
                <option value="86400">{t('lanAccessDay')}</option>
                <option value="604800">{t('lanAccessWeek')}</option>
                <option value="2592000">{t('lanAccessMonth')}</option>
                <option value="custom">{t('lanAccessCustom')}</option>
                <option value="0">{t('lanAccessForever')}</option>
              </select>
            </label>
            {duration === 'custom' && (
              <label>
                {t('lanAccessCustom')}
                <input
                  className="lm-input"
                  type="datetime-local"
                  value={custom}
                  required
                  onChange={(e) => setCustom(e.target.value)}
                />
              </label>
            )}
            {mode === 'generate' && configured && <p>{t('lanAccessRotateHint')}</p>}
            {error && (
              <p className="lm-error" role="alert">
                {error}
              </p>
            )}
          </form>
        </Dialog>
      )}
      {mode === 'copy' && (
        <Dialog title={t('lanAccessTitle')} close={close}>
          <p>{t('lanAccessCopyHint')}</p>
          <div className="lm-code">
            <code>{key}</code>
            <CopyButton text={key} />
          </div>
        </Dialog>
      )}
      {mode === 'revoke' && (
        <Dialog title={t('lanAccessRevoke')} close={close}>
          <p>{t('lanAccessRevokeHint')}</p>
          {error && (
            <p className="lm-error" role="alert">
              {error}
            </p>
          )}
          <button className="lm-btn primary" disabled={busy} onClick={() => void revoke()}>
            {t('lanAccessRevoke')}
          </button>
        </Dialog>
      )}
    </section>
  )
}

export function LANUnlockDialog({ close, unlock }: { close: () => void; unlock: (session: string) => void }) {
  const { api } = useApp()
  const { t } = useI18n()
  const [key, setKey] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  const submit = async () => {
    if (pending.current) return
    const controller = new AbortController()
    pending.current = controller
    setBusy(true)
    setError('')
    try {
      const result = await api.request<{ token: string }>('/lan/unlock', {
        method: 'POST',
        signal: controller.signal,
        body: JSON.stringify({ key: key.trim() }),
      })
      if (!controller.signal.aborted) unlock(result.token)
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : t('lanAccessFailed'))
    } finally {
      pending.current = null
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  return (
    <Dialog title={t('lanAccessUnlock')} close={close}>
      <p>{t('lanAccessGuestHint')}</p>
      <HTTPWarning />
      <form
        className="lm-lan-key-form"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
      >
        <input
          className="lm-input"
          type="password"
          aria-label={t('lanAccessTitle')}
          placeholder={t('lanAccessTitle')}
          autoComplete="off"
          spellCheck={false}
          required
          maxLength={256}
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
        {error && (
          <p className="lm-error" role="alert">
            {error}
          </p>
        )}
        <button className="lm-btn primary" disabled={busy || !key.trim()}>
          {t('lanAccessUnlock')}
        </button>
      </form>
    </Dialog>
  )
}
