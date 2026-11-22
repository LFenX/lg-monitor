/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useState } from 'react'
import { useGate } from './access'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { Dialog } from './parts'
import { useApp } from './state'
import type { CloudStatus } from './types'
import './cloud.css'

type AuthMode = 'otp' | 'password' | 'reset'

export function CloudAuthForm({ onSuccess }: { onSuccess?: (status: CloudStatus) => void }) {
  const { api, cloudStatus, lan, refresh } = useApp()
  const { t } = useI18n()
  const gate = useGate()

  const [mode, setMode] = useState<AuthMode>('otp')
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [deviceName, setDeviceName] = useState(cloudStatus?.deviceName || lan?.name || '')
  const [cooldown, setCooldown] = useState(0)
  const [sending, setSending] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [info, setInfo] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    if (cooldown <= 0) return
    const timer = setInterval(() => {
      setCooldown((prev) => (prev <= 1 ? 0 : prev - 1))
    }, 1000)
    return () => clearInterval(timer)
  }, [cooldown])

  const sendCode = () => {
    const trimmed = email.trim()
    if (!trimmed) {
      setError(t('cloudErrEmail'))
      return
    }
    gate('config', t('cloudSendCode'), () => {
      setSending(true)
      setError('')
      setInfo('')
      api
        .request<{ ok: boolean; cooldownSec?: number }>('/cloud/auth/otp/request', {
          method: 'POST',
          body: JSON.stringify({ email: trimmed, purpose: mode === 'reset' ? 'reset' : 'login' }),
        })
        .then((res) => {
          setCooldown(res.cooldownSec || 60)
          setInfo(t('cloudCodeSent', { email: trimmed }))
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrSendCode')))
        .finally(() => setSending(false))
    })
  }

  const handleSubmit = (event: React.FormEvent) => {
    event.preventDefault()
    const trimmedEmail = email.trim()
    if (!trimmedEmail) {
      setError(t('cloudErrEmail'))
      return
    }

    if (mode === 'reset') {
      if (code.trim().length !== 6) {
        setError(t('cloudErrCode'))
        return
      }
      if (password.length < 8) {
        setError(t('cloudErrNewPassword'))
        return
      }
      gate('config', t('cloudResetSubmit'), () => {
        setSubmitting(true)
        setError('')
        setInfo('')
        api
          .request('/cloud/auth/password/reset', {
            method: 'POST',
            body: JSON.stringify({ email: trimmedEmail, code: code.trim(), newPassword: password }),
          })
          .then(() => {
            setMode('password')
            setCode('')
            setInfo(t('cloudResetDone'))
          })
          .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrReset')))
          .finally(() => setSubmitting(false))
      })
      return
    }

    if (mode === 'otp' && code.trim().length !== 6) {
      setError(t('cloudErrCode'))
      return
    }
    if (mode === 'password' && !password) {
      setError(t('cloudErrPassword'))
      return
    }

    gate('config', t('cloudLoginSubmit'), () => {
      setSubmitting(true)
      setError('')
      setInfo('')
      const endpoint = mode === 'otp' ? '/cloud/auth/otp/verify' : '/cloud/auth/login'
      const body =
        mode === 'otp'
          ? { email: trimmedEmail, code: code.trim(), deviceName: deviceName.trim() || undefined }
          : { email: trimmedEmail, password, deviceName: deviceName.trim() || undefined }
      api
        .request<CloudStatus>(endpoint, { method: 'POST', body: JSON.stringify(body) })
        .then((status) => {
          refresh()
          onSuccess?.(status)
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrLogin')))
        .finally(() => setSubmitting(false))
    })
  }

  return (
    <form className="lm-cloud-auth" onSubmit={handleSubmit}>
      {mode !== 'reset' && (
        <div className="lm-cloud-tabs" role="tablist" aria-label={t('cloudLoginTitle')}>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'otp'}
            onClick={() => {
              setMode('otp')
              setError('')
            }}
          >
            {t('cloudTabOtp')}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'password'}
            onClick={() => {
              setMode('password')
              setError('')
            }}
          >
            {t('cloudTabPassword')}
          </button>
        </div>
      )}

      <div className="lm-cloud-field">
        <label htmlFor="lm-cloud-email">{t('cloudEmailLabel')}</label>
        <input
          id="lm-cloud-email"
          type="email"
          className="lm-input"
          autoComplete="email"
          placeholder={t('cloudEmailPlaceholder')}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
      </div>

      {(mode === 'otp' || mode === 'reset') && (
        <div className="lm-cloud-field">
          <label htmlFor="lm-cloud-code">{t('cloudCodeLabel')}</label>
          <div className="lm-cloud-code-row">
            <input
              id="lm-cloud-code"
              type="text"
              inputMode="numeric"
              maxLength={6}
              className="lm-input lm-cloud-code-input"
              placeholder={t('cloudCodePlaceholder')}
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D+/g, '').slice(0, 6))}
            />
            <button
              type="button"
              className="lm-btn"
              disabled={sending || cooldown > 0 || !email.trim()}
              onClick={sendCode}
            >
              {sending
                ? t('cloudSendingCode')
                : cooldown > 0
                  ? t('cloudResendIn', { sec: cooldown })
                  : t('cloudSendCode')}
            </button>
          </div>
        </div>
      )}

      {(mode === 'password' || mode === 'reset') && (
        <div className="lm-cloud-field">
          <label htmlFor="lm-cloud-password">
            {mode === 'reset' ? t('cloudNewPasswordLabel') : t('cloudPasswordLabel')}
          </label>
          <input
            id="lm-cloud-password"
            type="password"
            className="lm-input"
            autoComplete={mode === 'reset' ? 'new-password' : 'current-password'}
            placeholder={t('cloudPasswordPlaceholder')}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
      )}

      {mode !== 'reset' && (
        <div className="lm-cloud-field">
          <label htmlFor="lm-cloud-devname">{t('cloudDeviceNameLabel')}</label>
          <input
            id="lm-cloud-devname"
            type="text"
            maxLength={40}
            className="lm-input"
            placeholder={t('cloudDeviceNamePlaceholder')}
            value={deviceName}
            onChange={(e) => setDeviceName(e.target.value)}
          />
        </div>
      )}

      {info && (
        <p className="lm-note" data-tone="info" style={{ margin: 0 }}>
          {info}
        </p>
      )}
      {error && (
        <p className="lm-error" style={{ margin: 0 }}>
          {error}
        </p>
      )}

      <div className="lm-cloud-actions">
        {mode === 'password' ? (
          <button
            type="button"
            className="lm-cloud-link"
            onClick={() => {
              setMode('reset')
              setError('')
              setInfo('')
            }}
          >
            {t('cloudForgotPassword')}
          </button>
        ) : mode === 'reset' ? (
          <button
            type="button"
            className="lm-cloud-link"
            onClick={() => {
              setMode('otp')
              setError('')
              setInfo('')
            }}
          >
            {t('cloudBackToLogin')}
          </button>
        ) : (
          <span />
        )}
        <button type="submit" className="lm-btn primary" disabled={submitting}>
          {submitting
            ? t('cloudLoggingIn')
            : mode === 'reset'
              ? t('cloudResetSubmit')
              : t('cloudLoginSubmit')}
        </button>
      </div>

      <p className="lm-cloud-note">{t('cloudLoginNote')}</p>
    </form>
  )
}

