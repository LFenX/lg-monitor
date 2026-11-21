/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { Glyph } from './glyphs'
import type { GlyphName } from './glyphs'
import type { Monitor, Relation, Role } from './types'
import './roles.css'

/*
 * The preset vocabulary of the dependency view: what a service is (its role, drawn as an icon on a tinted tile)
 * and how one service uses another (its relation, drawn as a line style). Agents set them; people read them.
 */

export const roles: Record<Role, { name: string; glyph: GlyphName }> = {
  web: { name: '网站', glyph: 'globe' },
  api: { name: '接口', glyph: 'api' },
  app: { name: '应用', glyph: 'app' },
  database: { name: '数据库', glyph: 'database' },
  cache: { name: '缓存', glyph: 'bolt' },
  queue: { name: '消息队列', glyph: 'queue' },
  storage: { name: '存储', glyph: 'storage' },
  tunnel: { name: '隧道', glyph: 'tunnel' },
  gateway: { name: '网关', glyph: 'gateway' },
  worker: { name: '后台程序', glyph: 'worker' },
  scheduler: { name: '定时任务', glyph: 'task' },
  ai: { name: '模型服务', glyph: 'sparkle' },
  mcp: { name: 'MCP 服务', glyph: 'plug' },
  external: { name: '外部服务', glyph: 'external' },
}

/*
 * Relations read as "A … B": 官网 读写数据 订单库. Each has its own colour, line and arrowhead, so a map with
 * many lines still says what each one means: filled heads for direct use, open ones for messages that may
 * arrive later, a diamond for data, a circle for watching, a square for copies kept elsewhere, and heads at
 * both ends when data flows both ways.
 */
export type Head = 'arrow' | 'open' | 'diamond' | 'circle' | 'square'
export type Dash = 'solid' | 'dashed' | 'dotted'
export const relations: Record<
  Relation,
  { name: string; verb: string; head: Head; dash: Dash; both?: boolean }
> = {
  uses: { name: '依赖', verb: '依赖', head: 'arrow', dash: 'solid' },
  calls: { name: '调用接口', verb: '调用', head: 'arrow', dash: 'solid' },
  data: { name: '读写数据', verb: '读写', head: 'diamond', dash: 'solid' },
  reads: { name: '读取', verb: '读取', head: 'open', dash: 'solid' },
  writes: { name: '写入', verb: '写入', head: 'diamond', dash: 'solid' },
  via: { name: '经由', verb: '经由', head: 'arrow', dash: 'dashed' },
  forwards: { name: '转发到', verb: '转发给', head: 'arrow', dash: 'solid' },
  runs: { name: '触发', verb: '由它触发', head: 'arrow', dash: 'dotted' },
  sends: { name: '发送消息', verb: '发消息给', head: 'open', dash: 'dashed' },
  consumes: { name: '消费消息', verb: '消费', head: 'open', dash: 'dotted' },
  auth: { name: '认证', verb: '登录走', head: 'circle', dash: 'solid' },
  syncs: { name: '双向同步', verb: '同步', head: 'arrow', dash: 'solid', both: true },
  replicates: { name: '复制到', verb: '复制到', head: 'square', dash: 'dashed' },
  backup: { name: '备份到', verb: '备份到', head: 'square', dash: 'dotted' },
  fallback: { name: '故障切换到', verb: '出故障时切换到', head: 'open', dash: 'dashed' },
  monitors: { name: '监控', verb: '检查', head: 'circle', dash: 'dotted' },
}

/* The arrowhead's outline in a 10×10 box pointing right, its tip at (10, 5). */
export function headPath(head: Head) {
  switch (head) {
    case 'open':
      return 'M1.5 1.5L9 5L1.5 8.5'
    case 'diamond':
      return 'M0 5L5 1.5L10 5L5 8.5z'
    case 'circle':
      return 'M2 5a3.5 3.5 0 1 0 7 0a3.5 3.5 0 1 0 -7 0'
    case 'square':
      return 'M2 2h7v6h-7z'
  }
  return 'M0 1L10 5L0 9z'
}
export function roleOf(monitor: Pick<Monitor, 'role' | 'kind'>): Role {
  if (monitor.role && monitor.role in roles) return monitor.role
  return monitor.kind === 'url'
    ? 'web'
    : monitor.kind === 'task' || monitor.kind === 'job'
      ? 'scheduler'
      : 'app'
}

export function RoleIcon({ role, size = 28, title }: { role: Role; size?: number; title?: string }) {
  const meta = roles[role] || roles.app
  return (
    <span
      className="role-icon"
      data-role={role}
      style={{ width: size, height: size }}
      title={title ?? meta.name}
      aria-hidden={title === '' ? true : undefined}
    >
      <Glyph name={meta.glyph} size={Math.round(size * 0.58)} />
    </span>
  )
}

/* A short sample of a relation's line, for legends and lists: its colour, dash and arrowhead. */
export function RelationLine({ relation, optional = false }: { relation: Relation; optional?: boolean }) {
  const meta = relations[relation] || relations.uses
  return (
    <svg
      className="relation-line"
      data-relation={relation}
      data-dash={optional ? 'dashed' : meta.dash}
      data-head={meta.head}
      data-optional={optional || undefined}
      viewBox="0 0 34 10"
      width="34"
      height="10"
      aria-hidden="true"
    >
      <line x1={meta.both ? 9 : 1} y1="5" x2="25" y2="5" />
      <path d={headPath(meta.head)} transform="translate(24 0)" />
      {meta.both && <path d={headPath(meta.head)} transform="translate(10 10) rotate(180)" />}
    </svg>
  )
}
