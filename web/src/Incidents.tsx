/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useState } from 'react'
import { useMonitorHref } from './shared'
import { Dot } from './status'
import type { MonitorDetail, MonitorIncident } from './types'

/*
 * What went wrong, per service: the service itself first, then each child in the tree's order. Each entry says
 * which service, when it began and ended to the second, how long it lasted and why. Long histories fold.
 */

const SHOWN = 3
const pad = (value: number) => String(value).padStart(2, '0')

export function fullTime(ts: number) {
  const date = new Date(ts)
  return (
    date.getFullYear() +
    '-' +
    pad(date.getMonth() + 1) +
    '-' +
    pad(date.getDate()) +
    ' ' +
    pad(date.getHours()) +
    ':' +
    pad(date.getMinutes()) +
    ':' +
    pad(date.getSeconds())
  )
}

/* The end only repeats the date when the outage crossed midnight. */
function endTime(from: number, to: number) {
  const whole = fullTime(to)
  return fullTime(from).slice(0, 10) === whole.slice(0, 10) ? whole.slice(11) : whole
}

export function durationText(ms: number) {
  const seconds = Math.max(0, Math.round(ms / 1000))
  const days = Math.floor(seconds / 86400),
    hours = Math.floor((seconds % 86400) / 3600),
    minutes = Math.floor((seconds % 3600) / 60),
    rest = seconds % 60
  if (days) return days + ' 天' + (hours ? ' ' + hours + ' 小时' : '')
  if (hours) return hours + ' 小时' + (minutes ? ' ' + minutes + ' 分' : '')
  if (minutes) return minutes + ' 分' + (rest ? ' ' + rest + ' 秒' : '')
  return rest + ' 秒'
}

const severityName: Record<string, string> = { critical: '严重', warning: '警告', info: '提示' }

function Entry({ item }: { item: MonitorIncident }) {
  const ongoing = item.to === 0
  return (
    <li className="lm-incident" data-kind={item.kind} data-ongoing={ongoing || undefined}>
      <Dot state={ongoing ? 'down' : item.kind === 'alert' ? 'unknown' : 'down'} />
      <div className="what">
        <strong>
          {item.kind === 'alert' ? '告警' : '异常'} · {item.monitorName}
          {ongoing && <span className="lm-incident-live">进行中</span>}
        </strong>
        {item.reason && <span className="why">{item.reason}</span>}
        {item.kind === 'down' &&
          item.alerts.map((alert) => (
            <span className="alert" key={alert.startedAt}>
              {severityName[alert.severity] || alert.severity}告警：{alert.summary}
              {alert.resolvedAt ? '（已恢复）' : ''}
            </span>
          ))}
      </div>
      <div className="when">
        <time dateTime={new Date(item.from).toISOString()}>{fullTime(item.from)}</time>
        <span>
          {ongoing ? '至今' : '→ ' + endTime(item.from, item.to)}
          {item.host ? ' · ' + item.host : ''}
        </span>
        <b>{(ongoing ? '已持续 ' : '持续 ') + durationText(item.durationMs)}</b>
      </div>
    </li>
  )
}

function Group({
  name,
  id,
  own,
  items,
}: {
  name: string
  id: string
  own: boolean
  items: MonitorIncident[]
}) {
  const [open, setOpen] = useState(false)
  const href = useMonitorHref()
  const shown = open ? items : items.slice(0, SHOWN)
  const total = items.filter((item) => item.kind === 'down').reduce((sum, item) => sum + item.durationMs, 0)
  return (
    <section className="lm-incident-group" aria-label={name + ' 的事件'}>
      <header>
        {own ? <strong>{name}</strong> : <a href={href(id)}>{name}</a>}
        <small>
          {items.length} 条{total ? ' · 异常合计 ' + durationText(total) : ''}
        </small>
      </header>
      <ul>
        {shown.map((item) => (
          <Entry key={item.kind + item.from + item.host} item={item} />
        ))}
      </ul>
      {items.length > SHOWN && (
        <button className="lm-btn ghost small" onClick={() => setOpen((value) => !value)}>
          {open ? '收起' : '展开其余 ' + (items.length - SHOWN) + ' 条'}
        </button>
      )}
    </section>
  )
}

export function Incidents({ detail }: { detail: MonitorDetail }) {
  const incidents = detail.incidents || []
  const members = [detail.monitor, ...detail.children]
  const groups = members
    .map((member) => ({ member, items: incidents.filter((item) => item.monitorId === member.id) }))
    .filter((group) => group.items.length)
  const quiet = members.length - groups.length
  const downs = incidents.filter((item) => item.kind === 'down')
  const total = downs.reduce((sum, item) => sum + item.durationMs, 0)
  return (
    <section className="lm-section" aria-label="事件">
      <header>
        <h2 className="lm-h2">事件</h2>
        <small>
          {downs.length
            ? '最近 7 天 ' + downs.length + ' 次异常，合计 ' + durationText(total)
            : '最近 7 天没有异常'}
          {detail.children.length ? '，含子服务' : ''}
        </small>
      </header>
      {groups.length ? (
        <div className="lm-incidents">
          {groups.map(({ member, items }) => (
            <Group
              key={member.id}
              id={member.id}
              name={member.name}
              own={member.id === detail.monitor.id}
              items={items}
            />
          ))}
          {quiet > 0 && detail.children.length > 0 && (
            <p className="lm-muted lm-small" style={{ margin: 0 }}>
              {quiet === members.length ? '' : '其余 ' + quiet + ' 个服务最近 7 天没有异常。'}
            </p>
          )}
        </div>
      ) : (
        <p className="lm-muted lm-small" style={{ margin: 0 }}>
          恢复后只记持续满 1 分钟的异常；正在发生的会立即出现在这里。
        </p>
      )}
    </section>
  )
}
