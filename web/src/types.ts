/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

export type Scope = '' | 'read' | 'config' | 'admin'
export type Theme = 'light' | 'dark'
// ThemeChoice is what a person picks; system follows Windows' light or dark setting.
export type ThemeChoice = Theme | 'system'
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

/* One agent connection, as lgmon mcp reported it; kind is an id from internal/agents such as claude-code. */
export type AgentSession = {
  id: string
  kind?: string
  name: string
  client?: string
  version?: string
  token?: string
  connected: boolean
  since: number
  lastSeenAt: number
  calls: number
}
export type GuideStep = { id: string; title: string; done: boolean; how: string; tool?: string }
export type Guide = {
  product: string
  version: string
  scope: Scope
  canConfigure: boolean
  /* only while Windows keeps some of lgmon's ports */
  ports?: { moved: { configured: number; actual: number }[]; fix: string[] }
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
  hosts: {
    collectorId: string
    hostname: string
    state: string
    reason?: string
    ts: number
    offline?: boolean
  }[]
  rules: { id: string; type: string; severity: string }[]
  firing: number
  beats: Beat[]
  beatSlots?: StatusSlot[]
  uptime: number | null
  latencyMs: number | null
  kind: 'port' | 'url' | 'service' | 'process' | 'task' | 'job' | 'other'
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
  // What the service is and what it uses, for people: list subtitles, the dependency map and the docs.
  role?: Role
  roleGuessed?: boolean
  description?: string
  /* Where "打开" goes: the address set for it, or one guessed from its URL or a web-like port (entryGuessed). */
  entry?: string
  entryGuessed?: boolean
  depends?: Dependency[]
  dependents?: Dependency[]
  /* Services on the account's other computers this one uses: shown apart, never part of its own state. */
  external?: Dependency[]
  hasDoc?: boolean
  // A batch job's latest day with news; its records come from /jobs/{id}/days.
  job?: JobBrief
  /* Only on dependency-map nodes standing for a service on another computer of the account. */
  externalOf?: { device: string; deviceName: string; id: string }
}
export type JobStatus =
  'pending' | 'running' | 'success' | 'partial' | 'failed' | 'missed' | 'unknown' | 'disabled'
export type JobBrief = { date: string; status: JobStatus; summary: string }
export type JobStageInfo = { id: string; name?: string; critical?: boolean; receipt?: boolean }
export type JobStageView = {
  id: string
  name: string
  state: 'pending' | 'running' | 'success' | 'failed' | 'unknown' | 'skipped'
  progress?: number
  startedAt?: number
  finishedAt?: number
  error?: string
  receipt?: string
  critical?: boolean
  needsReceipt?: boolean
  updates: number
}
export type JobPartition = {
  region: string
  regionName: string
  hour: number
  hourLabel: string
  uniqueRecipients?: number
  uniqueSenders?: number
  receivedRows?: number
  sentRows?: number
  gzFiles?: number
  compressedBytes?: number
  targetDir?: string
  verify?: string
  upload?: string
  receipt?: string
  ts: number
}
export type JobAttempt = {
  id: string
  trigger: 'scheduled' | 'manual'
  status: JobStatus
  summary: string
  startedAt: number
  finishedAt?: number
  lastEventAt: number
  events: number
  stages: JobStageView[]
  partitions: JobPartition[]
}
export type JobDay = {
  date: string
  status: JobStatus
  summary: string
  enabled: boolean
  expected?: number
  attempts: JobAttempt[]
}
export type JobFile = {
  collectorId: string
  path: string
  state: 'ok' | 'error'
  problem?: string
  problemLine?: number
  problemAt?: number
  updatedAt: number
  lastEventAt?: number
}
export type JobInfo = {
  id: string
  name: string
  schedule: string
  stages: JobStageInfo[]
  source: 'api' | 'file'
  production: string[]
  file?: JobFile
  path?: string
  collector?: string
}
export type JobDays = { job: JobInfo; days: JobDay[] }
export type JobDependency = { id: string; name: string; state: Health; issue?: string }
export type JobDayDetail = { job: JobInfo; day: JobDay; production: JobDependency[]; fetch?: JobStageView }
export type Role =
  | 'web'
  | 'api'
  | 'app'
  | 'database'
  | 'cache'
  | 'queue'
  | 'storage'
  | 'tunnel'
  | 'gateway'
  | 'worker'
  | 'scheduler'
  | 'ai'
  | 'mcp'
  | 'external'
export type Relation =
  | 'uses'
  | 'calls'
  | 'data'
  | 'reads'
  | 'writes'
  | 'via'
  | 'forwards'
  | 'runs'
  | 'sends'
  | 'consumes'
  | 'auth'
  | 'syncs'
  | 'replicates'
  | 'backup'
  | 'fallback'
  | 'monitors'
