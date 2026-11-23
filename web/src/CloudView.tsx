/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { Suspense, lazy, useMemo, useState } from 'react'
import { Avatar } from './avatar'
import { BoardMap, ServiceCards, ServiceList, Stats, groupItems } from './Board'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago } from './kit'
import { PageLoading } from './loading'
import { SharedScope } from './shared'
import type { Shared } from './shared'
import { useApp, useResource } from './state'
import type { CloudBoardView } from './types'

type Translate = ReturnType<typeof useI18n>['t']

const ServicePage = lazy(() => import('./Service').then((v) => ({ default: v.ServicePage })))

export function cloudBoardHref(deviceId: string, boardId: string) {
  return '#/cloud/' + encodeURIComponent(deviceId) + '/boards/' + encodeURIComponent(boardId)
}

export function cloudMonitorHref(deviceId: string, monitorId: string) {
  return '#/cloud/' + encodeURIComponent(deviceId) + '/monitors/' + encodeURIComponent(monitorId)
}

function cloudShared(view: CloudBoardView, t: Translate): Shared {
  return {
    id: 'cloud:' + view.deviceId + ':' + view.board.id,
    boardName: view.board.name,
    ownerName: view.deviceName,
    ownerAvatar: view.deviceAvatar,
    monitors: view.board.items,
    apiPrefix: '/cloud/devices/' + encodeURIComponent(view.deviceId),
    boardHref: cloudBoardHref(view.deviceId, view.board.id),
    monitorHref: (monitorId) => cloudMonitorHref(view.deviceId, monitorId),
    badgeLabel: t('cloudBadge'),
    badgeTitle: t('cloudBadgeTitle', { name: view.deviceName }),
    supportsJobs: true,
  }
}

export function CloudBoardPage({ deviceId, boardId }: { deviceId: string; boardId: string }) {
  const { api, pulse, now } = useApp()
  const { t } = useI18n()
  const [layout, setLayout] = useState<'list' | 'cards' | 'map'>('list')

  const resource = useResource(
    (signal) =>
      api.request<CloudBoardView>(
        '/cloud/devices/' + encodeURIComponent(deviceId) + '/boards/' + encodeURIComponent(boardId),
        { signal },
      ),
    [pulse],
    deviceId + '/' + boardId,
  )

  const view = resource.value
  const groups = useMemo(() => groupItems(view?.board.items || []), [view])
  const shared = useMemo(() => (view ? cloudShared(view, t) : undefined), [view, t])

  if (!view || !shared) {
    return (
      <div className="lm-page">
        {resource.error ? (
          <div className="lm-note" data-tone="down">
            {resource.error}
            <a className="lm-btn small" href="#/">
              {t('cloudBackToBoards')}
            </a>
          </div>
        ) : (
          <PageLoading label={t('boardLoading')} />
        )}
      </div>
    )
  }

  const updatedAt = view.stateUpdated || view.lastSeen
  return (
    <SharedScope value={shared}>
      <div className="lm-page">
        <header className="lm-head">
          <div>
            <h1 className="lm-title">{view.board.name}</h1>
            <p className="lm-lead lm-remote-owner">
              <Avatar value={view.deviceAvatar} seed={view.deviceId} size={22} />
              <span>
                {t('cloudFromDevice', { name: view.deviceName })}
                {view.board.description ? ' · ' + view.board.description : ''}
              </span>
              {updatedAt > 0 && (
                <span className="lm-fresh" data-stale={!view.online || undefined}>
                  <i aria-hidden="true" />
                  {view.online
                    ? t('cloudUpdatedAgo', { ago: ago(updatedAt, now()) })
                    : `${t('cloudDeviceOffline')} · ${ago(updatedAt, now())}`}
                </span>
              )}
            </p>
          </div>
          <div className="lm-actions">
            <div className="lm-seg" role="group" aria-label={t('boardDisplayMode')}>
              <button aria-pressed={layout === 'list'} onClick={() => setLayout('list')}>
                <Glyph name="list" size={15} />
                {t('viewList')}
              </button>
              <button aria-pressed={layout === 'cards'} onClick={() => setLayout('cards')}>
                <Glyph name="cards" size={15} />
                {t('viewCards')}
              </button>
              <button aria-pressed={layout === 'map'} onClick={() => setLayout('map')}>
                <Glyph name="graph" size={15} />
                {t('viewMap')}
              </button>
            </div>
          </div>
        </header>
        <Stats board={view.board} />
        {layout === 'map' ? (
          <BoardMap board={view.board} remote />
        ) : layout === 'cards' ? (
          <ServiceCards groups={groups} board={view.board} />
        ) : (
          <ServiceList groups={groups} forceOpen={false} />
        )}
      </div>
    </SharedScope>
  )
}

export function CloudServicePage({ deviceId, monitorId }: { deviceId: string; monitorId: string }) {
  const { cloudDevices } = useApp()
  const { t } = useI18n()
  const device = cloudDevices.find((d) => d.id === deviceId)
  const firstBoard = device?.boards[0]
  const shared = useMemo<Shared>(
    () => ({
      id: 'cloud:' + deviceId,
      boardName: firstBoard?.name || device?.name || deviceId,
      ownerName: device?.name || deviceId,
      ownerAvatar: device?.avatar,
      monitors: [],
      apiPrefix: '/cloud/devices/' + encodeURIComponent(deviceId),
      boardHref: firstBoard ? cloudBoardHref(deviceId, firstBoard.id) : '#/settings/account',
      monitorHref: (id) => cloudMonitorHref(deviceId, id),
      badgeLabel: t('cloudBadge'),
      badgeTitle: t('cloudBadgeTitle', { name: device?.name || deviceId }),
      supportsJobs: true,
    }),
    [deviceId, device?.name, device?.avatar, firstBoard, t],
  )
  return (
    <SharedScope value={shared}>
      <Suspense
        fallback={
          <div className="lm-page">
            <PageLoading label={t('cloudOpeningService')} />
          </div>
        }
      >
        <ServicePage key={deviceId + ':' + monitorId} id={monitorId} />
      </Suspense>
    </SharedScope>
  )
}
