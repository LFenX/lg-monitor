// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { Avatar } from './avatar'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { useApp, useResource } from './state'
import { Dot } from './status'
import type { CrossDeviceRelation, CrossDeviceRelations, RelationEnd } from './types'
import './relations.css'

const shown = 3

function endLink(end: RelationEnd) {
  return end.current
    ? '#/monitors/' + encodeURIComponent(end.id)
    : '#/cloud/' + encodeURIComponent(end.device) + '/monitors/' + encodeURIComponent(end.id)
}

function troubled(end: RelationEnd) {
  return end.state !== 'up' || !end.online
}

function endState(end: RelationEnd): 'up' | 'down' | 'unknown' {
  if (!end.online) return 'unknown'
  return end.state === 'up' || end.state === 'down' ? end.state : 'unknown'
}

/* A service with whose computer it is on: services of the same name on two devices stay apart by face and name. */
function End({ end, size = 16 }: { end: RelationEnd; size?: number }) {
  const { t } = useI18n()
  return (
    <span className="lm-xdev-end" data-state={endState(end)}>
      <Avatar value={end.deviceAvatar} seed={end.device} size={size} />
      <span className="lm-xdev-names">
        <b>{end.name}</b>
        <small>{end.current ? t('cloudDeviceThis') : end.deviceName}</small>
      </span>
    </span>
  )
}

function useRelations() {
  const { api, pulse } = useApp()
  return useResource((signal) => api.request<CrossDeviceRelations>('/cloud/relations', { signal }), [pulse])
}

function sentence(relation: CrossDeviceRelation, en: boolean) {
  const from = `${relation.from.name}（${relation.from.current ? (en ? 'this PC' : '本机') : relation.from.deviceName}）`
  const to = `${relation.to.name}（${relation.to.current ? (en ? 'this PC' : '本机') : relation.to.deviceName}）`
  const issue = relation.to.issue ? (en ? ' — ' : '：') + relation.to.issue : ''
  return en ? `${from} depends on ${to}${issue}` : `${from} 依赖 ${to}${issue}`
}

/* Above "other people's boards": how many cross-device dependencies there are, between which devices, and
   which are in trouble, with a way into the full view. It stays out of the way when there are none. */
export function CrossDeviceCard() {
  const { lang } = useI18n()
  const en = lang === 'en'
  const relations = useRelations()
  const [full, setFull] = useState(false)
  const value = relations.value
  // An older lgmon answers without the list; the card simply stays away.
  if (!value || !Array.isArray(value.relations) || value.relations.length === 0) return null
  const rest = value.relations.length - shown
  return (
    <section className="lm-side-sec lm-xdev" aria-label={en ? 'Cross-device dependencies' : '跨设备依赖'}>
      <div className="lm-sec-head">
        <span className="lm-sec-title">{en ? 'Cross-device' : '跨设备依赖'}</span>
        <span className="lm-sec-meta">
          {value.problems > 0 && (
            <span className="lm-sec-pill is-bad">
              {en ? `${value.problems} issues` : `${value.problems} 个异常`}
            </span>
          )}
          <button
            type="button"
            className="lm-xdev-open"
            aria-label={en ? 'Open all cross-device dependencies' : '全屏查看跨设备依赖'}
            title={en ? 'Full view' : '展开全屏'}
            onClick={() => setFull(true)}
          >
            <Glyph name="graph" size={13} />
          </button>
        </span>
      </div>
      <p className="lm-xdev-sum">
        {en
          ? `${value.relations.length} links · ${value.devices} devices`
          : `${value.relations.length} 条关系 · ${value.devices} 台设备`}
      </p>
      <ul className="lm-xdev-list">
        {value.relations.slice(0, shown).map((relation) => (
          <li key={relation.from.device + relation.from.id + relation.to.device + relation.to.id}>
            <a
              href={endLink(relation.from)}
              title={sentence(relation, en)}
              aria-label={sentence(relation, en)}
            >
              <End end={relation.from} />
              <span className="lm-xdev-arrow" aria-hidden="true">
                →
              </span>
              <End end={relation.to} />
              <Dot state={endState(relation.to)} />
            </a>
          </li>
        ))}
      </ul>
      {rest > 0 && (
        <button type="button" className="lm-xdev-more" onClick={() => setFull(true)}>
          {en ? `${rest} more · view all` : `还有 ${rest} 条 · 查看全部`}
        </button>
      )}
      {/* The sidebar is its own stacking layer; the full view goes to the page body to cover everything. */}
      {full && createPortal(<CrossDeviceView value={value} close={() => setFull(false)} />, document.body)}
    </section>
  )
}

