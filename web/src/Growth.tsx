/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useMemo, useState } from 'react'
import { APIError } from './api'
import { BrandMark } from './brand'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import type { MessageKey } from './i18n'
import { ago } from './kit'
import { useLicense } from './license'
import { useApp } from './state'
import { AcquisitionDetails, type Acquisition } from './AcquisitionDetails'
import type { GitHubStats } from './types'
import './growth.css'

const sessionKey = 'lgmon-license-admin'

type UsageSnapshot = {
  boardsCount: number
  monitorsCount: number
  hasFirstBoard: boolean
  hasFirstMonitor: boolean
  features?: string[]
}

type LicenseDevice = {
  id: string
  name: string
  version?: string
  os?: string
  firstSeen: number
  lastSeen: number
  usage?: UsageSnapshot
}

type LicenseKey = {
  id: string
  label: string
  createdAt: number
  expiresAt: number
  maxDevices: number
  revokedAt: number
  state: 'active' | 'revoked' | 'expired'
  devices: LicenseDevice[]
}

type AdminDevice = {
  id: string
  name: string
  version?: string
  os?: string
  createdAt: number
  lastSeen?: number
  usage?: UsageSnapshot
}

type TelemetryDevice = {
  deviceId: string
  keyId?: string
  label?: string
  version?: string
  os?: string
  firstSeen?: number
  lastSeen?: number
  usage?: UsageSnapshot
}

type AnalyticsReply = {
  telemetryReady?: boolean
  devices?: TelemetryDevice[]
  localUsage?: UsageSnapshot
}

type WindowKey = '24h' | '7d' | '30d' | 'all'

const windowMs: Record<WindowKey, number> = {
  '24h': 24 * 3600 * 1000,
  '7d': 7 * 24 * 3600 * 1000,
  '30d': 30 * 24 * 3600 * 1000,
  all: 0,
}

const featureDefs: ReadonlyArray<{ id: string; labelKey: MessageKey }> = [
  { id: 'agent_mcp', labelKey: 'growthFeatAgent' },
  { id: 'docs', labelKey: 'growthFeatDocs' },
  { id: 'deps', labelKey: 'growthFeatDeps' },
  { id: 'alerts', labelKey: 'growthFeatAlerts' },
  { id: 'feishu', labelKey: 'growthFeatFeishu' },
  { id: 'bohemia', labelKey: 'growthFeatBohemia' },
  { id: 'lan_share', labelKey: 'growthFeatLanShare' },
  { id: 'lan_web', labelKey: 'growthFeatLanWeb' },
  { id: 'departments', labelKey: 'growthFeatDepts' },
  { id: 'batch_jobs', labelKey: 'growthFeatJobs' },
]

function pct(part: number, whole: number): string {
  if (whole <= 0) return '—'
  const val = (part / whole) * 100
  return val >= 100 ? '100%' : val >= 10 ? val.toFixed(0) + '%' : val.toFixed(1) + '%'
}

function fmtNum(n: number): string {
  return n.toLocaleString()
}

type ChannelKey = 'all' | 'windows' | 'linux'

function normalizeOS(raw?: string): 'windows' | 'linux' {
  const v = (raw || '').trim().toLowerCase()
  return v === 'linux' ? 'linux' : 'windows'
}

type EnrichedDevice = {
  id: string
  keyId: string
  keyLabel: string
  name: string
  version?: string
  os: 'windows' | 'linux'
  firstSeen: number
  lastSeen: number
  isAdmin: boolean
  usage?: UsageSnapshot
}

