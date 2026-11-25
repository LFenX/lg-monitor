/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useState } from 'react'
import { useGate } from './access'
import { APIError } from './api'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import type { MessageKey } from './locales/zh'
import { Dialog } from './parts'
import { useApp } from './state'
import type { CloudStatus } from './types'
import './cloud.css'

/* A new address registers once; after that every computer signs in with a code or the password. */
type View = 'login' | 'register' | 'reset'
type Method = 'otp' | 'password'

const errorCode = (err: unknown) => (err instanceof APIError ? err.code : '')

export function CloudAuthForm({ onSuccess }: { onSuccess?: (status: CloudStatus) => void }) {
  const { api, cloudStatus, lan, refresh } = useApp()
  const { lang, t } = useI18n()
  const gate = useGate()

  const [view, setView] = useState<View>('login')
  const [method, setMethod] = useState<Method>('otp')
  const [email, setEmail] = useState(cloudStatus?.lastEmail || '')
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [deviceName, setDeviceName] = useState(cloudStatus?.deviceName || lan?.name || '')
  const [remember, setRemember] = useState(true)
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

  const go = (next: View, note = '') => {
    setView(next)
    setCode('')
    setPassword('')
    setError('')
    setInfo(note)
  }

  // The service says which side of the switch an address belongs on; follow it and keep what was typed.
  const redirected = (err: unknown) => {
    const reason = errorCode(err)
    if (reason === 'account_not_found' && view !== 'register') {
      go('register', t('cloudNotRegistered'))
      return true
    }
    if (reason === 'account_exists' && view === 'register') {
      setMethod('otp')
      go('login', t('cloudAlreadyRegistered'))
      return true
    }
    return false
  }

  const sendCode = () => {
    const trimmed = email.trim()
    if (!trimmed) {
      setError(t('cloudErrEmail'))
      return
    }
    const purpose = view === 'register' ? 'register' : view === 'reset' ? 'reset' : 'login'
    gate('config', t('cloudSendCode'), () => {
      setSending(true)
      setError('')
      setInfo('')
      api
        .request<{ ok: boolean; cooldownSec?: number }>('/cloud/auth/otp/request', {
          method: 'POST',
          body: JSON.stringify({ email: trimmed, purpose, lang }),
        })
        .then((res) => {
          setCooldown(res.cooldownSec || 60)
          setInfo(t(view === 'register' ? 'cloudCodeSentRegister' : 'cloudCodeSent', { email: trimmed }))
        })
        .catch((err: unknown) => {
          if (!redirected(err)) setError(err instanceof Error ? err.message : t('cloudErrSendCode'))
        })
        .finally(() => setSending(false))
    })
  }

  const submit = (label: string, endpoint: string, body: Record<string, unknown>, fallback: string) =>
    gate('config', label, () => {
      setSubmitting(true)
      setError('')
      setInfo('')
      api
        .request<CloudStatus>(endpoint, { method: 'POST', body: JSON.stringify(body) })
        .then((status) => {
          refresh()
          onSuccess?.(status)
        })
        .catch((err: unknown) => {
          if (!redirected(err)) setError(err instanceof Error ? err.message : fallback)
        })
        .finally(() => setSubmitting(false))
    })

  const handleSubmit = (event: React.FormEvent) => {
    event.preventDefault()
    const trimmedEmail = email.trim()
    const trimmedCode = code.trim()
    const name = deviceName.trim() || undefined
    if (!trimmedEmail) {
      setError(t('cloudErrEmail'))
      return
    }
    if ((view !== 'login' || method === 'otp') && trimmedCode.length !== 6) {
      setError(t('cloudErrCode'))
      return
    }

    if (view === 'reset') {
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
            body: JSON.stringify({ email: trimmedEmail, code: trimmedCode, newPassword: password }),
          })
          .then(() => {
            setMethod('password')
            go('login', t('cloudResetDone'))
          })
          .catch((err: unknown) => setError(err instanceof Error ? err.message : t('cloudErrReset')))
          .finally(() => setSubmitting(false))
      })
      return
    }

    if (view === 'register') {
      if (password && password.length < 8) {
        setError(t('cloudErrNewPassword'))
        return
      }
      submit(
        t('cloudRegisterSubmit'),
        '/cloud/auth/register',
        {
          email: trimmedEmail,
          code: trimmedCode,
          password: password || undefined,
          deviceName: name,
          remember,
        },
        t('cloudErrRegister'),
      )
      return
    }

    if (method === 'password' && !password) {
      setError(t('cloudErrPassword'))
      return
    }
    submit(
      t('cloudLoginSubmit'),
      method === 'otp' ? '/cloud/auth/otp/verify' : '/cloud/auth/login',
      method === 'otp'
        ? { email: trimmedEmail, code: trimmedCode, deviceName: name, remember }
        : { email: trimmedEmail, password, deviceName: name, remember },
      t('cloudErrLogin'),
    )
  }

  // The device record this computer keeps belongs to the account it last signed in to.
  const lastEmail = cloudStatus?.lastEmail || ''
  const known =
    view === 'login' && lastEmail && email.trim().toLowerCase() === lastEmail.toLowerCase() ? lastEmail : ''
  const needsCode = view !== 'login' || method === 'otp'
  const needsPassword = view === 'reset' || view === 'register' || method === 'password'
  const submitLabel = submitting
    ? view === 'register'
      ? t('cloudRegistering')
      : t('cloudLoggingIn')
    : view === 'register'
      ? t('cloudRegisterSubmit')
      : view === 'reset'
        ? t('cloudResetSubmit')
        : t('cloudLoginSubmit')

  return (
    <form className="lm-cloud-auth" onSubmit={handleSubmit}>
      {view === 'reset' ? (
        <p className="lm-cloud-view-title">{t('cloudResetTitle')}</p>
      ) : (
        <div className="lm-cloud-tabs" role="tablist" aria-label={t('cloudLoginTitle')}>
          <button
            type="button"
            role="tab"
            aria-selected={view === 'login'}
            onClick={() => view !== 'login' && go('login')}
          >
            {t('cloudTabLogin')}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={view === 'register'}
            onClick={() => view !== 'register' && go('register')}
          >
            {t('cloudTabRegister')}
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

      {needsCode && (
        <div className="lm-cloud-field">
          <label htmlFor="lm-cloud-code">{t('cloudCodeLabel')}</label>
          <div className="lm-cloud-code-row">
            <input
              id="lm-cloud-code"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
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

      {needsPassword && (
        <div className="lm-cloud-field">
          <label htmlFor="lm-cloud-password">
            {view === 'register'
              ? t('cloudRegisterPasswordLabel')
              : view === 'reset'
                ? t('cloudNewPasswordLabel')
                : t('cloudPasswordLabel')}
          </label>
          <input
            id="lm-cloud-password"
            type="password"
            className="lm-input"
            autoComplete={view === 'login' ? 'current-password' : 'new-password'}
            placeholder={
              view === 'register' ? t('cloudRegisterPasswordPlaceholder') : t('cloudPasswordPlaceholder')
            }
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
      )}

      {view !== 'reset' && (
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
          <p className="lm-cloud-hint">
            {known ? t('cloudDeviceKnownNote', { email: known }) : t('cloudDeviceAutoNote')}
          </p>
        </div>
      )}

      {view !== 'reset' && (
        <label className="lm-cloud-remember">
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          <span>
            <b>{t('cloudRemember')}</b>
            <small>{t(remember ? 'cloudRememberOn' : 'cloudRememberOff')}</small>
          </span>
        </label>
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
        <span className="lm-cloud-links">
          {view === 'login' && (
            <button
              type="button"
              className="lm-cloud-link"
              onClick={() => {
                setMethod(method === 'otp' ? 'password' : 'otp')
                setError('')
                setInfo('')
              }}
            >
              {method === 'otp' ? t('cloudUsePassword') : t('cloudUseCode')}
            </button>
          )}
          {view === 'login' && method === 'password' && (
            <button type="button" className="lm-cloud-link" onClick={() => go('reset')}>
              {t('cloudForgotPassword')}
            </button>
          )}
          {view === 'reset' && (
            <button type="button" className="lm-cloud-link" onClick={() => go('login')}>
              {t('cloudBackToLogin')}
            </button>
          )}
        </span>
        <button type="submit" className="lm-btn primary" disabled={submitting}>
          {submitLabel}
        </button>
      </div>

      <p className="lm-cloud-note">{view === 'register' ? t('cloudRegisterNote') : t('cloudLoginNote')}</p>
    </form>
  )
}

/* What signing in did to the account's devices; an older sync service does not say. */
export function bindingMessage(status: CloudStatus): MessageKey | undefined {
  if (status.binding === 'known') return 'cloudBoundKnown'
  return status.binding === 'new' ? 'cloudBoundNew' : undefined
}

export function CloudLoginDialog({ close }: { close: () => void }) {
  const { t } = useI18n()
  const [done, setDone] = useState<MessageKey | null>(null)
  return (
    <Dialog
      title={t('cloudLoginTitle')}
      close={close}
      footer={
        done ? (
          <button type="button" className="lm-btn primary" onClick={close}>
            {t('cloudBoundDone')}
          </button>
        ) : undefined
      }
    >
      {done ? (
        <p className="lm-note" data-tone="info" role="status" style={{ margin: 0 }}>
          {t(done)}
        </p>
      ) : (
        <CloudAuthForm
          onSuccess={(status) => {
            const message = bindingMessage(status)
            if (message) setDone(message)
            else close()
          }}
        />
      )}
    </Dialog>
  )
}

/* Compact sidebar bottom dock pill: one click to sign in, or at-a-glance email & device count when signed in. */
export function CloudAccountPill() {
  const { cloudStatus, cloudDevices, guest } = useApp()
  const { t } = useI18n()
  const [open, setOpen] = useState(false)

  if (guest || !cloudStatus) return null

  // The dialog stays up after signing in to say whether this computer was recognised.
  if (cloudStatus.loggedIn && !open) {
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