export function CloudLoginDialog({ close }: { close: () => void }) {
  const { t } = useI18n()
  return (
    <Dialog title={t('cloudLoginTitle')} close={close}>
      <CloudAuthForm onSuccess={close} />
    </Dialog>
  )
}

/* Compact sidebar bottom dock pill: one click to sign in, or at-a-glance email & device count when signed in. */
export function CloudAccountPill() {
  const { cloudStatus, cloudDevices, guest } = useApp()
  const { t } = useI18n()
  const [open, setOpen] = useState(false)

  if (guest || !cloudStatus) return null

  if (cloudStatus.loggedIn) {
    const count = Math.max(cloudDevices.length, cloudStatus.devicesCount || 1)
    const dotClass = cloudStatus.lastError
      ? 'lm-cloud-pill-dot is-err'
      : cloudStatus.paused
        ? 'lm-cloud-pill-dot is-paused'
        : 'lm-cloud-pill-dot'
    return (
      <a
        href="#/settings/account"
        className="lm-cloud-pill is-signed"
        title={cloudStatus.lastError || `${cloudStatus.email} · ${t('cloudDevicesCount', { count })}`}
      >
        <span className={dotClass} aria-hidden="true" />
        <span className="lm-cloud-pill-label">{cloudStatus.email}</span>
        <span className="lm-cloud-pill-badge">{t('cloudDevicesCount', { count })}</span>
      </a>
    )
  }

  return (
    <>
      <button
        type="button"
        className="lm-cloud-pill"
        onClick={() => setOpen(true)}
        title={t('cloudLoginNote')}
      >
        <Glyph name="cloud" size={14} />
        <span className="lm-cloud-pill-label">{t('cloudLoginShort')}</span>
      </button>
      {open && <CloudLoginDialog close={() => setOpen(false)} />}
    </>
  )
}
