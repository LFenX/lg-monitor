/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { createContext, useContext, useEffect, useRef, useState } from 'react'
import type { MouseEvent, ReactNode } from 'react'
import { API, APIError } from './api'
import { BrandMark, Wordmark } from './brand'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { useCopy } from './kit'
import './license.css'

/*
 * The beta gate. A release built to require a beta key shows this page until one is entered; the key is checked
 * with the owner's license service and kept on this computer, and when it is revoked or runs out, the page comes
 * back. Ordinary builds answer "off" and never show it.
 *
 * The owner's console hides behind a triple click on the lgmon mark, on any computer: sign in with the admin
 * password, create keys with a validity and a computer limit, take keys or single computers back, and mark
 * computers as the owner's own (admin devices, as many as they like), which need no key.
 */

export type License = {
  enforced: boolean
  state: 'off' | 'active' | 'missing' | 'expired' | 'revoked' | 'stale'
  label?: string
  expiresAt?: number
  admin?: boolean
  /* This computer's ID with the license service, so the console can point it out among the admin devices. */
  device?: string
  problem?: string
}

const LicenseContext = createContext<{ license?: License }>({})

export function useLicense() {
  return useContext(LicenseContext)
}

const ADMIN_EVENT = 'lgmon-license-admin'
const sessionKey = 'lgmon-license-admin'

/* Three clicks within a moment open the owner's console; one or two do what the mark always did. */
export function useTripleClick() {
  const clicks = useRef<number[]>([])
  return (event: MouseEvent) => {
    const now = event.timeStamp
    clicks.current = [...clicks.current.filter((at) => now - at < 650), now]
    if (clicks.current.length >= 3) {
      clicks.current = []
      event.preventDefault()
      window.dispatchEvent(new Event(ADMIN_EVENT))
    }
  }
}

function blocked(license?: License) {
  return !!license && license.enforced && license.state !== 'active' && license.state !== 'off'
}

const reasons: Record<string, string> = {
  missing: 'lgmon 正在内测。输入管理员发给你的内测密钥就能开始使用，激活后这台电脑会记住它。',
  expired: '你的内测密钥已经到期。向管理员要一个新的密钥，输入后继续使用。',
  revoked: '这台电脑的内测资格已被收回。需要继续使用的话，向管理员要一个新的密钥。',
  stale: '已经有一段时间没能和授权服务核对了。连上网络后会自动恢复，也可以重新输入密钥。',
}

/* Shapes what is typed or pasted into LGMON-XXXXX-XXXXX-XXXXX-XXXXX, without fighting someone typing the prefix. */
export function format(value: string) {
  const plain = value.toUpperCase().replace(/[^0-9A-Z]/g, '')
  if ('LGMON'.startsWith(plain)) return plain
  const body = (plain.startsWith('LGMON') ? plain.slice(5) : plain).slice(0, 20)
  const groups = body.match(/.{1,5}/g) || []
  return 'LGMON' + (groups.length ? '-' + groups.join('-') : '')
}

function Gate({ api, license, done }: { api: API; license: License; done: () => void }) {
  const [key, setKey] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const triple = useTripleClick()
  return (
    <div className="lm-gate">
      <i className="lm-gate-glow" aria-hidden="true" />
      <main className="lm-gate-card" aria-label="输入内测密钥">
        <button className="lm-gate-mark" onClick={triple} aria-label="lgmon">
          <BrandMark size={44} />
        </button>
        <Wordmark />
        <h1>输入内测密钥</h1>
        <p>{reasons[license.state] || reasons.missing}</p>
        <form
          onSubmit={(event) => {
            event.preventDefault()
            setBusy(true)
            setError('')
            api
              .request<License>('/license', { method: 'POST', body: JSON.stringify({ key }) })
              .then(done)
              .catch((err: unknown) => setError(err instanceof Error ? err.message : '激活失败，请重试'))
              .finally(() => setBusy(false))
          }}
        >
          <input
            className="lm-gate-key"
            value={key}
            onChange={(event) => setKey(format(event.target.value))}
            placeholder="LGMON-XXXXX-XXXXX-XXXXX-XXXXX"
            aria-label="内测密钥"
            autoComplete="off"
            spellCheck={false}
            autoFocus
          />
          <button className="lm-gate-go" disabled={busy || key.length < 29}>
            {busy ? '正在激活…' : '激活这台电脑'}
          </button>
        </form>
        {error && (
          <p className="lm-gate-error" role="alert">
            {error}
          </p>
        )}
        {license.problem && !error && <p className="lm-gate-note">{license.problem}</p>}
        <p className="lm-gate-foot">
          密钥由管理员生成，有效期和能用的电脑数各不相同；到期或被收回时这里会再出现。
        </p>
      </main>
    </div>
  )
}

