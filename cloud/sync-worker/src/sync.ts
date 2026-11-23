/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import type { CompactMeta, CompactState, Env, SyncDocsRequest, SyncJobsRequest, SyncPushRequest } from './types.ts'

const MAX_MONITORS_PER_DEVICE = 250
const MAX_BOARDS_PER_DEVICE = 40
const MAX_DOCS_PER_BATCH = 40
const MAX_DOC_BYTES = 64 * 1024
const MAX_JOBS_PER_BATCH = 20
const MAX_JOB_BYTES = 512 * 1024
const DEVICE_ONLINE_WINDOW_MS = 3 * 60 * 1000

export interface SessionContext {
  userId: string
  email: string
  deviceId: string
}

interface DeviceRow {
  user_id: string
  device_id: string
  name: string
  avatar: string
  os: string
  version: string
  meta_hash: string
  meta_json: string
  meta_seq: number
  state_hash: string
  state_json: string
  state_seq: number
  seq: number
  last_seen: number
  created_at: number
  updated_at: number
}

function clampString(value: unknown, maxLen: number): string {
  if (typeof value !== 'string') return ''
  const trimmed = value.trim()
  return trimmed.length > maxLen ? trimmed.slice(0, maxLen) : trimmed
}

function validateMeta(meta: CompactMeta): string | null {
  if (!Array.isArray(meta.boards) || !Array.isArray(meta.monitors)) {
    return 'meta 必须包含 boards 和 monitors 数组'
  }
  if (meta.boards.length > MAX_BOARDS_PER_DEVICE) {
    return `单台设备最多同步 ${MAX_BOARDS_PER_DEVICE} 个面板`
  }
  if (meta.monitors.length > MAX_MONITORS_PER_DEVICE) {
    return `单台设备最多同步 ${MAX_MONITORS_PER_DEVICE} 个服务`
  }
  return null
}

function validateState(state: CompactState): string | null {
  if (!state || typeof state !== 'object' || !state.m || typeof state.m !== 'object') {
    return 'state 格式无效'
  }
  if (Object.keys(state.m).length > MAX_MONITORS_PER_DEVICE) {
    return `状态帧超过单设备 ${MAX_MONITORS_PER_DEVICE} 个服务上限`
  }
  return null
}

/*
 * Clients pull with since=<the highest seq they saw>, so a user's seq must never go back: taking
 * MAX(devices.seq) did when the newest device was unbound, and two concurrent pushes could share one.
 * The single UPDATE ... RETURNING is atomic in D1.
 */
export async function nextUserSeq(db: D1Database, userId: string): Promise<number> {
  const row = await db
    .prepare(
      `UPDATE users
       SET sync_seq = MAX(sync_seq, (SELECT COALESCE(MAX(seq), 0) FROM devices WHERE user_id = ?1)) + 1
       WHERE id = ?1
       RETURNING sync_seq`,
    )
    .bind(userId)
    .first<{ sync_seq: number }>()
  if (!row) throw new Error('user not found while advancing sync seq')
  return row.sync_seq
}

/**
 * Layer 1 + Layer 2 ultra-lean push:
 * - Unchanged heartbeats send only { metaHash, stateHash } (~85B) and update last_seen.
 * - Meta / State blobs are overwritten in place (UPSERT) only when hash changes.
 * - Replies with needMeta (if server has no meta yet) and needDocs (exact monitor IDs whose doc hash changed).
 */
