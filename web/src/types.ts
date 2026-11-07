export type Scope = '' | 'read' | 'config' | 'admin'
export type Theme = 'light' | 'dark'
export type Collector = {
  id: string
  hostname: string
  version: string
  lastSeen: number
  connected: boolean
  online: boolean
  clockOffsetMs?: number
}
export type Target = {
  id: string
  name?: string
  tags?: string[]
  collector?: string
  probes?: { type: string }[]
}
export type TargetState = {
  collectorId: string
  targetId: string
  ts: number
  state: string
  detail: { reason?: string; process?: string; port?: string; processes?: unknown[] }
}
export type Alert = {
  id: string
  ruleId: string
  collectorId: string
  targetId: string
  state: string
  severity: string
  startedAt: number
  resolvedAt: number
  summary: string
  acknowledgedAt?: number
  flapping?: boolean
}
export type Notifier = {
  id: string
  type: string
  app?: string
  chatId?: string
  chatName?: string
  template?: string
  filter?: { severity?: string[]; tags?: string[]; collectors?: string[] }
}
export type Notifiers = {
  feishuApps?: { id: string; appId: string; domain: string }[]
  notifiers?: Notifier[]
}
export type Metric = {
  collectorId: string
  targetId?: string
  name: string
  resource?: string
  probe?: string
  ts: number
  average: number
  max: number
  samples: number
}
export type Probe = {
  probe: number
  ts: number
  samples: number
  successRate: number
  averageLatencyMs: number
  error?: string
  message?: string
}
export type History = {
  from: number
  to: number
  step: string
  states: TargetState[]
  probes: Probe[]
  processes: Metric[]
  notes: string[]
}
export type Query = { columns: string[]; rows: unknown[][]; truncated: boolean }
export type Summary = {
  collector_id: string
  ts: number
  cpu: number
  memory_percent: number
  memory_used: number
  memory_total: number
  uptime: number
}
export type Service = {
  name: string
  ts: number
  state: string
  start_type: string
  pid: number
  detail: string
}
export type Gap = { ts: number; kind: string; message: string; dropped: number }
export type Template = { id: string; name: string; preset: boolean; description?: string }
export type Snapshot = {
  pause: NotificationPause
  collectors: Collector[]
  targets: TargetState[]
  definitions: Target[]
  alerts: Alert[]
  notifiers: Notifiers
  summaries: Summary[]
}
export type NotificationPause = {
  paused: boolean
  startedAt: number | null
  until: number | null
  reason: string | null
  by: string | null
  suppressed: number | null
}
export type Period = { from: number; to: number; step: 'raw' | '1m' | '1h' }
export type StreamStatus = 'connecting' | 'connected' | 'reconnecting' | 'disconnected'