/* One dependency seen from either end: id is the other service. optional: it can fail and the service still runs. */
export type Dependency = {
  restrictedName?: string
  id: string
  relation: Relation
  note?: string
  optional?: boolean
  /* An external dependency: the other computer of the account the service lives on. */
  device?: string
  deviceName?: string
}
export type BoardStats = {
  services: number
  children: number
  up: number
  down: number
  unknown: number
  uptime: number | null
  state: Health | 'empty'
  external?: number
  externalDown?: number
}
/* A service on another computer of the same cloud account that this board's services use. */
export type ExternalService = {
  device: string
  deviceName: string
  deviceAvatar?: string
  online: boolean
  id: string
  name: string
  role?: Role
  state: Health
  issue?: string
  uptime?: number | null
  parent?: string
  users: string[]
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
export type BoardGraphNode = {
  id: string
  name: string
  parent?: string
  restricted?: boolean
  depends?: Dependency[]
}
export type BoardView = BoardInfo & {
  items: Monitor[]
  unassigned: string[]
  graph?: BoardGraphNode[]
  external?: ExternalService[]
}
/* summary: only the hourly summary is left for this range, so it carries a share, not exact failure times. */
export type StatusSegment = {
  from: number
  to: number
  state: Health
  summary?: boolean
  uptime?: number | null
}
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
  /* A colleague's model in use here, when this computer has no DeepSeek key of its own. */
  sharedBy?: { name: string; avatar?: string; model: string }
}
/* This computer's account: the nickname and avatar other people see instead of the computer's name. */
export type Profile = { nickname: string; avatar: string; createdAt: number; updatedAt: number }
export type LANState = {
  visible: boolean
  web?: boolean
  webServices?: Record<string, string[]>
  webDeviceBoards?: Record<string, string[]>
  webDeviceServices?: Record<string, Record<string, string[]>>
  peers?: string[]
  webBoards?: string[]
  webAddresses?: string[]
  name: string
  /* A bundled avatar's key or a small data-URL picture. */
  avatar?: string
  hostname: string
  os?: string
  fingerprint: string
  addresses: string[]
  listening: boolean
  problem?: string
  /* allowed: lgmon opened 7445/TCP and 7446/UDP for LAN addresses; blocked: it could not. */
  firewall?: 'allowed' | 'blocked'
  firewallFix?: string
  pending: number
  viewers: number
}
export type LANVisitor = {
  ip: string
  avatar: string
  firstSeen: number
  lastSeen: number
  connections: number
}

export type AssetCounts = {
  installer: number
  linux?: number
  linuxScript?: number
  legacyInstaller: number
  checksums: number
  metadata: number
  other: number
}

export type ReleaseDownload = {
  tag: string
  downloads: number
  assets?: AssetCounts
  publishedAt?: string
}

export type GitHubStats = {
  downloads: number
  assets?: AssetCounts
  stars: number
  forks: number
  version?: string
  repoUrl: string
  updatedAt: number
  releases?: ReleaseDownload[]
  /** False while the host has never read real counts, e.g. GitHub's anonymous quota is spent on its network. */
  available?: boolean
}
export type LANBoardCard = {
  id: string
  name: string
  description?: string
  services: number
  children: number
}
export type LANDirectory = {
  name: string
  avatar?: string
  hostname?: string
  fingerprint: string
  boards: LANBoardCard[]
}
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
  ownerAvatar?: string
  ownerAccount?: string
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
  avatar?: string
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
  avatar?: string
  hostname: string
  createdAt: number
  /* The viewer's own lgmon, when this computer knows it: then they look the same as in the other lists. */
  fingerprint?: string
}
export type LANRemoteBoard = {
  id: string
  ownerName: string
  ownerAvatar?: string
  address: string
  board: BoardView
  /* When this copy was read from the owner. */
  fetchedAt?: number
}

/* Departments: groups people on the LAN make; each lives on the computer that created it. */
export type DepartmentPerson = { key: string; name: string; avatar?: string; role: 'leader' | 'member' }
export type DepartmentCard = {
  id: string
  name: string
  avatar?: string
  parent?: string
  people: DepartmentPerson[]
}
export type DepartmentHost = {
  id: string
  address?: string
  local?: boolean
  name: string
  avatar?: string
  departments: DepartmentCard[]
  problem?: string
}
export type DepartmentList = { me: string; hosts: DepartmentHost[]; pending: Record<string, string> }
export type DepartmentRequest = {
  id: string
  department: string
  key: string
  name: string
  avatar?: string
  createdAt: number
}

export type DiskVolume = {
  root: string
  totalBytes: number
  freeBytes: number
  usedBytes: number
  suggestedPath?: string
  current?: boolean
}

export type StorageMigrationState = {
  state: 'idle' | 'preparing' | 'copying' | 'verifying' | 'switching' | 'done' | 'error'
  source?: string
  target?: string
  percent: number
  copiedBytes: number
  totalBytes: number
  copiedFiles: number
  totalFiles: number
  currentFile?: string
  startedAt?: number
  updatedAt?: number
  finishedAt?: number
  needsRestart?: boolean
  error?: string
}

export type StorageStatus = {
  home: string
  defaultHome: string
  redirected: boolean
  dataBytes: number
  dbBytes: number
  configBytes: number
  logsBytes: number
  otherBytes: number
  fileCount: number
  currentDrive?: DiskVolume
  drives: DiskVolume[]
  migration?: StorageMigrationState
}