export async function handleSyncPush(
  env: Env,
  session: SessionContext,
  body: SyncPushRequest,
  now: number,
): Promise<{ status: number; payload: Record<string, unknown> }> {
  const existing = await env.DB.prepare('SELECT * FROM devices WHERE user_id = ? AND device_id = ?')
    .bind(session.userId, session.deviceId)
    .first<DeviceRow>()

  if (!existing) {
    return { status: 404, payload: { error: { code: 'device_unbound', message: '该设备已被解绑，请重新登录' } } }
  }

  let nextName = existing.name
  let nextAvatar = existing.avatar
  let nextOS = existing.os
  let nextVersion = existing.version
  let metaChanged = false
  let stateChanged = false

  if (body.device) {
    const n = clampString(body.device.name, 40)
    if (n && n !== existing.name) {
      nextName = n
      metaChanged = true
    }
    if (typeof body.device.avatar === 'string' && body.device.avatar.length <= 65536 && body.device.avatar !== existing.avatar) {
      nextAvatar = body.device.avatar
      metaChanged = true
    }
    const os = clampString(body.device.os, 32)
    if (os) nextOS = os
    const ver = clampString(body.device.version, 32)
    if (ver) nextVersion = ver
  }

  let nextMetaHash = existing.meta_hash
  let nextMetaJSON = existing.meta_json
  let nextStateHash = existing.state_hash
  let nextStateJSON = existing.state_json

  let parsedMeta: CompactMeta | null = null
  if (body.meta) {
    const err = validateMeta(body.meta)
    if (err) return { status: 400, payload: { error: { code: 'invalid_meta', message: err } } }
    parsedMeta = body.meta
    const serialized = JSON.stringify(body.meta)
    if (serialized.length > 256 * 1024) {
      return { status: 413, payload: { error: { code: 'payload_too_large', message: '元信息体积超过上限' } } }
    }
    const incomingHash = clampString(body.metaHash, 64)
    if (incomingHash !== existing.meta_hash || serialized !== existing.meta_json) {
      nextMetaHash = incomingHash
      nextMetaJSON = serialized
      metaChanged = true
    }
  } else if (existing.meta_json) {
    try {
      parsedMeta = JSON.parse(existing.meta_json) as CompactMeta
    } catch {
      parsedMeta = null
    }
  }

  let parsedState: CompactState | null = null
  if (body.state) {
    const err = validateState(body.state)
    if (err) return { status: 400, payload: { error: { code: 'invalid_state', message: err } } }
    parsedState = body.state
    const serialized = JSON.stringify(body.state)
    if (serialized.length > 256 * 1024) {
      return { status: 413, payload: { error: { code: 'payload_too_large', message: '状态帧体积超过上限' } } }
    }
    const incomingHash = clampString(body.stateHash, 64)
    if (incomingHash !== existing.state_hash || serialized !== existing.state_json) {
      nextStateHash = incomingHash
      nextStateJSON = serialized
      stateChanged = true
    }
  } else if (existing.state_json) {
    try {
      parsedState = JSON.parse(existing.state_json) as CompactState
    } catch {
      parsedState = null
    }
  }

  const reqMetaHash = clampString(body.metaHash, 64)
  const reqStateHash = clampString(body.stateHash, 64)
  const needMeta = !nextMetaJSON || (reqMetaHash !== '' && reqMetaHash !== nextMetaHash)
  const needState = !nextStateJSON || (reqStateHash !== '' && reqStateHash !== nextStateHash)

  let seq = existing.seq
  let metaSeq = existing.meta_seq
  let stateSeq = existing.state_seq

  if (metaChanged || stateChanged) {
    seq = await nextUserSeq(env.DB, session.userId)
    if (metaChanged) metaSeq = seq
    if (stateChanged) stateSeq = seq
    await env.DB.prepare(
      `UPDATE devices
       SET name = ?, avatar = ?, os = ?, version = ?,
           meta_hash = ?, meta_json = ?, meta_seq = ?,
           state_hash = ?, state_json = ?, state_seq = ?,
           seq = ?, last_seen = ?, updated_at = ?
       WHERE user_id = ? AND device_id = ?`,
    )
      .bind(
        nextName,
        nextAvatar,
        nextOS,
        nextVersion,
        nextMetaHash,
        nextMetaJSON,
        metaSeq,
        nextStateHash,
        nextStateJSON,
        stateSeq,
        seq,
        now,
        now,
        session.userId,
        session.deviceId,
      )
      .run()
  } else {
    // Heartbeat-only path: just bump last_seen so other devices know we're online.
    await env.DB.prepare('UPDATE devices SET last_seen = ?, os = ?, version = ? WHERE user_id = ? AND device_id = ?')
      .bind(now, nextOS, nextVersion, session.userId, session.deviceId)
      .run()
  }

  // Diff doc hashes against stored device_docs so only modified Markdown docs get uploaded.
  const needDocs: string[] = []
  if (parsedMeta) {
    const wanted = new Map<string, string>()
    for (const m of parsedMeta.monitors) {
      if (m.id && typeof m.dh === 'string' && m.dh.trim() !== '') {
        wanted.set(m.id, m.dh.trim().slice(0, 64))
      }
    }
    const existingDocs = await env.DB.prepare(
      'SELECT monitor_id, doc_hash FROM device_docs WHERE user_id = ? AND device_id = ?',
    )
      .bind(session.userId, session.deviceId)
      .all<{ monitor_id: string; doc_hash: string }>()

    const storedMap = new Map<string, string>()
    const orphanIds: string[] = []
    for (const row of existingDocs.results || []) {
      if (!wanted.has(row.monitor_id)) {
        orphanIds.push(row.monitor_id)
      } else {
        storedMap.set(row.monitor_id, row.doc_hash)
      }
    }
    // Prune docs whose monitor no longer exists or had its doc deleted.
    for (const orphan of orphanIds) {
      await env.DB.prepare('DELETE FROM device_docs WHERE user_id = ? AND device_id = ? AND monitor_id = ?')
        .bind(session.userId, session.deviceId, orphan)
        .run()
    }
    for (const [monitorId, hash] of wanted.entries()) {
      if (storedMap.get(monitorId) !== hash) {
        needDocs.push(monitorId)
      }
    }
  }

  // Diff batch job hashes against stored device_jobs so only updated job daily records get uploaded.
  const needJobs: string[] = []
  if (parsedState && parsedState.m && typeof parsedState.m === 'object') {
    const wantedJobs = new Map<string, string>()
    for (const [monitorId, st] of Object.entries(parsedState.m)) {
      if (monitorId && st && typeof st.jh === 'string' && st.jh.trim() !== '') {
        wantedJobs.set(monitorId, st.jh.trim().slice(0, 64))
      }
    }
    const existingJobs = await env.DB.prepare(
      'SELECT monitor_id, job_hash FROM device_jobs WHERE user_id = ? AND device_id = ?',
    )
      .bind(session.userId, session.deviceId)
      .all<{ monitor_id: string; job_hash: string }>()

    const storedJobMap = new Map<string, string>()
    const orphanJobs: string[] = []
    for (const row of existingJobs.results || []) {
      if (!wantedJobs.has(row.monitor_id)) {
        orphanJobs.push(row.monitor_id)
      } else {
        storedJobMap.set(row.monitor_id, row.job_hash)
      }
    }
    for (const orphan of orphanJobs) {
      await env.DB.prepare('DELETE FROM device_jobs WHERE user_id = ? AND device_id = ? AND monitor_id = ?')
        .bind(session.userId, session.deviceId, orphan)
        .run()
    }
    for (const [monitorId, hash] of wantedJobs.entries()) {
      if (storedJobMap.get(monitorId) !== hash) {
        needJobs.push(monitorId)
      }
    }
  }

  return {
    status: 200,
    payload: {
      ok: true,
      seq,
      needMeta,
      needState,
      needDocs,
      needJobs,
      serverTime: now,
    },
  }
}

