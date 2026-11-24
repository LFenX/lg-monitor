/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import type { MouseEvent } from 'react'
import { Avatar } from './avatar'
import { useI18n } from './i18n'
import { RoleIcon } from './roles'
import { Dot, percentText } from './status'
import type { BoardView, Dependency, ExternalService, Monitor } from './types'
import './external.css'

/*
 * External dependencies: services on the account's other computers that this board's services use. They come
 * from what those computers last synced, sit in their own block and never change this board's state or alerts.
 */

export function cloudMonitorHref(device: string, id: string) {
  return '#/cloud/' + encodeURIComponent(device) + '/monitors/' + encodeURIComponent(id)
}

/* A map node's id for a service on another computer; it can never collide with a local service id. */
export function externalKey(device: string, id: string) {
  return 'ext~' + device + '~' + id
}

/* The board's external services as map nodes, and its items with their external links pointing at them. */
export function externalGraph(board: BoardView): { items: Monitor[]; nodes: Monitor[] } {
  const external = board.external || []
  if (!external.length) return { items: board.items, nodes: [] }
  const nodes: Monitor[] = external.map((service) => ({
    id: externalKey(service.device, service.id),
    name: service.name,
    kind: 'other',
    state: service.state,
    watching: [],
    hosts: [],
    rules: [],
    firing: 0,
    beats: [],
    uptime: service.uptime ?? null,
    latencyMs: null,
    role: service.role || 'external',
    description: service.issue,
    depends: [],
    externalOf: { device: service.device, deviceName: service.deviceName, id: service.id },
  }))
  const items = board.items.map((item) =>
    item.external?.length
      ? {
          ...item,
          depends: [
            ...(item.depends || []),
            ...item.external.map(
              (link): Dependency => ({ ...link, id: externalKey(link.device || '', link.id) }),
            ),
          ],
        }
      : item,
  )
  return { items, nodes }
}

/* The line above the board when something it uses elsewhere is in trouble; the board's own state is untouched. */
export function ExternalNote({ board }: { board: BoardView }) {
  const { t } = useI18n()
  const external = board.external || []
  const down = external.filter((service) => service.state === 'down')
  if (!down.length) return null
  const names = down.slice(0, 3).map((service) => service.name + '（' + service.deviceName + '）')
  return (
    <p className="lm-note lm-ext-note" data-tone="down" role="status">
      {t('externalDownNote', {
        count: down.length,
        names: names.join('、') + (down.length > 3 ? t('externalMore', { count: down.length - 3 }) : ''),
      })}
      <a href="#ext-deps" onClick={scrollToExternal}>
        {t('externalSee')}
      </a>
    </p>
  )
}

function scrollToExternal(event: MouseEvent) {
  event.preventDefault()
  document.getElementById('ext-deps')?.scrollIntoView({ behavior: 'smooth' })
}

export function ExternalSection({ board }: { board: BoardView }) {
  const { t } = useI18n()
  const external = board.external || []
  if (!external.length) return null
  const names = new Map(board.items.map((item) => [item.id, item.name]))
  const down = external.filter((service) => service.state === 'down').length
  return (
    <section id="ext-deps" className="lm-ext" aria-label={t('externalTitle')}>
      <header className="lm-ext-head">
        <h2>{t('externalTitle')}</h2>
        <em data-tone={down ? 'down' : undefined}>
          {down ? t('externalDownCount', { count: down }) : t('externalCount', { count: external.length })}
        </em>
        <p>{t('externalLead')}</p>
      </header>
      <ul className="lm-ext-list">
        {external.map((service) => (
          <ExternalRow key={service.device + '/' + service.id} service={service} names={names} />
        ))}
      </ul>
    </section>
  )
}

function ExternalRow({ service, names }: { service: ExternalService; names: Map<string, string> }) {
  const { t } = useI18n()
  const users = service.users.map((id) => names.get(id) || id)
  return (
    <li className="lm-ext-row" data-state={service.state}>
      <a href={cloudMonitorHref(service.device, service.id)}>
        <RoleIcon role={service.role || 'external'} size={30} title="" />
        <span className="lm-ext-text">
          <b>{service.name}</b>
          <small>
            {service.issue ? <span className="issue">{service.issue}</span> : t('externalUsedBy', { names: users.join('、') })}
          </small>
        </span>
        <span className="lm-ext-device" title={service.online ? t('externalOnline') : t('externalOffline')}>
          <Avatar value={service.deviceAvatar} seed={service.device} size={18} />
          <span>{service.deviceName}</span>
        </span>
        <span className="lm-ext-state">
          {service.uptime != null && <small>{percentText(service.uptime)}</small>}
          <Dot state={service.state} />
        </span>
      </a>
    </li>
  )
}