/* The full picture: every link, grouped by the device whose service depends on the other one, searchable. */
function CrossDeviceView({ value, close }: { value: CrossDeviceRelations; close: () => void }) {
  const { lang } = useI18n()
  const en = lang === 'en'
  const [query, setQuery] = useState('')
  useEffect(() => {
    const key = (event: KeyboardEvent) => event.key === 'Escape' && close()
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [close])
  const groups = useMemo(() => {
    const words = query.trim().toLowerCase()
    const match = (relation: CrossDeviceRelation) =>
      !words ||
      [
        relation.from.name,
        relation.from.deviceName,
        relation.to.name,
        relation.to.deviceName,
        relation.relation || '',
      ]
        .join(' ')
        .toLowerCase()
        .includes(words)
    const result = new Map<string, { end: RelationEnd; items: CrossDeviceRelation[] }>()
    for (const relation of value.relations.filter(match)) {
      const group = result.get(relation.from.device) || { end: relation.from, items: [] }
      group.items.push(relation)
      result.set(relation.from.device, group)
    }
    return [...result.values()]
  }, [value, query])
  return (
    <div
      className="lm-xdev-full"
      role="dialog"
      aria-modal="true"
      aria-label={en ? 'Cross-device dependencies' : '跨设备依赖'}
    >
      <header>
        <button type="button" className="lm-btn small" onClick={close}>
          <Glyph name="back" size={14} />
          {en ? 'Back' : '返回'}
        </button>
        <div>
          <h2>{en ? 'Cross-device dependencies' : '跨设备依赖'}</h2>
          <p>
            {en
              ? `${value.relations.length} links between ${value.devices} devices · ${value.problems} with issues. A → B means A depends on B.`
              : `${value.devices} 台设备之间 ${value.relations.length} 条关系 · ${value.problems} 条有异常。A → B 表示 A 依赖 B。`}
          </p>
        </div>
        <input
          className="lm-input"
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={en ? 'Find a service or device' : '搜索服务或设备'}
          aria-label={en ? 'Find a service or device' : '搜索服务或设备'}
        />
      </header>
      <div className="lm-xdev-groups">
        {groups.length === 0 && <p className="lm-muted">{en ? 'Nothing matches.' : '没有匹配的关系。'}</p>}
        {groups.map((group) => (
          <section
            key={group.end.device}
            aria-label={group.end.current ? (en ? 'This PC' : '本机') : group.end.deviceName}
          >
            <h3>
              <Avatar value={group.end.deviceAvatar} seed={group.end.device} size={20} />
              {group.end.current ? (en ? 'This PC' : '本机') : group.end.deviceName}
              {!group.end.online && <small>{en ? 'offline' : '离线'}</small>}
            </h3>
            <ul>
              {group.items.map((relation) => (
                <li
                  key={relation.from.id + relation.to.device + relation.to.id}
                  data-trouble={troubled(relation.from) || troubled(relation.to) || undefined}
                >
                  <a href={endLink(relation.from)} onClick={close} title={relation.from.name}>
                    <Dot state={endState(relation.from)} />
                    <b>{relation.from.name}</b>
                  </a>
                  <span className="lm-xdev-rel">
                    {en ? 'depends on' : '依赖'}
                    {relation.relation ? ' · ' + relation.relation : ''} →
                  </span>
                  <a href={endLink(relation.to)} onClick={close} title={sentence(relation, en)}>
                    <End end={relation.to} size={18} />
                    <Dot state={endState(relation.to)} />
                  </a>
                  {relation.to.issue && <small className="lm-xdev-issue">{relation.to.issue}</small>}
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </div>
  )
}