/** Layer 3 incremental Markdown doc upload (invoked only for IDs requested in needDocs). */
export async function handleSyncDocsPush(
  env: Env,
  session: SessionContext,
  body: SyncDocsRequest,
  now: number,
): Promise<{ status: number; payload: Record<string, unknown> }> {
  if (!body || !Array.isArray(body.docs)) {
    return { status: 400, payload: { error: { code: 'invalid_request', message: 'docs 必须是数组' } } }
  }
  if (body.docs.length > MAX_DOCS_PER_BATCH) {
    return { status: 400, payload: { error: { code: 'too_many_docs', message: `单次最多上传 ${MAX_DOCS_PER_BATCH} 篇文档` } } }
  }
  let saved = 0
  for (const item of body.docs) {
    const monitorId = clampString(item.monitorId, 96)
    const hash = clampString(item.hash, 64)
    if (!monitorId || !hash || typeof item.markdown !== 'string') continue
    if (item.markdown.length > MAX_DOC_BYTES) {
      return { status: 413, payload: { error: { code: 'doc_too_large', message: `文档 ${monitorId} 超过 64 KB 上限` } } }
    }
    await env.DB.prepare(
      `INSERT INTO device_docs (user_id, device_id, monitor_id, doc_hash, markdown, updated_at)
       VALUES (?, ?, ?, ?, ?, ?)
       ON CONFLICT(user_id, device_id, monitor_id)
       DO UPDATE SET doc_hash = excluded.doc_hash, markdown = excluded.markdown, updated_at = excluded.updated_at`,
    )
      .bind(session.userId, session.deviceId, monitorId, hash, item.markdown, now)
      .run()
    saved++
  }
  return { status: 200, payload: { ok: true, saved } }
}