type Device = { id: string; name: string; version?: string; firstSeen: number; lastSeen: number }
type AdminDevice = { id: string; name: string; version?: string; createdAt: number; lastSeen?: number }
type Key = {
  id: string
  label: string
  createdAt: number
  expiresAt: number
  maxDevices: number
  revokedAt: number
  state: 'active' | 'revoked' | 'expired'
  devices: Device[]
}

const when = (ms: number) =>
  ms
    ? new Date(ms).toLocaleString('zh-CN', {
        month: 'numeric',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      })
    : '不过期'

const stateText: Record<Key['state'], string> = { active: '有效', revoked: '已收回', expired: '已过期' }

/* Whether this computer is one of the owner's: said plainly when it is, offered as one clear step when not. */
function ThisComputer({ license, busy, adopt }: { license?: License; busy: boolean; adopt: () => void }) {
  if (license?.admin)
    return (
      <div className="lm-admin-me" data-admin="">
        <span className="lm-admin-me-mark" aria-hidden="true">
          <Glyph name="check" size={16} />
        </span>
        <span className="lm-admin-me-t">
          <b>这台电脑是管理员设备</b>
          <small>不用内测密钥。管理员设备可以有好几台，都列在下面。</small>
        </span>
      </div>
    )
  return (
    <div className="lm-admin-me">
      <span className="lm-admin-me-mark" aria-hidden="true">
        <Glyph name="computer" size={16} />
      </span>
      <span className="lm-admin-me-t">
        <b>这台电脑还不是管理员设备</b>
        <small>你自己的电脑设为管理员设备后不用内测密钥，可以设好几台。</small>
      </span>
      <button className="lm-btn small primary" disabled={busy} onClick={adopt}>
        设为管理员设备
      </button>
    </div>
  )
}

