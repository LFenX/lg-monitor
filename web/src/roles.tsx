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

/* Relations read as "A … B": 官网 读写数据 订单库. */
export const relations: Record<Relation, { name: string; verb: string }> = {
  uses: { name: '依赖', verb: '依赖' },
  calls: { name: '调用', verb: '调用' },
  data: { name: '读写数据', verb: '读写' },
  via: { name: '经由', verb: '经由' },
  runs: { name: '触发', verb: '由它触发' },
  sends: { name: '发送消息', verb: '发消息给' },
}

export function roleOf(monitor: Pick<Monitor, 'role' | 'kind'>): Role {
  if (monitor.role && monitor.role in roles) return monitor.role
  return monitor.kind === 'url' ? 'web' : monitor.kind === 'task' ? 'scheduler' : 'app'
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

/* A short sample of a relation's line, for legends and lists. */
export function RelationLine({ relation }: { relation: Relation }) {
  return (
    <svg
      className="relation-line"
      data-relation={relation}
      viewBox="0 0 28 8"
      width="28"
      height="8"
      aria-hidden="true"
    >
      <line x1="1" y1="4" x2="21" y2="4" />
      <path d="M20 1l5 3-5 3z" />
    </svg>
  )
}