/** Layer 3 incremental batch job daily records upload (invoked only for IDs requested in needJobs). */
export async function handleSyncJobsPush(
  env: Env,
  session: SessionContext,
  body: SyncJobsRequest,
  now: number,
): Promise<{ status: number; payload: Record<string, unknown> }> {
  if (!body || !Array.isArray(body.jobs)) {
    return { status: 400, payload: { error: { code: 'invalid_request', message: 'jobs 必须是数组' } } }
  }
  if (body.jobs.length > MAX_JOBS_PER_BATCH) {
    return { status: 400, payload: { error: { code: 'too_many_jobs', message: `单次最多上传 ${MAX_JOBS_PER_BATCH} 个批处理任务` } } }
  }
  let saved = 0
  for (const item of body.jobs) {
    const monitorId = clampString(item.monitorId, 96)
    const hash = clampString(item.hash, 64)
    if (!monitorId || !hash || item.payload == null) continue
    const serialized = typeof item.payload === 'string' ? item.payload : JSON.stringify(item.payload)
    if (serialized.length > MAX_JOB_BYTES) {
      return {
        status: 413,
        payload: { error: { code: 'job_too_large', message: `任务 ${monitorId} 每日记录超过 512 KB 上限` } },
      }
    }
    await env.DB.prepare(
      `INSERT INTO device_jobs (user_id, device_id, monitor_id, job_hash, payload_json, updated_at)
       VALUES (?, ?, ?, ?, ?, ?)
       ON CONFLICT(user_id, device_id, monitor_id)
       DO UPDATE SET job_hash = excluded.job_hash, payload_json = excluded.payload_json, updated_at = excluded.updated_at`,
    )
      .bind(session.userId, session.deviceId, monitorId, hash, serialized, now)
      .run()
    saved++
  }
  return { status: 200, payload: { ok: true, saved } }
}

