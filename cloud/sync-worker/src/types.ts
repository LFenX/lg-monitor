/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

export interface Env {
  DB: D1Database
  RESEND_API_KEY?: string
  MAIL_FROM?: string
  APP_NAME?: string
  /** Set to "true" ONLY during local wrangler dev without RESEND_API_KEY. */
  DEV_MODE?: string
}

export interface DeviceInfoInput {
  id: string
  name?: string
  avatar?: string
  os?: string
  version?: string
  /** Hash that stays the same for one computer, whatever happens to its data files. */
  machine?: string
}

/** Compact board metadata (layer 1: uploaded only when metaHash changes). */
export interface CompactBoard {
  id: string
  name: string
  desc?: string
  /** Top-level monitor IDs on this board. */
  m: string[]
  /** Card style default (pulse/minimal/ring/latency/days/number). */
  cs?: string
  /** Per-monitor card styles. */
  cards?: Record<string, string>
}

/** Compact monitor metadata (layer 1: uploaded only when metaHash changes). */
export interface CompactMonitorMeta {
  id: string
  /** Display name. */
  n: string
  /** Parent monitor ID, if child. */
  p?: string
  /** Kind: port/url/service/process/task/job/other. */
  k?: string
  /** Role: app/api/database/queue/storage/tunnel/gateway/worker. */
  r?: string
  /** Watching summary strings. */
  w?: string[]
  /** Short service description (<= 240 chars). */
  d?: string
  /** Entry URL. */
  e?: string
  /** Compact dependencies: [targetId, relation, optional(0|1), note]. */
  dep?: [string, string, number, string][]
  /** 16-char SHA-256 hex prefix of service Markdown doc (empty when no doc). */
  dh?: string
}

export interface CompactMeta {
  boards: CompactBoard[]
  monitors: CompactMonitorMeta[]
}

/** Latest meaningful day summary for a batch job monitor (shown on board rows). */
export interface CompactJobBrief {
  /** Business date YYYY-MM-DD (Beijing time). */
  dt: string
  /** JobStatus: pending/running/success/partial/failed/missed/unknown/disabled. */
  st: string
  /** Short summary (<= 160 chars). */
  sm: string
}

/** Compact live state for one monitor (layer 2: ~80 bytes raw before gzip). */
export interface CompactMonitorState {
  /** 0=unknown, 1=up, 2=down. */
  s: number
  /** 24h uptime in basis points (0..10000 => 0.00%..100.00%), -1 when unknown. */
  u: number
  /** Latency in ms, -1 when unknown. */
  l: number
  /** Firing alerts count. */
  f?: number
  /** Short issue reason (<= 160 chars), omitted when healthy. */
  i?: string
  /** 48-char encoded 24h beat strip: '0'=empty, '1'=up, '2'=down, '3'=unknown. */
  b?: string
  /** UTC ms start of the 48-slot 24h window when exact segments are included. */
  bs?: number
  /** Merged 24h timeline runs: [fromOffsetMS, toOffsetMS, stateCode]. */
  sg?: [number, number, number][]
  /** Latest batch job brief for kind="job" monitors. */
  j?: CompactJobBrief
  /** 16-char SHA-256 hex prefix of 30-day JobDays snapshot (omitted for non-jobs). */
  jh?: string
}

export interface CompactState {
  ts: number
  m: Record<string, CompactMonitorState>
  /** Latest resource sample of the device's computers; stored as sent, read only by the account's devices. */
  dv?: unknown[]
}

export interface SyncPushRequest {
  device?: {
    name?: string
    avatar?: string
    os?: string
    version?: string
    machine?: string
  }
  metaHash?: string
  stateHash?: string
  meta?: CompactMeta
  state?: CompactState
}

export interface SyncDocItem {
  monitorId: string
  hash: string
  markdown: string
}

export interface SyncDocsRequest {
  docs: SyncDocItem[]
}

export interface SyncJobItem {
  monitorId: string
  hash: string
  payload: unknown
}

export interface SyncJobsRequest {
  jobs: SyncJobItem[]
}
