/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useState } from 'react'
import { allows, useGate } from './access'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { CopyButton, Dialog } from './parts'
import { useSystemRestart } from './restart'
import { useApp } from './state'
import { ErrorNotice } from './ui'
import type {
  ArchiveExtractResult,
  ArchiveListResult,
  StorageCompactResult,
  StorageMigrationState,
  StorageStatus,
} from './types'
import './storage.css'

const ARCHIVABLE_TABLES = [
  'target_status',
  'probe_results',
  'eventlog_events',
  'service_status',
  'task_status',
  'inventory_changes',
  'collector_events',
  'metric_minute',
  'probe_minute',
]

function toLocalDateTimeInput(ms: number): string {
  if (!ms || ms <= 0) return ''
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function parseLocalDateTimeInput(val: string): number {
  const trimmed = val.trim()
  if (!trimmed) return 0
  const parsed = Date.parse(trimmed)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

function formatArchiveTime(ms: number): string {
  if (!ms || ms <= 0) return '—'
  return new Date(ms).toLocaleString()
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let idx = 0
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024
    idx++
  }
  return value >= 100 || idx === 0
    ? `${Math.round(value)} ${units[idx]}`
    : `${value.toFixed(1)} ${units[idx]}`
}

const ACTIVE_STATES = new Set(['preparing', 'copying', 'verifying', 'switching'])

function stepIndex(state?: StorageMigrationState['state']): number {
  switch (state) {
    case 'preparing':
      return 0
    case 'copying':
      return 1
    case 'verifying':
      return 2
    case 'switching':
      return 3
    case 'done':
      return 4
    default:
      return -1
  }
}

export function StorageSettings() {
  const { api, scope, guest } = useApp()
  const { t } = useI18n()
  const gate = useGate()
  const [status, setStatus] = useState<StorageStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [target, setTarget] = useState('')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState('')
  const [compacting, setCompacting] = useState(false)
  const [compactNotice, setCompactNotice] = useState('')
  const [showDetails, setShowDetails] = useState(false)
  const [archives, setArchives] = useState<ArchiveListResult | null>(null)
  const [extractFrom, setExtractFrom] = useState('')
  const [extractTo, setExtractTo] = useState('')
  const [extractTable, setExtractTable] = useState('')
  const [extractTarget, setExtractTarget] = useState('')
  const [extracting, setExtracting] = useState(false)
  const [extractError, setExtractError] = useState('')
  const [extracted, setExtracted] = useState<ArchiveExtractResult | null>(null)
  const { restarting, restartMessage, restartError, triggerRestart } = useSystemRestart(api)

  const loadArchives = useCallback(async () => {
    try {
      const list = await api.request<ArchiveListResult>('/storage/archives')
      if (list && typeof list === 'object' && 'summary' in list) {
        setArchives(list)
      }
    } catch {
      // Optional detail fetch should never block main storage page rendering.
    }
  }, [api])

  const loadStatus = useCallback(async () => {
    try {
      const next = await api.request<StorageStatus>('/storage')
      setStatus(next)
      setLoadError('')
      setTarget((prev) => {
        if (prev) return prev
        const preferred = next.drives?.find((d) => !d.current && d.suggestedPath)
        return preferred?.suggestedPath || ''
      })
      void loadArchives()
    } catch (err: unknown) {
      setLoadError(err instanceof Error ? err.message : '读取存储信息失败')
    } finally {
      setLoading(false)
    }
  }, [api, loadArchives])

  useEffect(() => {
    void loadStatus()
  }, [loadStatus])

  const migration = status?.migration
  const isMigrating = !!migration && ACTIVE_STATES.has(migration.state)

  // Poll live byte/file progress while migration runs in the background.
  useEffect(() => {
    if (!isMigrating) return
    const timer = setInterval(() => {
      void loadStatus()
    }, 350)
    return () => clearInterval(timer)
  }, [isMigrating, loadStatus])

  const canEdit = !guest && allows(scope, 'config')

  const requestStart = () => {
    setSubmitError('')
    const trimmed = target.trim()
    if (!trimmed) {
      setSubmitError(t('storageTargetHint'))
      return
    }
    gate('config', t('storageMigrateTitle'), () => setConfirmOpen(true))
  }

  const runCompact = () => {
    setSubmitError('')
    setCompactNotice('')
    gate('config', t('storageCompactBtn'), () => {
      setCompacting(true)
      void api
        .request<StorageCompactResult>('/storage/compact', { method: 'POST' })
        .then((res) => {
          if (res?.storage) setStatus(res.storage)
          if (res && res.freedBytes > 32 * 1024) {
            setCompactNotice(
              t('storageCompactDone', {
                freed: formatBytes(res.freedBytes),
                before: formatBytes(res.beforeBytes),
                after: formatBytes(res.afterBytes),
              }),
            )
          } else if (res) {
            setCompactNotice(t('storageCompactAlreadyLean', { after: formatBytes(res.afterBytes) }))
          }
          void loadArchives()
        })
        .catch((err: unknown) => {
          setSubmitError(err instanceof Error ? err.message : '压缩存储空间失败')
        })
        .finally(() => setCompacting(false))
    })
  }

  const runExtract = async (overrideFrom?: number, overrideTo?: number) => {
    setExtracting(true)
    setExtractError('')
    try {
      const params = new URLSearchParams()
      const fromMS = overrideFrom ?? parseLocalDateTimeInput(extractFrom)
      const toMS = overrideTo ?? parseLocalDateTimeInput(extractTo)
      if (fromMS > 0) params.set('from', String(fromMS))
      if (toMS > 0) params.set('to', String(toMS))
      if (extractTable) params.set('table', extractTable)
      if (extractTarget.trim()) params.set('target', extractTarget.trim())
      params.set('limit', '500')
      const query = params.toString()
      const res = await api.request<ArchiveExtractResult>('/storage/archives/extract' + (query ? `?${query}` : ''))
      setExtracted(res)
    } catch (err: unknown) {
      setExtractError(err instanceof Error ? err.message : '提取历史归档失败')
    } finally {
      setExtracting(false)
    }
  }

  const exportExtractedJSONL = () => {
    if (!extracted || extracted.records.length === 0) return
    const lines = extracted.records.map((rec) => JSON.stringify(rec)).join('\n') + '\n'
    const blob = new Blob([lines], { type: 'application/x-ndjson' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `lgmon-archive-${Date.now()}.jsonl`
    link.click()
    URL.revokeObjectURL(url)
  }

  const startMigration = async () => {
    setConfirmOpen(false)
    setSubmitting(true)
    setSubmitError('')
    try {
      const next = await api.request<StorageStatus>('/storage/migrate', {
        method: 'POST',
        body: JSON.stringify({ target: target.trim() }),
      })
      setStatus(next)
    } catch (err: unknown) {
      setSubmitError(err instanceof Error ? err.message : '启动迁移失败')
    } finally {
      setSubmitting(false)
    }
  }

  if (loading && !status) {
    return (
      <div className="st-loading">
        <Glyph name="storage" size={18} />
        <span>…</span>
      </div>
    )
  }

  if (loadError && !status) {
    return <ErrorNotice message={loadError} />
  }

  if (!status) return null

  const drive = status.currentDrive
  const usedPct =
    drive && drive.totalBytes > 0 ? Math.min(100, Math.round((drive.usedBytes / drive.totalBytes) * 100)) : 0
  const currentStep = stepIndex(migration?.state)
  const steps = [
    t('storageStepPrepare'),
    t('storageStepCopy'),
    t('storageStepVerify'),
    t('storageStepSwitch'),
  ]
  const isOnSystemDrive = status.home.toUpperCase().startsWith('C:')

  return (
    <div className="st-root">
      {/* Active Storage Overview */}
      <section className="st-card">
        <header className="st-card-head">
          <div>
            <h3>{t('storageCurrentTitle')}</h3>
            <div className="st-path-row">
              <code className="st-path">{status.home}</code>
              <CopyButton text={status.home} />
            </div>
          </div>
          <div className="st-head-actions">
            <span className={'st-badge' + (status.redirected ? ' is-custom' : '')}>
              {status.redirected ? t('storageRedirectedBadge') : t('storageDefaultBadge')}
            </span>
            <button
              type="button"
              className="lm-btn small"
              disabled={compacting || isMigrating}
              title={t('storageCompactHint')}
              onClick={runCompact}
            >
              <Glyph name="sparkle" size={14} />
              {compacting ? t('storageCompactingBtn') : t('storageCompactBtn')}
            </button>
          </div>
        </header>

        {compactNotice && <p className="st-compact-notice">{compactNotice}</p>}

        {drive && drive.totalBytes > 0 && (
          <div className="st-disk">
            <div className="st-disk-meta">
              <span>
                {t('storageDiskSpace', {
                  root: drive.root,
                  free: formatBytes(drive.freeBytes),
                  total: formatBytes(drive.totalBytes),
                })}
              </span>
              <strong>{usedPct}%</strong>
            </div>
            <div className="st-bar" aria-hidden="true">
              <i className={usedPct >= 88 ? 'is-warn' : ''} style={{ width: usedPct + '%' }} />
            </div>
          </div>
        )}

        <div className="st-breakdown">
          <div className="st-stat">
            <span>{t('storageBreakdownDb')}</span>
            <strong>{formatBytes(status.dbBytes)}</strong>
            {((status.walBytes ?? 0) > 1024 * 1024 || (status.freelistBytes ?? 0) > 1024 * 1024) && (
              <small>
                {t('storageDbMetaHint', {
                  wal: formatBytes(status.walBytes ?? 0),
                  free: formatBytes(status.freelistBytes ?? 0),
                })}
              </small>
            )}
          </div>
          <div className="st-stat">
            <span>{t('storageBreakdownArchive')}</span>
            <strong>{formatBytes(status.archiveBytes ?? 0)}</strong>
          </div>
          <div className="st-stat">
            <span>{t('storageBreakdownConfig')}</span>
            <strong>{formatBytes(status.configBytes)}</strong>
          </div>
          <div className="st-stat">
            <span>{t('storageBreakdownLogs')}</span>
            <strong>{formatBytes(status.logsBytes)}</strong>
          </div>
          <div className="st-stat">
            <span>{t('storageBreakdownOther')}</span>
            <strong>{formatBytes(status.otherBytes)}</strong>
          </div>
          <div className="st-stat is-total">
            <span>{t('storageFilesCount', { count: status.fileCount })}</span>
            <strong>{formatBytes(status.dataBytes)}</strong>
          </div>
        </div>

        <p className="st-autoclean-note">{t('storageAutoCleanNote')}</p>

        {((status.topFiles && status.topFiles.length > 0) || (status.tables && status.tables.length > 0)) && (
          <>
            <button
              type="button"
              className="lm-btn small st-details-toggle"
              onClick={() => setShowDetails((prev) => !prev)}
            >
              {showDetails ? t('storageDetailsHide') : t('storageDetailsShow')}
            </button>
            {showDetails && (
              <div className="st-details-grid">
                {status.topFiles && status.topFiles.length > 0 && (
                  <div className="st-detail-box">
                    <h4>{t('storageTopFilesTitle')}</h4>
                    <ul className="st-detail-list">
                      {status.topFiles.map((file) => (
                        <li key={file.path}>
                          <span className="st-detail-name" title={file.path}>
                            {file.path}
                          </span>
                          <span className="st-detail-val">{formatBytes(file.size)}</span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
                {status.tables && status.tables.length > 0 && (
                  <div className="st-detail-box">
                    <h4>{t('storageTopTablesTitle')}</h4>
                    <ul className="st-detail-list">
                      {status.tables.map((tbl) => (
                        <li key={tbl.name}>
                          <span className="st-detail-name">{tbl.name}</span>
                          <span className="st-detail-val">
                            {t('storageRowsCount', { count: tbl.rows.toLocaleString() })} ·{' '}
                            {formatBytes(tbl.payloadBytes)}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            )}
          </>
        )}
      </section>

      {/* Compressed History Archives & On-Demand Extraction */}
      <section className="st-card">
        <header className="st-card-head">
          <div>
            <h3>{t('storageArchiveTitle')}</h3>
            <p className="st-lead">{t('storageArchiveLead')}</p>
          </div>
        </header>

        {!archives || archives.summary.chunkCount === 0 ? (
          <p className="st-autoclean-note">{t('storageArchiveEmpty')}</p>
        ) : (
          <>
            <div className="st-archive-summary">
              {t('storageArchiveSummary', {
                chunks: archives.summary.chunkCount,
                records: archives.summary.totalRecords.toLocaleString(),
                compressed: formatBytes(archives.summary.compressedBytes),
                raw: formatBytes(archives.summary.uncompressedBytes),
                ratio:
                  archives.summary.uncompressedBytes > 0
                    ? Math.max(
                        0,
                        Math.round(
                          (1 - archives.summary.compressedBytes / archives.summary.uncompressedBytes) * 100,
                        ),
                      )
                    : 0,
              })}
            </div>

            <div className="st-chunk-list">
              {archives.chunks.map((chunk) => (
                <div key={chunk.id} className="st-chunk-row">
                  <div className="st-chunk-meta">
                    <strong>
                      {chunk.day} · {chunk.tier}
                    </strong>
                    <span>
                      {formatArchiveTime(chunk.minTs)} ~ {formatArchiveTime(chunk.maxTs)} ·{' '}
                      {t('storageRowsCount', { count: chunk.recordCount.toLocaleString() })} ·{' '}
                      {formatBytes(chunk.compressedBytes)}
                    </span>
                  </div>
                  <button
                    type="button"
                    className="lm-btn small"
                    disabled={extracting}
                    onClick={() => {
                      setExtractFrom(toLocalDateTimeInput(chunk.minTs))
                      setExtractTo(toLocalDateTimeInput(chunk.maxTs + 60_000))
                      void runExtract(chunk.minTs, chunk.maxTs)
                    }}
                  >
                    {t('storageArchiveQuickPick')}
                  </button>
                </div>
              ))}
            </div>

            <div className="st-extract-filters">
              <div className="st-extract-field">
                <label htmlFor="st-extract-from">{t('storageArchiveFrom')}</label>
                <input
                  id="st-extract-from"
                  type="datetime-local"
                  value={extractFrom}
                  onChange={(e) => setExtractFrom(e.target.value)}
                />
              </div>
              <div className="st-extract-field">
                <label htmlFor="st-extract-to">{t('storageArchiveTo')}</label>
                <input
                  id="st-extract-to"
                  type="datetime-local"
                  value={extractTo}
                  onChange={(e) => setExtractTo(e.target.value)}
                />
              </div>
              <div className="st-extract-field">
                <label htmlFor="st-extract-table">{t('storageArchiveTable')}</label>
                <select
                  id="st-extract-table"
                  value={extractTable}
                  onChange={(e) => setExtractTable(e.target.value)}
                >
                  <option value="">{t('storageArchiveAllTables')}</option>
                  {ARCHIVABLE_TABLES.map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
              </div>
              <div className="st-extract-field">
                <label htmlFor="st-extract-target">{t('storageArchiveTarget')}</label>
                <input
                  id="st-extract-target"
                  type="text"
                  value={extractTarget}
                  placeholder="nebula"
                  onChange={(e) => setExtractTarget(e.target.value)}
                />
              </div>
            </div>

            <div className="st-extract-actions">
              <button
                type="button"
                className="lm-btn primary"
                disabled={extracting}
                onClick={() => void runExtract()}
              >
                {extracting ? t('storageArchiveExtractingBtn') : t('storageArchiveExtractBtn')}
              </button>
              {extracted && extracted.records.length > 0 && (
                <button type="button" className="lm-btn" onClick={exportExtractedJSONL}>
                  {t('storageArchiveExportBtn')}
                </button>
              )}
            </div>

            {extractError && <ErrorNotice message={extractError} />}

            {extracted && (
              <>
                <p className="st-autoclean-note">
                  {t('storageArchiveExtractedCount', {
                    chunks: extracted.chunksScanned,
                    total: extracted.total,
                    returned: extracted.returned,
                  })}
                </p>
                {extracted.records.length === 0 ? (
                  <p className="st-autoclean-note">{t('storageArchiveNoMatch')}</p>
                ) : (
                  <div className="st-extract-table-wrap">
                    <table className="st-extract-table">
                      <thead>
                        <tr>
                          <th>{t('storageArchiveFrom')}</th>
                          <th>{t('storageArchiveTable')}</th>
                          <th>Target</th>
                          <th>Row</th>
                        </tr>
                      </thead>
                      <tbody>
                        {extracted.records.slice(0, 50).map((rec, idx) => (
                          <tr key={`${rec.table}-${rec.ts}-${idx}`}>
                            <td>{formatArchiveTime(rec.ts)}</td>
                            <td>
                              <code>{rec.table}</code>
                            </td>
                            <td>{rec.targetId || rec.collectorId || '—'}</td>
                            <td>
                              <pre className="st-extract-json">{JSON.stringify(rec.row)}</pre>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </>
            )}
          </>
        )}
      </section>

      {/* Live Migration Progress Card */}
      {migration && migration.state !== 'idle' && (
        <section
          className={'st-card st-progress-card is-' + migration.state}
          role="status"
          aria-live="polite"
        >
          <header className="st-prog-head">
            <div className="st-prog-title">
              <Glyph
                name={
                  migration.state === 'done' ? 'check' : migration.state === 'error' ? 'refresh' : 'storage'
                }
                size={18}
              />
              <div>
                <h3>
                  {migration.state === 'done'
                    ? t('storageDoneTitle')
                    : migration.state === 'error'
                      ? t('storageErrorTitle')
                      : t('storageProgressTitle')}
                </h3>
                {migration.target && <p className="st-prog-target">{migration.target}</p>}
              </div>
            </div>
            <strong className="st-prog-pct">{migration.percent}%</strong>
          </header>

          <div
            className="st-prog-bar"
            role="progressbar"
            aria-valuenow={migration.percent}
            aria-valuemin={0}
            aria-valuemax={100}
          >
            <i style={{ width: migration.percent + '%' }} />
          </div>

          <ol className="st-steps">
            {steps.map((label, idx) => {
              const done = currentStep > idx || migration.state === 'done'
              const active = currentStep === idx && isMigrating
              return (
                <li key={label} data-done={done || undefined} data-active={active || undefined}>
                  <span className="st-step-dot">{done ? '✓' : idx + 1}</span>
                  <span>{label}</span>
                </li>
              )
            })}
          </ol>

          {isMigrating && (
            <div className="st-prog-meta">
              <span>
                {migration.currentFile
                  ? `${t('storageCurrentFile')}: ${migration.currentFile}`
                  : t('storageStepPrepare')}
              </span>
              <span className="st-prog-nums">
                {formatBytes(migration.copiedBytes)} / {formatBytes(migration.totalBytes)} ·{' '}
                {migration.copiedFiles} / {migration.totalFiles}
              </span>
            </div>
          )}

          {migration.state === 'done' && (
            <div className="st-done-box">
              <p>{t('storageDoneBody', { target: migration.target || '' })}</p>
              <p className="st-done-note">{t('storageDoneBackupNote')}</p>
              <div className="st-done-actions">
                <button
                  type="button"
                  className="lm-btn primary"
                  disabled={restarting}
                  onClick={() => gate('config', t('restartServiceBtn'), () => void triggerRestart())}
                >
                  <Glyph name="refresh" size={14} />
                  {restarting ? t('restartInProgress') : t('restartNowBtn')}
                </button>
                {restarting && <span className="st-done-note">{restartMessage || t('restartWaiting')}</span>}
              </div>
              {restartError && <ErrorNotice message={restartError} />}
            </div>
          )}

          {migration.state === 'error' && migration.error && <ErrorNotice message={migration.error} />}
        </section>
      )}

      {/* Migrate Data Form */}
      <section className="st-card">
        <header className="st-card-head">
          <div>
            <h3>{t('storageMigrateTitle')}</h3>
            <p className="st-lead">{t('storageMigrateLead')}</p>
          </div>
        </header>

        {!canEdit && <p className="st-readonly">{t('storageReadOnlyNotice')}</p>}

        {status.drives && status.drives.length > 0 && (
          <div className="st-drives-wrap">
            <p className="st-label">{t('storagePickDrive')}</p>
            <div className="st-drives">
              {status.drives.map((vol) => {
                const suggested = vol.suggestedPath || `${vol.root}lgmon-data`
                const selected = target.trim().toLowerCase() === suggested.toLowerCase()
                const recommended =
                  isOnSystemDrive && !vol.current && !vol.root.toUpperCase().startsWith('C:')
                const volUsedPct =
                  vol.totalBytes > 0 ? Math.min(100, Math.round((vol.usedBytes / vol.totalBytes) * 100)) : 0
                return (
                  <button
                    key={vol.root}
                    type="button"
                    className={'st-drive' + (selected ? ' is-selected' : '')}
                    disabled={isMigrating}
                    onClick={() => {
                      setTarget(suggested)
                      setSubmitError('')
                    }}
                  >
                    <div className="st-drive-top">
                      <strong>{vol.root}</strong>
                      {recommended && <span className="st-tag is-rec">{t('storageRecommended')}</span>}
                      {vol.current && <span className="st-tag">{t('storageCurrentDriveTag')}</span>}
                    </div>
                    <span className="st-drive-path">{suggested}</span>
                    <div className="st-drive-bar" aria-hidden="true">
                      <i style={{ width: volUsedPct + '%' }} />
                    </div>
                    <small className="st-drive-free">
                      {t('storageFreeLabel', { free: formatBytes(vol.freeBytes) })} /{' '}
                      {formatBytes(vol.totalBytes)}
                    </small>
                  </button>
                )
              })}
            </div>
          </div>
        )}

        <div className="st-field">
          <label htmlFor="st-target-input">{t('storageTargetLabel')}</label>
          <div className="st-input-row">
            <input
              id="st-target-input"
              type="text"
              value={target}
              disabled={isMigrating || submitting}
              placeholder={t('storageTargetPlaceholder')}
              onChange={(event) => {
                setTarget(event.target.value)
                setSubmitError('')
              }}
            />
            <button
              type="button"
              className="lm-btn primary"
              disabled={isMigrating || submitting || !target.trim()}
              onClick={requestStart}
            >
              <Glyph name="storage" size={15} />
              {submitting
                ? t('storageStartingBtn')
                : migration?.state === 'error'
                  ? t('storageRetryBtn')
                  : t('storageStartBtn')}
            </button>
          </div>
          <p className="st-hint">{t('storageTargetHint')}</p>
        </div>

        {submitError && <ErrorNotice message={submitError} />}
      </section>

      {confirmOpen && (
        <Dialog title={t('storageConfirmTitle')} close={() => setConfirmOpen(false)}>
          <p className="st-confirm-body">{t('storageConfirmBody', { target: target.trim() })}</p>
          <div className="st-confirm-actions">
            <button type="button" className="lm-btn" onClick={() => setConfirmOpen(false)}>
              {t('storageCancelBtn')}
            </button>
            <button type="button" className="lm-btn primary" onClick={() => void startMigration()}>
              {t('storageConfirmBtn')}
            </button>
          </div>
        </Dialog>
      )}
    </div>
  )
}