/** Multi-device delta snapshot pull: sends meta/state ONLY for devices with seq > since. */
export async function handleSyncSnapshot(
  env: Env,
  session: SessionContext,
  since: number,
  ifNoneMatch: string | null,
  now: number,
): Promise<{ status: number; etag?: string; payload?: Record<string, unknown> }> {
  const rows = await env.DB.prepare(
    'SELECT * FROM devices WHERE user_id = ? ORDER BY created_at ASC, device_id ASC',
  )
    .bind(session.userId)
    .all<DeviceRow>()

  const devices = rows.results || []
  let maxSeq = 0
  const sigParts: string[] = []
  for (const row of devices) {
    if (row.seq > maxSeq) maxSeq = row.seq
    const online = now - row.last_seen <= DEVICE_ONLINE_WINDOW_MS ? 1 : 0
    sigParts.push(`${row.device_id}:${row.seq}:${online}`)
  }
  const etag = `"sync-${maxSeq}-${sigParts.join(',')}"`
  if (ifNoneMatch && ifNoneMatch === etag && since >= maxSeq) {
    return { status: 304, etag }
  }

  const items = devices.map((row) => {
    const includeMeta = row.meta_seq > since || since === 0
    const includeState = row.state_seq > since || since === 0
    let meta: CompactMeta | undefined
    let state: CompactState | undefined
    if (includeMeta && row.meta_json) {
      try {
        meta = JSON.parse(row.meta_json) as CompactMeta
      } catch {
        meta = undefined
      }
    }
    if (includeState && row.state_json) {
      try {
        state = JSON.parse(row.state_json) as CompactState
      } catch {
        state = undefined
      }
    }
    return {
      id: row.device_id,
      name: row.name,
      // An uploaded avatar can be a 64 KB data URL; it only changes together with meta_seq.
      avatar: includeMeta ? row.avatar : undefined,
      os: row.os,
      version: row.version,
      isCurrent: row.device_id === session.deviceId,
      online: now - row.last_seen <= DEVICE_ONLINE_WINDOW_MS,
      lastSeen: row.last_seen,
      seq: row.seq,
      metaHash: row.meta_hash,
      stateHash: row.state_hash,
      meta,
      state,
    }
  })

  return {
    status: 200,
    etag,
    payload: {
      seq: maxSeq,
      serverTime: now,
      devices: items,
    },
  }
}

/** Layer 3 on-demand Markdown doc pull with ETag / 304 caching. */
export async function handleGetDoc(
  env: Env,
  session: SessionContext,
  deviceId: string,
  monitorId: string,
  ifNoneMatch: string | null,
): Promise<{ status: number; etag?: string; payload?: Record<string, unknown> }> {
  const row = await env.DB.prepare(
    'SELECT doc_hash, markdown, updated_at FROM device_docs WHERE user_id = ? AND device_id = ? AND monitor_id = ?',
  )
    .bind(session.userId, deviceId, monitorId)
    .first<{ doc_hash: string; markdown: string; updated_at: number }>()

  if (!row) {
    return { status: 404, payload: { error: { code: 'not_found', message: '该服务暂未同步文档' } } }
  }
  const etag = `"${row.doc_hash}"`
  if (ifNoneMatch && ifNoneMatch === etag) {
    return { status: 304, etag }
  }
  return {
    status: 200,
    etag,
    payload: {
      deviceId,
      monitorId,
      hash: row.doc_hash,
      markdown: row.markdown,
      updatedAt: row.updated_at,
    },
  }
}

/** Layer 3 on-demand batch job daily records pull with ETag / 304 caching. */
export async function handleGetJob(
  env: Env,
  session: SessionContext,
  deviceId: string,
  monitorId: string,
  ifNoneMatch: string | null,
): Promise<{ status: number; etag?: string; payload?: Record<string, unknown> }> {
  const row = await env.DB.prepare(
    'SELECT job_hash, payload_json, updated_at FROM device_jobs WHERE user_id = ? AND device_id = ? AND monitor_id = ?',
  )
    .bind(session.userId, deviceId, monitorId)
    .first<{ job_hash: string; payload_json: string; updated_at: number }>()

  if (!row) {
    return { status: 404, payload: { error: { code: 'not_found', message: '该批处理任务暂未同步每日记录' } } }
  }
  const etag = `"${row.job_hash}"`
  if (ifNoneMatch && ifNoneMatch === etag) {
    return { status: 304, etag }
  }
  let payload: unknown = null
  try {
    payload = JSON.parse(row.payload_json)
  } catch {
    return { status: 500, payload: { error: { code: 'corrupt_job_payload', message: '云端任务记录格式损坏' } } }
  }
  return {
    status: 200,
    etag,
    payload: {
      deviceId,
      monitorId,
      hash: row.job_hash,
      payload,
      updatedAt: row.updated_at,
    },
  }
}