export function GrowthPage() {
  const { api, guest, now, cloudStatus } = useApp()
  const { license } = useLicense()
  const { lang, t } = useI18n()

  const [session, setSession] = useState(() => {
    try {
      return sessionStorage.getItem(sessionKey) || ''
    } catch {
      return ''
    }
  })
  const [password, setPassword] = useState('')
  const [loggingIn, setLoggingIn] = useState(false)
  const [autoLoggingIn, setAutoLoggingIn] = useState(false)
  const [autoTried, setAutoTried] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const [ghStats, setGhStats] = useState<GitHubStats | null>(null)
  const [acquisition, setAcquisition] = useState<Acquisition | null>(null)
  const [keys, setKeys] = useState<LicenseKey[]>([])
  const [admins, setAdmins] = useState<AdminDevice[]>([])
  const [analytics, setAnalytics] = useState<AnalyticsReply | null>(null)

  const [selectedKey, setSelectedKey] = useState<string>('all')
  const [selectedChannel, setSelectedChannel] = useState<ChannelKey>('all')
  const [includeAdmins, setIncludeAdmins] = useState<boolean>(false)
  const [windowKey, setWindowKey] = useState<WindowKey>('7d')

  const isWhitelistedAccount =
    !!license?.whitelistAdmin ||
    (!!cloudStatus?.loggedIn && (cloudStatus.email || '').trim().toLowerCase() === 'fli.gda@foxmail.com')

  const clearSession = useCallback(() => {
    setSession('')
    try {
      sessionStorage.removeItem(sessionKey)
    } catch {
      // Ignore private storage errors.
    }
  }, [])

  const loadGitHub = useCallback(() => {
    void fetch('/api/v1/download/stats', { cache: 'no-store' })
      .then((res) => (res.ok ? (res.json() as Promise<GitHubStats>) : null))
      .then((data) => {
        if (data && typeof data.downloads === 'number') setGhStats(data)
      })
      .catch(() => undefined)
  }, [])

  const loadAdminData = useCallback(
    async (activeSession: string) => {
      if (!activeSession) return
      setLoading(true)
      setError('')
      const post = <T,>(action: string) =>
        api.request<T>('/license/admin', {
          method: 'POST',
          body: JSON.stringify({ session: activeSession, action }),
        })
      try {
        const [keysRes, adminsRes, analyticsRes, acquisitionRes] = await Promise.all([
          post<{ keys: LicenseKey[] }>('keys'),
          post<{ devices: AdminDevice[] }>('admin_devices').catch(() => ({ devices: [] as AdminDevice[] })),
          post<AnalyticsReply>('analytics').catch(() => ({ telemetryReady: false, devices: [] })),
          post<Acquisition>('growth').catch(() => null),
        ])
        setAcquisition(acquisitionRes?.days === 30 && Array.isArray(acquisitionRes.downloads) && Array.isArray(acquisitionRes.gates) ? acquisitionRes : null)
        setKeys(Array.isArray(keysRes?.keys) ? keysRes.keys : [])
        setAdmins(Array.isArray(adminsRes?.devices) ? adminsRes.devices : [])
        setAnalytics(analyticsRes || null)
      } catch (err: unknown) {
        if (err instanceof APIError && err.status === 401) {
          clearSession()
        }
        setError(err instanceof Error ? err.message : '读取数据失败')
      } finally {
        setLoading(false)
      }
    },
    [api, clearSession],
  )

  useEffect(() => {
    if (guest) return
    loadGitHub()
  }, [guest, loadGitHub])

  useEffect(() => {
    if (guest || session || autoTried || !isWhitelistedAccount) return
    setAutoTried(true)
    setAutoLoggingIn(true)
    setError('')
    api
      .request<{ session: string }>('/license/admin', {
        method: 'POST',
        body: JSON.stringify({ action: 'auto_login' }),
      })
      .then((res) => {
        if (!res?.session) return
        setSession(res.session)
        try {
          sessionStorage.setItem(sessionKey, res.session)
        } catch {
          // Session stays in memory for this tab.
        }
      })
      .catch(() => {
        // Fall back silently to the password form when auto-login is unavailable.
      })
      .finally(() => setAutoLoggingIn(false))
  }, [api, guest, session, autoTried, isWhitelistedAccount])

  useEffect(() => {
    if (guest || !session) return
    void loadAdminData(session)
  }, [guest, session, loadAdminData])

  // Merge telemetry usage onto devices by deviceId (also attach localUsage to this admin machine).
  const allDevices = useMemo<EnrichedDevice[]>(() => {
    const usageByDevice = new Map<string, UsageSnapshot>()
    const osByDevice = new Map<string, string>()
    for (const row of analytics?.devices || []) {
      if (row.deviceId && row.usage) {
        usageByDevice.set(row.deviceId, row.usage)
      }
      if (row.deviceId && row.os) {
        osByDevice.set(row.deviceId, row.os)
      }
    }
    if (license?.device && analytics?.localUsage && !usageByDevice.has(license.device)) {
      usageByDevice.set(license.device, analytics.localUsage)
    }
    const list: EnrichedDevice[] = []
    for (const key of keys) {
      for (const dev of key.devices || []) {
        list.push({
          id: dev.id,
          keyId: key.id,
          keyLabel: key.label,
          name: dev.name,
          version: dev.version,
          os: normalizeOS(dev.os || osByDevice.get(dev.id)),
          firstSeen: dev.firstSeen,
          lastSeen: dev.lastSeen,
          isAdmin: false,
          usage: dev.usage || usageByDevice.get(dev.id),
        })
      }
    }
    for (const adm of admins) {
      list.push({
        id: adm.id,
        keyId: '__admin__',
        keyLabel: lang === 'en' ? 'Admin Devices' : '管理员设备',
        name: adm.name,
        version: adm.version,
        os: normalizeOS(adm.os || osByDevice.get(adm.id)),
        firstSeen: adm.createdAt,
        lastSeen: adm.lastSeen || adm.createdAt,
        isAdmin: true,
        usage: adm.usage || usageByDevice.get(adm.id),
      })
    }
    return list
  }, [keys, admins, analytics, license?.device, lang])

  const keyScopedDevices = useMemo(() => {
    const adminIds = new Set(admins.map((admin) => admin.id))
    const unique = new Map<string, EnrichedDevice>()
    for (const dev of allDevices) {
      if (selectedKey !== 'all' && dev.keyId !== selectedKey) continue
      if (selectedKey === 'all' && !includeAdmins && adminIds.has(dev.id)) continue
      const previous = unique.get(dev.id)
      if (!previous || previous.lastSeen < dev.lastSeen) unique.set(dev.id, dev)
    }
    return [...unique.values()]
  }, [allDevices, admins, selectedKey, includeAdmins])

  const channelCounts = useMemo(() => {
    let windows = 0
    let linux = 0
    for (const dev of keyScopedDevices) {
      if (dev.os === 'linux') linux++
      else windows++
    }
    return { all: keyScopedDevices.length, windows, linux }
  }, [keyScopedDevices])

  const filteredDevices = useMemo(() => {
    if (selectedChannel === 'all') return keyScopedDevices
    return keyScopedDevices.filter((dev) => dev.os === selectedChannel)
  }, [keyScopedDevices, selectedChannel])

  const currentNow = now()
  const cutoff = windowMs[windowKey] > 0 ? currentNow - windowMs[windowKey] : 0

  const metrics = useMemo(() => {
    const activated = filteredDevices.length
    const active = filteredDevices.filter((d) => !cutoff || d.lastSeen >= cutoff).length
    const reporting = filteredDevices.filter((d) => !!d.usage)
    const firstBoard = reporting.filter((d) => d.usage?.hasFirstBoard).length
    const firstService = reporting.filter((d) => d.usage?.hasFirstMonitor).length
    const anyFeature = reporting.filter((d) => (d.usage?.features?.length || 0) > 0).length
    const featureCounts = new Map<string, number>()
    for (const def of featureDefs) featureCounts.set(def.id, 0)
    for (const dev of reporting) {
      for (const feat of dev.usage?.features || []) {
        featureCounts.set(feat, (featureCounts.get(feat) || 0) + 1)
      }
    }
    return {
      activated,
      active,
      reportingCount: reporting.length,
      firstBoard,
      firstService,
      anyFeature,
      featureCounts,
    }
  }, [filteredDevices, cutoff])

  const keyCohorts = useMemo(() => {
    return keys.map((key) => {
      const devs = allDevices.filter((d) => d.keyId === key.id)
      const active = devs.filter((d) => !cutoff || d.lastSeen >= cutoff).length
      const reported = devs.filter((d) => !!d.usage)
      const firstBoard = reported.filter((d) => d.usage?.hasFirstBoard).length
      const avgMonitors =
        reported.length > 0
          ? reported.reduce((sum, d) => sum + (d.usage?.monitorsCount || 0), 0) / reported.length
          : null
      const lastSeen = devs.reduce((max, d) => Math.max(max, d.lastSeen || 0), 0)
      return { key, devs, active, reported: reported.length, firstBoard, avgMonitors, lastSeen }
    })
  }, [keys, allDevices, cutoff])

  if (guest) {
    return (
      <div className="lm-page">
        <p className="lm-note" data-tone="down">
          {lang === 'en' ? 'Admin growth dashboard is not available to guests.' : '访客无法查看管理员看板。'}
        </p>
      </div>
    )
  }

  const stateLabels: Record<LicenseKey['state'], string> =
    lang === 'en'
      ? { active: 'Active', revoked: 'Revoked', expired: 'Expired' }
      : { active: '有效', revoked: '已收回', expired: '已过期' }

  const isSingleKey = selectedKey !== 'all'
  const funnelTop = Math.max(metrics.activated, 1)
  const funnelSteps = [
    {
      id: 'activated',
      label: t('growthStepActivated'),
      count: metrics.activated,
      tone: 'primary',
    },
    {
      id: 'first_board',
      label: t('growthStepFirstBoard'),
      count: metrics.firstBoard,
      tone: 'up',
    },
    {
      id: 'first_service',
      label: t('growthStepFirstService'),
      count: metrics.firstService,
      tone: 'up',
    },
    {
      id: 'features',
      label: t('growthStepFeature'),
      count: metrics.anyFeature,
      tone: 'violet',
    },
  ]

  const releases = ghStats?.releases || []
  const assets = ghStats?.assets
  const windowsDownloads = assets ? assets.installer + assets.legacyInstaller : null
  const linuxDownloads = assets ? (assets.linux || 0) : null
  const totalInstallerDownloads =
    windowsDownloads !== null && linuxDownloads !== null ? windowsDownloads + linuxDownloads : null
  const pending = acquisition?.gates.reduce((sum, row) => sum + row.pending, 0)

  return (
    <div className="lm-page lm-growth">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">{t('growthTitle')}</h1>
          <p className="lm-lead">{t('growthLead')}</p>
        </div>
        <div className="lm-actions">
          {session && (
            <>
              <button
                type="button"
                className="lm-btn"
                disabled={loading}
                onClick={() => {
                  loadGitHub()
                  void loadAdminData(session)
                }}
              >
                <Glyph name="refresh" size={15} />
                {t('growthRefresh')}
              </button>
              <button type="button" className="lm-btn ghost" onClick={clearSession}>
                {t('growthLogoutBtn')}
              </button>
            </>
          )}
        </div>
      </header>

      {!session ? (
        <section className="lm-growth-login">
          <div className="lm-growth-login-head">
            <BrandMark size={34} />
            <div>
              <h2>{t('growthLoginTitle')}</h2>
              <p className="lm-muted lm-small">
                {autoLoggingIn
                  ? lang === 'en'
                    ? 'Verifying whitelisted admin account…'
                    : '已识别白名单管理员账号，正在免密登录…'
                  : t('growthLoginHint')}
              </p>
            </div>
          </div>
          {!autoLoggingIn && (
          <form
            className="lm-growth-login-form"
            onSubmit={(event) => {
              event.preventDefault()
              setLoggingIn(true)
              setError('')
              api
                .request<{ session: string }>('/license/admin', {
                  method: 'POST',
                  body: JSON.stringify({ action: 'login', password }),
                })
                .then((res) => {
                  setSession(res.session)
                  setPassword('')
                  try {
                    sessionStorage.setItem(sessionKey, res.session)
                  } catch {
                    // Session stays in memory for this tab.
                  }
                })
                .catch((err: unknown) => setError(err instanceof Error ? err.message : '登录失败'))
                .finally(() => setLoggingIn(false))
            }}
          >
            <input
              className="lm-input"
              type="password"
              value={password}
              placeholder={t('growthPasswordPlaceholder')}
              aria-label={t('growthPasswordPlaceholder')}
              autoFocus
              onChange={(event) => setPassword(event.target.value)}
            />
            <button className="lm-btn primary" disabled={loggingIn || !password}>
              {loggingIn ? t('growthLoggingIn') : t('growthLoginBtn')}
            </button>
          </form>
          )}
          {error && (
            <p className="lm-error" role="alert">
              {error}
            </p>
          )}
        </section>
      ) : (
        <>
          {error && (
            <p className="lm-note" data-tone="down">
              {error}
            </p>
          )}

          {analytics && analytics.telemetryReady === false && (
            <div className="lm-note" data-tone="accent">
              {t('growthTelemetryPending')}
            </div>
          )}

          {/* Filter Toolbar: Platform Channel + Activation Key + Activity Window */}
          <section className="lm-growth-controls" aria-label={t('growthKeyFilter')}>
            <div className="lm-growth-keys-filter">
              <span className="lm-growth-filter-label">
                {lang === 'en' ? 'Distribution Channel (OS)' : '分发渠道（系统平台）'}
              </span>
              <div className="lm-growth-pills" role="group" aria-label={lang === 'en' ? 'Platform channel' : '系统渠道筛选'}>
                {([
                  ['all', lang === 'en' ? 'All Channels' : '全部渠道', channelCounts.all],
                  ['windows', 'Windows', channelCounts.windows],
                  ['linux', 'Linux', channelCounts.linux],
                ] as const).map(([id, label, count]) => (
                  <button
                    key={id}
                    type="button"
                    className="lm-growth-pill"
                    aria-pressed={selectedChannel === id}
                    onClick={() => setSelectedChannel(id)}
                  >
                    <span>{label}</span>
                    <small>{count}</small>
                  </button>
                ))}
              </div>
              <span className="lm-growth-filter-label" style={{ marginTop: 4 }}>{t('growthKeyFilter')}</span>
              <div className="lm-growth-pills">
                <button
                  type="button"
                  className="lm-growth-pill"
                  aria-pressed={selectedKey === 'all'}
                  onClick={() => setSelectedKey('all')}
                >
                  <span>{t('growthAllKeys')}</span>
                  <small>{new Set(allDevices.filter(d => includeAdmins || !admins.some(a => a.id === d.id)).map(d => d.id)).size}</small>
                </button>
                {keys.map((key) => (
                  <button
                    key={key.id}
                    type="button"
                    className="lm-growth-pill"
                    data-state={key.state}
                    aria-pressed={selectedKey === key.id}
                    onClick={() => setSelectedKey(key.id)}
                  >
                    <i className="lm-growth-dot" aria-hidden="true" />
                    <span>{key.label}</span>
                    <small>{key.devices?.length || 0}</small>
                  </button>
                ))}
                {admins.length > 0 && (
                  <button
                    type="button"
                    className="lm-growth-pill"
                    aria-pressed={selectedKey === '__admin__'}
                    onClick={() => setSelectedKey('__admin__')}
                  >
                    <span>{lang === 'en' ? 'Admin Devices' : '管理员设备'}</span>
                    <small>{admins.length}</small>
                  </button>
                )}
              </div>
            </div>
            <div className="lm-growth-right-controls">
              {selectedKey === 'all' && admins.length > 0 && (
                <label className="lm-growth-toggle">
                  <input
                    type="checkbox"
                    checked={includeAdmins}
                    onChange={(event) => setIncludeAdmins(event.target.checked)}
                  />
                  <span>{t('growthIncludeAdmins')}</span>
                </label>
              )}
              <div className="lm-seg" role="group" aria-label={t('growthKpiActive')}>
                {(['24h', '7d', '30d', 'all'] as WindowKey[]).map((w) => (
                  <button
                    key={w}
                    type="button"
                    aria-pressed={windowKey === w}
                    onClick={() => setWindowKey(w)}
                  >
                    {w === '24h'
                      ? t('growthWindow24h')
                      : w === '7d'
                        ? t('growthWindow7d')
                        : w === '30d'
                          ? t('growthWindow30d')
                          : t('growthWindowAll')}
                  </button>
                ))}
              </div>
            </div>
          </section>

          {/* 4 Executive KPI Cards */}
          <section className="lm-growth-kpis">
            <article className="lm-growth-kpi">
              <span className="lm-growth-kpi-label">{lang === 'en' ? 'Installer file downloads' : '安装包下载次数'}</span>
              <strong className="lm-growth-kpi-val">
                {selectedChannel === 'windows'
                  ? windowsDownloads === null
                    ? '—'
                    : fmtNum(windowsDownloads)
                  : selectedChannel === 'linux'
                    ? linuxDownloads === null
                      ? '—'
                      : fmtNum(linuxDownloads)
                    : totalInstallerDownloads === null
                      ? '—'
                      : fmtNum(totalInstallerDownloads)}
              </strong>
              <small className="lm-growth-kpi-sub">
                {assets
                  ? `Windows ${fmtNum(assets.installer + assets.legacyInstaller)} · Linux ${fmtNum(assets.linux || 0)}`
                  : lang === 'en'
                    ? 'GitHub · all releases'
                    : 'GitHub · 所有版本累计'}
                {ghStats?.version ? ` · ${ghStats.version}` : ''}
              </small>
            </article>
            <article className="lm-growth-kpi">
              <span className="lm-growth-kpi-label">{lang === 'en' ? 'Waiting at the beta key screen' : '已到密钥页 · 待激活'}</span>
              <strong className="lm-growth-kpi-val">{pending === undefined ? '—' : fmtNum(pending)}</strong>
              <small className="lm-growth-kpi-sub">{lang === 'en' ? 'First seen in the last 30 days · all keys' : '近 30 天首次到达 · 全部密钥'}</small>
            </article>
            <article className="lm-growth-kpi">
              <span className="lm-growth-kpi-label">{t('growthKpiActivated')}</span>
              <strong className="lm-growth-kpi-val">{fmtNum(metrics.activated)}</strong>
              <small className="lm-growth-kpi-sub">
                {isSingleKey
                  ? t('growthSingleKeyNote')
                  : (lang === 'en' ? 'Devices in the selected cohort' : '当前所选范围的设备')}
              </small>
            </article>
            <article className="lm-growth-kpi">
              <span className="lm-growth-kpi-label">{t('growthKpiActive')}</span>
              <strong className="lm-growth-kpi-val">{fmtNum(metrics.active)}</strong>
              <small className="lm-growth-kpi-sub">
                {pct(metrics.active, metrics.activated)} ({windowKey})
              </small>
            </article>
            <article className="lm-growth-kpi">
              <span className="lm-growth-kpi-label">{t('growthKpiAdoption')}</span>
              <strong className="lm-growth-kpi-val">{fmtNum(metrics.firstBoard)}</strong>
              <small className="lm-growth-kpi-sub">
                {pct(metrics.firstBoard, metrics.activated)} · {metrics.anyFeature}{' '}
                {lang === 'en' ? 'with features' : '台用进阶功能'}
              </small>
            </article>
          </section>

          {/* Visual Conversion Funnel + Feature Adoption Grid */}
          <div className="lm-growth-grid">
            <section className="lm-section lm-growth-card" style={{ marginTop: 0 }}>
              <header className="lm-growth-card-head">
                <div>
                  <h2 className="lm-h2">{t('growthFunnelTitle')}</h2>
                  <p className="lm-muted lm-small">{t('growthFunnelLead')}</p>
                </div>
              </header>
              <AcquisitionDetails stats={ghStats} data={acquisition} lang={lang} />
              <ol className="lm-funnel">
                {funnelSteps.map((step, idx) => {
                  const widthPct = Math.max(
                    step.count > 0 ? 6 : 0,
                    Math.min(100, (step.count / funnelTop) * 100),
                  )
                  return (
                    <li key={step.id} className="lm-funnel-step">
                      <div className="lm-funnel-meta">
                        <span className="lm-funnel-name">{step.label}</span>
                        <span className="lm-funnel-nums">
                          <b>{fmtNum(step.count)}</b>
                          {idx > 0 && (
                            <small>
                              {lang === 'en' ? 'of activated devices' : '占已激活设备'} {pct(step.count, metrics.activated)}
                            </small>
                          )}

                        </span>
                      </div>
                      <div className="lm-funnel-track">
                        <div
                          className={'lm-funnel-bar tone-' + step.tone}
                          style={{ width: widthPct + '%' }}
                        />
                      </div>
                    </li>
                  )
                })}
              </ol>
            </section>

            <section className="lm-section lm-growth-card" style={{ marginTop: 0 }}>
              <header className="lm-growth-card-head">
                <div>
                  <h2 className="lm-h2">{t('growthFeaturesTitle')}</h2>
                  <p className="lm-muted lm-small">{t('growthFeaturesLead')}</p>
                </div>
              </header>
              <ul className="lm-feat-bars">
                {featureDefs.map((def) => {
                  const count = metrics.featureCounts.get(def.id) || 0
                  const base = Math.max(metrics.activated, 1)
                  const widthPct = Math.min(100, (count / base) * 100)
                  return (
                    <li key={def.id} className="lm-feat-row">
                      <div className="lm-feat-head">
                        <span>{t(def.labelKey)}</span>
                        <span className="lm-feat-val">
                          <b>{count}</b>
                          <small>{pct(count, metrics.activated)}</small>
                        </span>
                      </div>
                      <div className="lm-feat-track">
                        <div className="lm-feat-fill" style={{ width: widthPct + '%' }} />
                      </div>
                    </li>
                  )
                })}
              </ul>
            </section>
          </div>

          {/* Activation Key Cohort Comparison Table */}
          <section className="lm-section lm-growth-card">
            <header className="lm-growth-card-head">
              <div>
                <h2 className="lm-h2">{t('growthKeysTableTitle')}</h2>
                <p className="lm-muted lm-small">{t('growthKeysTableHint')}</p>
              </div>
            </header>
            {keyCohorts.length === 0 ? (
              <p className="lm-muted lm-small">{t('growthNoKeys')}</p>
            ) : (
              <div className="lm-growth-table-wrap">
                <table className="lm-growth-table">
                  <thead>
                    <tr>
                      <th>{t('growthColKey')}</th>
                      <th>{t('growthColState')}</th>
                      <th>{t('growthColDevices')}</th>
                      <th>{t('growthColActive')}</th>
                      <th>{t('growthColFirstBoard')}</th>
                      <th>{t('growthColAvgMonitors')}</th>
                      <th>{t('growthColLastSeen')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {keyCohorts.map(({ key, devs, active, firstBoard, avgMonitors, lastSeen }) => (
                      <tr
                        key={key.id}
                        className={selectedKey === key.id ? 'is-selected' : undefined}
                        onClick={() => setSelectedKey(selectedKey === key.id ? 'all' : key.id)}
                      >
                        <td>
                          <strong>{key.label}</strong>
                        </td>
                        <td>
                          <span className="lm-growth-state" data-state={key.state}>
                            {stateLabels[key.state]}
                          </span>
                        </td>
                        <td className="num">
                          {devs.length}
                          {key.maxDevices ? ` / ${key.maxDevices}` : ''}
                        </td>
                        <td className="num">
                          {active} <small>({pct(active, devs.length)})</small>
                        </td>
                        <td className="num">
                          {firstBoard} <small>({pct(firstBoard, devs.length)})</small>
                        </td>
                        <td className="num">{avgMonitors !== null ? avgMonitors.toFixed(1) : '—'}</td>
                        <td className="muted">{lastSeen ? ago(lastSeen, currentNow) : '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {/* Releases Download Breakdown */}
          <section className="lm-section lm-growth-card">
            <header className="lm-growth-card-head">
              <div>
                <h2 className="lm-h2">{t('growthReleasesTitle')}</h2>
              </div>
            </header>
            {releases.length === 0 ? (
              <p className="lm-muted lm-small">{t('growthNoReleases')}</p>
            ) : (
              <div className="lm-growth-table-wrap">
                <table className="lm-growth-table">
                  <thead>
                    <tr>
                      {(lang === 'en'
                        ? ['Version', 'Windows', 'Linux', 'Linux script', 'Legacy Win', 'Checksums', 'Metadata', 'Other', 'Total']
                        : ['版本', 'Windows 安装包', 'Linux 安装包', 'Linux 脚本', '旧版安装包', '校验文件', '安装信息', '其他', '合计']
                      ).map((label) => (
                        <th key={label}>{label}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {releases.map((rel) => (
                      <tr key={rel.tag}>
                        <td>
                          <code>{rel.tag}</code>
                        </td>
                        {[
                          rel.assets?.installer,
                          rel.assets?.linux,
                          rel.assets?.linuxScript,
                          rel.assets?.legacyInstaller,
                          rel.assets?.checksums,
                          rel.assets?.metadata,
                          rel.assets?.other,
                          rel.downloads,
                        ].map((n, i) => (
                          <td key={i} className="num">
                            {n === undefined ? '—' : fmtNum(n)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </div>
  )
}