function Console({
  api,
  license,
  close,
  changed,
}: {
  api: API
  license?: License
  close: () => void
  changed: () => void
}) {
  const [session, setSession] = useState(() => {
    try {
      return sessionStorage.getItem(sessionKey) || ''
    } catch {
      return ''
    }
  })
  const [password, setPassword] = useState(''),
    [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [keys, setKeys] = useState<Key[]>(),
    [fresh, setFresh] = useState<{ key: string; label: string }>(),
    [open, setOpen] = useState(''),
    [admins, setAdmins] = useState<AdminDevice[]>(),
    [removing, setRemoving] = useState('')
  const [label, setLabel] = useState(''),
    [days, setDays] = useState(7),
    [devices, setDevices] = useState(1)
  const { t } = useI18n()
  const { copied, copy } = useCopy()
  const admin = <T,>(body: Record<string, unknown>, what: string) => {
    setBusy(what)
    setError('')
    return api
      .request<T>('/license/admin', { method: 'POST', body: JSON.stringify({ session, ...body }) })
      .catch((err: unknown) => {
        if (err instanceof APIError && err.status === 401) {
          setSession('')
          try {
            sessionStorage.removeItem(sessionKey)
          } catch {
            // Nothing kept.
          }
        }
        setError(err instanceof Error ? err.message : '操作失败')
        throw err
      })
      .finally(() => setBusy(''))
  }
  const load = () =>
    admin<{ keys: Key[] }>({ action: 'keys' }, 'keys')
      .then((value) => setKeys(value.keys))
      .catch(() => undefined)
  // Read on its own, quietly: an older license service without the list must not block the keys.
  const loadAdmins = () =>
    api
      .request<{ devices: AdminDevice[] }>('/license/admin', {
        method: 'POST',
        body: JSON.stringify({ session, action: 'admin_devices' }),
      })
      .then((value) => setAdmins(Array.isArray(value?.devices) ? value.devices : []))
      .catch(() => setAdmins(undefined))
  useEffect(() => {
    if (!session) return
    void load()
    void loadAdmins()
    // load reads the session it was given; it runs again after a login.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session])
  useEffect(() => {
    const key = (event: KeyboardEvent) => event.key === 'Escape' && close()
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [close])
  return (
    <div className="lm-admin" role="dialog" aria-modal="true" aria-label="内测管理">
      <div className="lm-admin-panel" data-step={session ? 'console' : 'login'}>
        <header>
          <span className="lm-admin-title">
            <BrandMark size={26} />
            <b>内测管理</b>
            {session && <small>已登录</small>}
          </span>
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            {session && (
              <button
                type="button"
                className="lm-btn small primary"
                onClick={() => {
                  close()
                  window.location.hash = '#/admin/growth'
                }}
              >
                <Glyph name="graph" size={14} />
                {t('growthOpenBtn')}
              </button>
            )}
            <button className="lm-admin-close" onClick={close} aria-label="关闭">
              <Glyph name="close" size={16} />
            </button>
          </div>
        </header>
        {!session ? (
          <form
            className="lm-admin-login"
            onSubmit={(event) => {
              event.preventDefault()
              admin<{ session: string }>({ action: 'login', password }, 'login')
                .then((value) => {
                  setSession(value.session)
                  setPassword('')
                  try {
                    sessionStorage.setItem(sessionKey, value.session)
                  } catch {
                    // The session lasts while this window is open.
                  }
                })
                .catch(() => undefined)
            }}
          >
            <p>只有管理员可以进入。登录后可以生成内测密钥、收回访问，这一次登录在这个窗口里有效 12 小时。</p>
            <input
              className="lm-input"
              type="password"
              aria-label="管理员密码"
              placeholder="管理员密码"
              autoFocus
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            <button className="lm-btn primary" disabled={!!busy || !password}>
              {busy === 'login' ? '正在登录…' : '登录'}
            </button>
          </form>
        ) : (
          <div className="lm-admin-body">
            <ThisComputer
              license={license}
              busy={!!busy}
              adopt={() =>
                admin({ action: 'adopt' }, 'adopt')
                  .then(() => {
                    changed()
                    void loadAdmins()
                  })
                  .catch(() => undefined)
              }
            />
            <section className="lm-admin-new" aria-label="新建内测密钥">
              <h2>新建内测密钥</h2>
              <form
                onSubmit={(event) => {
                  event.preventDefault()
                  admin<{ key: string }>({ action: 'create', label, days, maxDevices: devices }, 'create')
                    .then((value) => {
                      setFresh({ key: value.key, label })
                      setLabel('')
                      void load()
                    })
                    .catch(() => undefined)
                }}
              >
                <label>
                  发给谁
                  <input
                    className="lm-input"
                    value={label}
                    maxLength={60}
                    placeholder="例如：小王的笔记本"
                    onChange={(event) => setLabel(event.target.value)}
                  />
                </label>
                <label>
                  有效期
                  <select
                    className="lm-input"
                    value={days}
                    onChange={(event) => setDays(Number(event.target.value))}
                  >
                    {[1, 3, 7, 14, 30, 90, 365].map((value) => (
                      <option key={value} value={value}>
                        {value} 天
                      </option>
                    ))}
                    <option value={0}>不过期</option>
                  </select>
                </label>
                <label>
                  能用几台电脑
                  <select
                    className="lm-input"
                    value={devices}
                    onChange={(event) => setDevices(Number(event.target.value))}
                  >
                    {[1, 2, 3, 5, 10].map((value) => (
                      <option key={value} value={value}>
                        {value} 台
                      </option>
                    ))}
                    <option value={0}>不限</option>
                  </select>
                </label>
                <button className="lm-btn primary" disabled={!!busy || !label.trim()}>
                  {busy === 'create' ? '正在生成…' : '生成密钥'}
                </button>
              </form>
              {fresh && (
                <div className="lm-admin-fresh" role="status">
                  <span>
                    给「{fresh.label}」的密钥，<b>只显示这一次</b>：
                  </span>
                  <code>{fresh.key}</code>
                  <button className="lm-btn small" onClick={() => copy(fresh.key)}>
                    <Glyph name={copied ? 'check' : 'copy'} size={14} />
                    {copied ? '已复制' : '复制'}
                  </button>
                </div>
              )}
            </section>
            <section aria-label="内测密钥">
              <header className="lm-admin-list-head">
                <h2>全部密钥{keys ? ' · ' + keys.length : ''}</h2>
              </header>
              {!keys ? (
                <p className="lm-muted lm-small">正在读取…</p>
              ) : keys.length === 0 ? (
                <p className="lm-muted lm-small">还没有密钥。</p>
              ) : (
                <ul className="lm-admin-keys">
                  {keys.map((item) => (
                    <li key={item.id} data-state={item.state}>
                      <div className="lm-admin-key">
                        <span className="lm-admin-chip">{stateText[item.state]}</span>
                        <span className="lm-admin-key-t">
                          <b>{item.label}</b>
                          <small>
                            {item.expiresAt ? '到期 ' + when(item.expiresAt) : '不过期'} · 电脑{' '}
                            {item.devices.length}
                            {item.maxDevices ? ' / ' + item.maxDevices : ''} · 创建于 {when(item.createdAt)}
                          </small>
                        </span>
                        <button
                          className="lm-btn small ghost"
                          onClick={() => setOpen(open === item.id ? '' : item.id)}
                        >
                          {open === item.id ? '收起' : '电脑'}
                        </button>
                        <button
                          className="lm-btn small"
                          disabled={!!busy}
                          onClick={() =>
                            admin({ action: 'update', id: item.id, extendDays: 7 }, item.id)
                              .then(load)
                              .catch(() => undefined)
                          }
                        >
                          延长 7 天
                        </button>
                        <button
                          className={'lm-btn small' + (item.state === 'revoked' ? '' : ' danger')}
                          disabled={!!busy}
                          onClick={() =>
                            admin(
                              item.state === 'revoked'
                                ? { action: 'update', id: item.id, restore: true }
                                : { action: 'update', id: item.id, revoke: true },
                              item.id,
                            )
                              .then(load)
                              .catch(() => undefined)
                          }
                        >
                          {item.state === 'revoked' ? '恢复' : '收回'}
                        </button>
                      </div>
                      {open === item.id && (
                        <ul className="lm-admin-devices">
                          {item.devices.length ? (
                            item.devices.map((device) => (
                              <li key={device.id}>
                                <span>
                                  <b>{device.name}</b>
                                  <small>
                                    {device.version ? 'lgmon ' + device.version + ' · ' : ''}最近{' '}
                                    {when(device.lastSeen)}
                                  </small>
                                </span>
                                <button
                                  className="lm-btn small danger"
                                  disabled={!!busy}
                                  onClick={() =>
                                    admin(
                                      { action: 'remove_device', id: item.id, device: device.id },
                                      device.id,
                                    )
                                      .then(load)
                                      .catch(() => undefined)
                                  }
                                >
                                  移除这台
                                </button>
                              </li>
                            ))
                          ) : (
                            <li className="lm-muted lm-small">还没有电脑用这个密钥。</li>
                          )}
                        </ul>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </section>
            <section aria-label="管理员设备">
              <header className="lm-admin-list-head">
                <h2>管理员设备{admins ? ' · ' + admins.length : ''}</h2>
              </header>
              {!admins ? (
                <p className="lm-muted lm-small">正在读取…</p>
              ) : admins.length === 0 ? (
                <p className="lm-muted lm-small">还没有管理员设备。</p>
              ) : (
                <ul className="lm-admin-admins">
                  {admins.map((device) => {
                    const here = !!license?.device && device.id === license.device
                    return (
                      <li key={device.id}>
                        <span className="lm-admin-key-t">
                          <b>
                            {device.name}
                            {here && <em className="lm-admin-here">这台</em>}
                          </b>
                          <small>
                            {device.version ? 'lgmon ' + device.version + ' · ' : ''}设为管理员{' '}
                            {when(device.createdAt)}
                            {device.lastSeen ? ' · 最近 ' + when(device.lastSeen) : ''}
                          </small>
                        </span>
                        <button
                          className="lm-btn small danger"
                          disabled={!!busy}
                          title={here ? '移除后这台电脑要输入内测密钥才能继续用' : '它下一次核对时失效'}
                          onClick={() => {
                            if (removing !== device.id) return setRemoving(device.id)
                            setRemoving('')
                            admin({ action: 'remove_admin', device: device.id }, device.id)
                              .then(() => {
                                void loadAdmins()
                                if (here) changed()
                              })
                              .catch(() => undefined)
                          }}
                        >
                          {removing === device.id ? (here ? '确认移除这台' : '确认移除') : '移除'}
                        </button>
                      </li>
                    )
                  })}
                </ul>
              )}
            </section>
          </div>
        )}
        {error && (
          <p className="lm-error" role="alert">
            {error}
          </p>
        )}
      </div>
    </div>
  )
}

/* Wraps the whole page: shows the gate when this build needs a key, and the console on a triple click. */
export function LicenseGate({ api, children }: { api: API; children: ReactNode }) {
  const [license, setLicense] = useState<License>()
  const [admin, setAdmin] = useState(false)
  const read = () =>
    api
      .request<License>('/license')
      .then(setLicense)
      .catch(() => undefined)
  useEffect(() => {
    void read()
    api.onLicense = () => void read()
    const show = () => setAdmin(true)
    window.addEventListener(ADMIN_EVENT, show)
    return () => {
      api.onLicense = undefined
      window.removeEventListener(ADMIN_EVENT, show)
    }
    // read uses the api it was made with.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api])
  return (
    <LicenseContext.Provider value={{ license }}>
      {blocked(license) ? (
        <Gate api={api} license={license!} done={() => window.location.reload()} />
      ) : (
        children
      )}
      {admin && (
        <Console
          api={api}
          license={license}
          close={() => setAdmin(false)}
          changed={() => {
            // A computer let through or shut out by the change reloads; otherwise the console just reads again.
            const before = blocked(license)
            void api
              .request<License>('/license')
              .then((next) => {
                if (blocked(next) !== before) window.location.reload()
                else setLicense(next)
              })
              .catch(() => undefined)
          }}
        />
      )}
    </LicenseContext.Provider>
  )
}