export type StorageCompactResult = {
  beforeBytes: number
  afterBytes: number
  freedBytes: number
  storage: StorageStatus
}

export type SystemRestartResult = {
  ok: boolean
  mode: 'service' | 'process'
  message: string
}

export type SystemUpdateState = {
  state:
    | 'window'
    | 'dev'
    | 'idle'
    | 'checking'
    | 'none'
    | 'available'
    | 'downloading'
    | 'ready'
    | 'applying'
    | 'error'
  during?: 'check' | 'download' | 'apply' | ''
  message?: string
  current?: string
  version?: string
  platform?: string
  notes?: string
  page?: string
  size?: number
  done?: number
  total?: number
  checkedAt?: number
}

export type CloudSyncStats = {
  bytesSent: number
  bytesReceived: number
  pushCount: number
  heartbeatCount: number
  pullCount: number
  docUploads: number
}

export type CloudStatus = {
  loggedIn: boolean
  email?: string
  hasPassword?: boolean
  deviceId: string
  deviceName: string
  os?: string
  serverUrl: string
  paused: boolean
  syncing: boolean
  lastPushAt?: number
  lastPullAt?: number
  lastError?: string
  devicesCount: number
  /* false for a sign-in that ends when lgmon or the computer restarts */
  remember?: boolean
  /* the account this computer last signed in to and still has a device record with */
  lastEmail?: string
  /* only on the reply to a sign-in: a new device, or the record this computer already had */
  binding?: 'new' | 'known'
  stats: CloudSyncStats
}

export type CloudDeviceSummary = {
  id: string
  name: string
  avatar?: string
  os?: string
  version?: string
  isCurrent: boolean
  /* another record this same computer left in the account */
  sameMachine?: boolean
  online: boolean
  lastSeen: number
  stateUpdated?: number
  totalCount: number
  downCount: number
  boards: BoardInfo[]
}

export type CloudBoardView = {
  deviceId: string
  deviceName: string
  deviceAvatar?: string
  deviceOs?: string
  online: boolean
  lastSeen: number
  stateUpdated?: number
  board: BoardView
}

/* Device monitoring: one computer's latest system sample. Load is absent on Windows; swap is the page file there. */
export type DeviceSystem = {
  ts: number
  cpuPercent: number
  memoryTotal: number
  memoryUsed: number
  memoryAvailable: number
  memoryPercent: number
  uptimeSeconds: number
  disks: { path: string; total: number; used: number; free: number; usedPercent: number }[]
  networks: {
    name: string
    bytesSent: number
    bytesReceived: number
    sendBytesPerSec: number
    receiveBytesPerSec: number
  }[]
  cpuCores?: number
  corePercent?: number[]
  load1?: number
  load5?: number
  load15?: number
  swapTotal?: number
  swapUsed?: number
  swapPercent?: number
  memoryCached?: number
}
export type ProcessUsage = {
  name: string
  /* The script or module an interpreter such as node or python runs. */
  detail?: string
  /* The owner's name for this program, or the monitored service it belongs to. */
  displayName?: string
  count: number
  pid?: number
  cpuPercent: number
  rss: number
}
export type ContainerUsage = {
  id: string
  name: string
  cpuPercent: number
  memoryUsed: number
  memoryLimit: number
  memoryPercent: number
  pids: number
}
export type RelationEnd = {
  device: string
  deviceName: string
  deviceAvatar?: string
  current?: boolean
  online: boolean
  id: string
  name: string
  state: Health
  issue?: string
}
export type CrossDeviceRelation = { from: RelationEnd; to: RelationEnd; relation?: string }
export type CrossDeviceRelations = { relations: CrossDeviceRelation[]; devices: number; problems: number }

export type CloudDeviceSystem = {
  deviceId: string
  deviceName: string
  deviceAvatar?: string
  deviceOs?: string
  online: boolean
  lastSeen: number
  stateUpdated?: number
  devices: Device[]
}

/* Another device's CPU, memory and swap curves as they arrive with cloud sync; -1 marks a bucket with no data. */
export type DeviceSeries = {
  span: '1h' | '6h' | '24h' | '7d'
  from: number
  step: number
  cpu: number[]
  mem: number[]
  swap?: number[]
}
export type DeviceHistory = { series: DeviceSeries[] }

export type Device = {
  id: string
  hostname: string
  os?: string
  version?: string
  online: boolean
  lastSeen: number
  system: DeviceSystem | null
  history?: DeviceHistory
  usage?: {
    ts: number
    processCount: number
    top: ProcessUsage[]
    containers: ContainerUsage[]
    containerError?: string
  }
}
export type DeviceAlert = { enabled?: boolean; threshold?: number; for?: string }
export type DeviceAlerts = {
  memory?: DeviceAlert
  swap?: DeviceAlert
  cpu?: DeviceAlert
  disk?: DeviceAlert
  containerMemory?: DeviceAlert
  notify?: string[]
}
