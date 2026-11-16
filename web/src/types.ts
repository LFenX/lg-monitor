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
  config?: {
    state: 'applied' | 'pending' | 'rejected' | 'outdated'
    version: string
    message: string
  }
  cache?: {
    bytes: number
    maxBytes: number
    bytesPerHour: number | null
    projectedHours: number | null
  } | null
}
export type Target = {
  id: string
  name?: string
  tags?: string[]
  collector?: string
  source?: { kind: 'service-switch'; collector: string; ref: string }
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

export type AlertEvents = {
  alertId: string
  from: number | null
  to: number | null
  events: {
    ts: number
    channel: string
    provider: string
    eventId: number
    level: number
    recordId: string
    subject: string
    message: string
  }[]
  nextCursor: string | null
}
export type Notifier = {
  id: string
  type: string
  app?: string
  chatId?: string
  openId?: string
  chatName?: string
  template?: string
  cardActions?: boolean
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
  tlsNotAfter?: number | null
  tlsSubject?: string
}
export type ProbeCertificate = { probe: number; ts: number; tlsNotAfter: number; tlsSubject: string }
export type History = {
  from: number
  to: number
  step: string
  states: TargetState[]
  probes: Probe[]
  processes: Metric[]
  notes: string[]
  certificates?: ProbeCertificate[]
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
export type RuleError = {
  ruleId: string
  ruleName: string
  ruleType: string
  error: string
  at: number
  consecutive: number
  active: boolean
}
export type Snapshot = {
  pause: NotificationPause
  collectors: Collector[]
  targets: TargetState[]
  definitions: Target[]
  alerts: Alert[]
  notifiers: Notifiers
  summaries: Summary[]
  ruleErrors: RuleError[]
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

export type GuideStep = { id: string; title: string; done: boolean; how: string; tool?: string }
export type Guide = {
  product: string
  version: string
  scope: Scope
  canConfigure: boolean
  install: { executable: string; home: string; personal: boolean; url: string }
  state: {
    collectors: number
    online: number
    monitors: number
    healthy: number
    firingAlerts: number
    dashboards: number
    boards?: number
    notifiers: number
    agentSeenAt: number
    agentName?: string
  }
  collectors: { id: string; hostname: string; online: boolean }[]
  steps: GuideStep[]
  workflow: string[]
  rules: string[]
  modules?: { name: string; purpose: string; tools: string[] }[]
}
export type Beat = 'up' | 'down' | 'unknown' | ''
export type Health = 'up' | 'down' | 'unknown'
export type Monitor = {
  id: string
  name: string
  collector?: string
  watching: string[]
  state: Health
  hosts: { collectorId: string; hostname: string; state: string; reason?: string; ts: number }[]
  rules: { id: string; type: string; severity: string }[]
  firing: number
  beats: Beat[]
  beatSlots?: StatusSlot[]
  uptime: number | null
  latencyMs: number | null
  kind: 'port' | 'url' | 'service' | 'process' | 'task' | 'other'
  dashboard?: string
  url?: string
  parent?: string
  children?: string[]
  depth?: number
  boards?: string[]
  overall?: Health
  issue?: string
  groupBeats?: Beat[]
  groupBeatSlots?: StatusSlot[]
  groupUptime?: number | null
  monitorUrl?: string
  uptime7d?: number | null
  uptime30d?: number | null
  days?: StatusSlot[]
  latency?: { ts: number; ms: number }[]
}
export type BoardStats = {
  services: number
  children: number
  up: number
  down: number
  unknown: number
  uptime: number | null
  state: Health | 'empty'
}
export type BoardInfo = {
  id: string
  name: string
  description?: string
  monitors: string[]
  summary: BoardStats
  url: string
  cardStyle?: CardStyle
  cards?: Record<string, CardStyle>
}
export type CardStyle = 'pulse' | 'minimal' | 'ring' | 'latency' | 'days' | 'number'
export type BoardView = BoardInfo & { items: Monitor[]; unassigned: string[] }
export type StatusSegment = { from: number; to: number; state: Health }
export type StatusSlot = { from: number; state: Beat; uptime: number | null; segments?: StatusSegment[] }
export type IncidentAlert = { startedAt: number; resolvedAt?: number; severity: string; summary: string }
export type MonitorIncident = {
  monitorId: string
  monitorName: string
  depth: number
  kind: 'down' | 'alert'
  from: number
  to: number
  durationMs: number
  reason?: string
  host?: string
  alerts: IncidentAlert[]
}
export type MonitorEvent = {
  ts: number
  kind: 'up' | 'down' | 'unknown' | 'alert' | 'resolved'
  text: string
  host?: string
}
export type MonitorDetail = {
  monitor: Monitor
  lineage: { id: string; name: string; overall: Health }[]
  children: Monitor[]
  boards: { id: string; name: string }[]
  slots: StatusSlot[]
  days: StatusSlot[]
  uptime24h: number | null
  uptime7d: number | null
  uptime30d: number | null
  latency: { ts: number; ms: number }[]
  latencyNow: number | null
  latencyAvg: number | null
  certExpiresAt?: number
  events: MonitorEvent[]
  incidents?: MonitorIncident[]
  historyNotice?: string
  rules: { id: string; type: string; severity: string; text: string; notify: string[] }[]
}
export type ExploreKind = 'ports' | 'services' | 'processes' | 'tasks'
export type ExploreItem = {
  kind: 'port' | 'service' | 'process' | 'task'
  name: string
  displayName?: string
  task?: string
  lastRun?: number
  result?: string
  state?: string
  startType?: string
  pid?: number
  port?: number
  address?: string
  process?: string
  executable?: string
  cpuPercent?: number
  rss?: number
  services?: string[]
  monitoredBy?: string
  suggest?: { name: string; port?: number; process?: string; service?: string; task?: string }
}
export type ExploreResult = {
  collector: { id: string; hostname: string; online: boolean }
  kind: ExploreKind
  collectedAt: number
  total: number
  matched: number
  counts: Record<string, number>
  items: ExploreItem[]
  offset: number
  limit: number
  notes?: string[]
}
export type BoardSummary = {
  id: string
  title: string
  file: string
  version: string
  panelCount: number
  error: string | null
  html?: boolean
  description?: string
  theme?: string
  icon?: string
  types?: string[]
  layout?: { type: string; width: number }[]
}
export type BoardTemplate = {
  id: string
  title: string
  description: string
  theme?: string
  icon: string
  targeted: boolean
  panels: string[]
  layout: { type: string; width: number }[]
  example: string
}
export type AssistantStatus = {
  name: string
  configured: boolean
  deepseekKey: boolean
  model: string
  models: string[]
  dashscopeKey: boolean
  region: 'beijing' | 'singapore'
  embeddingModel: string
  rerankModel: string
  docs: number
  problem?: string
  setup: string
}
export type LANState = {
  visible: boolean
  name: string
  hostname: string
  fingerprint: string
  addresses: string[]
  listening: boolean
  problem?: string
  pending: number
  viewers: number
}
export type LANBoardCard = {
  id: string
  name: string
  description?: string
  services: number
  children: number
}
export type LANDirectory = { name: string; hostname: string; fingerprint: string; boards: LANBoardCard[] }
export type LANPeer = {
  name: string
  address: string
  fingerprint: string
  directory?: LANDirectory
  problem?: string
}
export type LANStatus = 'pending' | 'approved' | 'denied' | 'expired' | 'revoked'
export type LANOutgoing = {
  id: string
  address: string
  ownerName: string
  fingerprint: string
  boardId: string
  boardName: string
  code: string
  status: LANStatus
  createdAt: number
}
export type LANIncoming = {
  id: string
  boardId: string
  boardName: string
  deviceName: string
  hostname: string
  address: string
  memberId?: string
  code: string
  status: LANStatus
  createdAt: number
  decidedAt?: number
}
export type LANGrant = {
  id: string
  memberId: string
  boardId: string
  boardName: string
  deviceName: string
  hostname: string
  createdAt: number
}
export type LANRemoteBoard = { id: string; ownerName: string; address: string; board: BoardView }
