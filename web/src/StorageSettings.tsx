/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useCallback, useEffect, useState } from 'react'
import { allows, useGate } from './access'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { CopyButton, Dialog } from './parts'
import { useApp } from './state'
import { ErrorNotice } from './ui'
import type { StorageMigrationState, StorageStatus } from './types'
import './storage.css'

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
    } catch (err: unknown) {
      setLoadError(err instanceof Error ? err.message : '读取存储信息失败')
    } finally {
      setLoading(false)
    }
  }, [api])

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
          <span className={'st-badge' + (status.redirected ? ' is-custom' : '')}>
            {status.redirected ? t('storageRedirectedBadge') : t('storageDefaultBadge')}
          </span>
        </header>

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
