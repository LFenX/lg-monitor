/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import {
  constantTimeEqual,
  hashPassword,
  isValidEmail,
  normalizeEmail,
  randomHex,
  randomOTP,
  randomToken,
  sha256Hex,
  verifyPassword,
} from './crypto.ts'
import { createMailSender } from './mail.ts'
import type { MailSender } from './mail.ts'
import { handleGetDoc, handleSyncDocsPush, handleSyncPush, handleSyncSnapshot, nextUserSeq } from './sync.ts'
import type { SessionContext } from './sync.ts'
import type { DeviceInfoInput, Env, SyncDocsRequest, SyncPushRequest } from './types.ts'

const MAX_BODY_BYTES = 320 * 1024
const OTP_TTL_MS = 10 * 60 * 1000
const OTP_COOLDOWN_MS = 60 * 1000
const OTP_MAX_ATTEMPTS = 5
const SESSION_TTL_MS = 90 * 24 * 60 * 60 * 1000
const MAX_DEVICES_PER_USER = 20

type OTPPurpose = 'login' | 'register' | 'reset'

interface UserRow {
  id: string
  email: string
  password_hash: string | null
  password_salt: string | null
  created_at: number
  updated_at: number
}

interface OTPRow {
  id: string
  email: string
  purpose: string
  code_hash: string
  salt: string
  attempts: number
  expires_at: number
  used_at: number | null
  created_at: number
}

export interface WorkerDeps {
  mail?: MailSender
  now?: () => number
}

function jsonResponse(payload: unknown, status = 200, extraHeaders?: Record<string, string>): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      'Cache-Control': 'no-store',
      ...extraHeaders,
    },
  })
}

function errorResponse(status: number, code: string, message: string): Response {
  return jsonResponse({ error: { code, message } }, status)
}

// Stops reading once the limit is passed, so a small gzip bomb cannot inflate past it in memory.
async function readCapped(stream: ReadableStream<Uint8Array>, limit: number): Promise<Uint8Array | null> {
  const reader = stream.getReader()
  const chunks: Uint8Array[] = []
  let total = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    total += value.byteLength
    if (total > limit) {
      await reader.cancel().catch(() => undefined)
      return null
    }
    chunks.push(value)
  }
  const out = new Uint8Array(total)
  let offset = 0
  for (const chunk of chunks) {
    out.set(chunk, offset)
    offset += chunk.byteLength
  }
  return out
}

async function readJSON<T>(request: Request): Promise<{ ok: true; data: T } | { ok: false; response: Response }> {
  try {
    let raw: Uint8Array | null = new Uint8Array(0)
    if (request.body) {
      const encoding = (request.headers.get('Content-Encoding') || '').toLowerCase()
      const source = encoding === 'gzip' ? request.body.pipeThrough(new DecompressionStream('gzip')) : request.body
      raw = await readCapped(source, MAX_BODY_BYTES)
    }
    if (!raw) {
      return { ok: false, response: errorResponse(413, 'payload_too_large', '请求体超过体积上限') }
    }
    if (raw.byteLength === 0) {
      return { ok: true, data: {} as T }
    }
    const text = new TextDecoder().decode(raw)
    return { ok: true, data: JSON.parse(text) as T }
  } catch {
    return { ok: false, response: errorResponse(400, 'invalid_json', '请求体必须是有效的 JSON') }
  }
}

async function checkRateLimit(
  db: D1Database,
  bucket: string,
  limit: number,
  windowMs: number,
  now: number,
): Promise<boolean> {
  const row = await db
    .prepare('SELECT count, window_end FROM rate_limits WHERE bucket = ?')
    .bind(bucket)
    .first<{ count: number; window_end: number }>()
  if (!row || row.window_end <= now) {
    await db
      .prepare(
        `INSERT INTO rate_limits (bucket, count, window_end) VALUES (?, 1, ?)
         ON CONFLICT(bucket) DO UPDATE SET count = 1, window_end = excluded.window_end`,
      )
      .bind(bucket, now + windowMs)
      .run()
    return true
  }
  if (row.count >= limit) return false
  await db.prepare('UPDATE rate_limits SET count = count + 1 WHERE bucket = ?').bind(bucket).run()
  return true
}

function cleanDevice(input?: DeviceInfoInput): { id: string; name: string; avatar: string; os: string; version: string } | null {
  if (!input || typeof input.id !== 'string') return null
  const id = input.id.trim().slice(0, 80)
  if (!id || !/^[A-Za-z0-9._:-]+$/.test(id)) return null
  const name = (typeof input.name === 'string' ? input.name.trim() : '').slice(0, 40) || '我的设备'
  const avatar = typeof input.avatar === 'string' && input.avatar.length <= 65536 ? input.avatar : ''
  const os = (typeof input.os === 'string' ? input.os.trim() : '').slice(0, 32)
  const version = (typeof input.version === 'string' ? input.version.trim() : '').slice(0, 32)
  return { id, name, avatar, os, version }
}

async function upsertDeviceAndSession(
  db: D1Database,
  user: UserRow,
  device: { id: string; name: string; avatar: string; os: string; version: string },
  now: number,
): Promise<{ token: string } | { error: Response }> {
  const existing = await db
    .prepare('SELECT device_id FROM devices WHERE user_id = ? AND device_id = ?')
    .bind(user.id, device.id)
    .first<{ device_id: string }>()

  if (!existing) {
    const countRow = await db
      .prepare('SELECT COUNT(*) AS cnt FROM devices WHERE user_id = ?')
      .bind(user.id)
      .first<{ cnt: number }>()
    if ((countRow?.cnt ?? 0) >= MAX_DEVICES_PER_USER) {
      return {
        error: errorResponse(409, 'device_limit', `单个账号最多绑定 ${MAX_DEVICES_PER_USER} 台设备，请先解绑旧设备`),
      }
    }
    await db
      .prepare(
        `INSERT INTO devices (user_id, device_id, name, avatar, os, version, last_seen, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .bind(user.id, device.id, device.name, device.avatar, device.os, device.version, now, now, now)
      .run()
  } else {
    await db
      .prepare(
        'UPDATE devices SET name = ?, avatar = ?, os = ?, version = ?, last_seen = ?, updated_at = ? WHERE user_id = ? AND device_id = ?',
      )
      .bind(device.name, device.avatar, device.os, device.version, now, now, user.id, device.id)
      .run()
  }

  // A device holds one session: signing in again replaces it, and expired ones go with it.
  await db
    .prepare('DELETE FROM sessions WHERE user_id = ? AND (device_id = ? OR expires_at <= ?)')
    .bind(user.id, device.id, now)
    .run()
  const token = randomToken()
  const tokenHash = await sha256Hex(token)
  await db
    .prepare(
      'INSERT INTO sessions (token_hash, user_id, device_id, created_at, last_seen, expires_at) VALUES (?, ?, ?, ?, ?, ?)',
    )
    .bind(tokenHash, user.id, device.id, now, now, now + SESSION_TTL_MS)
    .run()

  return { token }
}

async function authenticate(request: Request, db: D1Database, now: number): Promise<SessionContext | null> {
  const auth = request.headers.get('Authorization') || ''
  if (!auth.startsWith('Bearer ')) return null
  const token = auth.slice(7).trim()
  if (!token) return null
  const tokenHash = await sha256Hex(token)
  const row = await db
    .prepare(
      `SELECT s.user_id, s.device_id, s.expires_at, u.email
       FROM sessions s
       JOIN users u ON u.id = s.user_id
       WHERE s.token_hash = ?`,
    )
    .bind(tokenHash)
    .first<{ user_id: string; device_id: string; expires_at: number; email: string }>()
  if (!row || row.expires_at <= now) return null
  return { userId: row.user_id, email: row.email, deviceId: row.device_id }
}

async function verifyOTPCode(
  db: D1Database,
  email: string,
  code: string,
  purpose: OTPPurpose,
  now: number,
): Promise<{ ok: true } | { ok: false; response: Response }> {
  const cleaned = code.trim()
  if (!/^\d{6}$/.test(cleaned)) {
    return { ok: false, response: errorResponse(400, 'invalid_code', '请输入 6 位数字验证码') }
  }
  const latest = await db
    .prepare(
      `SELECT * FROM otp_codes
       WHERE email = ? AND purpose = ? AND used_at IS NULL
       ORDER BY created_at DESC LIMIT 1`,
    )
    .bind(email, purpose)
    .first<OTPRow>()

  if (!latest || latest.expires_at <= now) {
    return { ok: false, response: errorResponse(400, 'otp_expired', '验证码已过期或不存在，请重新获取') }
  }
  if (latest.attempts >= OTP_MAX_ATTEMPTS) {
    return { ok: false, response: errorResponse(429, 'otp_locked', '验证码输错次数过多，请重新获取') }
  }
  const expected = await sha256Hex(latest.salt + ':' + cleaned)
  if (!constantTimeEqual(expected, latest.code_hash)) {
    await db.prepare('UPDATE otp_codes SET attempts = attempts + 1 WHERE id = ?').bind(latest.id).run()
    return { ok: false, response: errorResponse(401, 'otp_mismatch', '验证码不正确') }
  }
  await db.prepare('UPDATE otp_codes SET used_at = ? WHERE id = ?').bind(now, latest.id).run()
  return { ok: true }
}

export async function handleRequest(request: Request, env: Env, deps: WorkerDeps = {}): Promise<Response> {
  const now = (deps.now ?? Date.now)()
  const mail = deps.mail ?? createMailSender(env)
  const url = new URL(request.url)
  const path = url.pathname
  const method = request.method

  if (path === '/health' && method === 'GET') {
    return jsonResponse({ ok: true, service: 'lgmon-sync', time: now })
  }

  // ---- 1. Request an email code: register (new address), login or reset (existing account) ----
  if (path === '/v1/auth/otp/request' && method === 'POST') {
    const parsed = await readJSON<{
      email?: string
      purpose?: string
      lang?: string
      deviceName?: string
      os?: string
    }>(request)
    if (!parsed.ok) return parsed.response
    const email = normalizeEmail(parsed.data.email)
    if (!isValidEmail(email)) {
      return errorResponse(400, 'invalid_email', '请输入有效的邮箱地址')
    }
    const purpose: OTPPurpose =
      parsed.data.purpose === 'reset' ? 'reset' : parsed.data.purpose === 'register' ? 'register' : 'login'
    const lang = parsed.data.lang === 'en' ? 'en' : 'zh'

    // Registering and signing in are separate steps, so each answers right away when the address is on the other side.
    const known = await env.DB.prepare('SELECT id FROM users WHERE email = ?').bind(email).first<{ id: string }>()
    if (purpose === 'register' && known) {
      return errorResponse(409, 'account_exists', '这个邮箱已经注册过了，请直接登录')
    }
    if (purpose !== 'register' && !known) {
      return errorResponse(404, 'account_not_found', '这个邮箱还没有注册，请先注册')
    }

    // Per-email 60s cooldown + hourly cap (6/hour)
    const recent = await env.DB.prepare(
      'SELECT created_at FROM otp_codes WHERE email = ? ORDER BY created_at DESC LIMIT 1',
    )
      .bind(email)
      .first<{ created_at: number }>()
    if (recent && now - recent.created_at < OTP_COOLDOWN_MS) {
      const waitSec = Math.ceil((OTP_COOLDOWN_MS - (now - recent.created_at)) / 1000)
      return errorResponse(429, 'otp_cooldown', `发送太频繁，请 ${waitSec} 秒后再试`)
    }
    const allowed = await checkRateLimit(env.DB, `otp:email:${email}`, 6, 60 * 60 * 1000, now)
    if (!allowed) {
      return errorResponse(429, 'rate_limited', '该邮箱 1 小时内获取验证码次数过多，请稍后再试')
    }

    const code = randomOTP()
    const salt = randomHex(12)
    const codeHash = await sha256Hex(salt + ':' + code)
    const otpId = 'otp_' + randomHex(12)

    // Stored before sending, so a code that reached the inbox can always be verified.
    await env.DB.prepare(
      `INSERT INTO otp_codes (id, email, purpose, code_hash, salt, attempts, expires_at, created_at)
       VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
    )
      .bind(otpId, email, purpose, codeHash, salt, now + OTP_TTL_MS, now)
      .run()
    await env.DB.prepare('DELETE FROM otp_codes WHERE email = ? AND expires_at <= ?').bind(email, now).run()

    try {
      await mail.sendOTP({
        email,
        code,
        purpose,
        lang,
        deviceName: typeof parsed.data.deviceName === 'string' ? parsed.data.deviceName.trim().slice(0, 40) : '',
        os: typeof parsed.data.os === 'string' ? parsed.data.os.trim().slice(0, 16) : '',
        ttlMinutes: Math.round(OTP_TTL_MS / 60000),
      })
    } catch (err: unknown) {
      await env.DB.prepare('DELETE FROM otp_codes WHERE id = ?').bind(otpId).run()
      const message = err instanceof Error ? err.message : '发送验证码失败'
      return errorResponse(502, 'mail_failed', message)
    }

    return jsonResponse({ ok: true, cooldownSec: Math.round(OTP_COOLDOWN_MS / 1000), expiresInSec: Math.round(OTP_TTL_MS / 1000) })
  }

  // ---- 2a. Register: a register code creates the account, optionally with a password, and binds this device ----
  if (path === '/v1/auth/register' && method === 'POST') {
    const parsed = await readJSON<{ email?: string; code?: string; password?: string; device?: DeviceInfoInput }>(
      request,
    )
    if (!parsed.ok) return parsed.response
    const email = normalizeEmail(parsed.data.email)
    if (!isValidEmail(email)) return errorResponse(400, 'invalid_email', '请输入有效的邮箱地址')
    const device = cleanDevice(parsed.data.device)
    if (!device) return errorResponse(400, 'invalid_device', '缺少有效的设备标识')
    const password = typeof parsed.data.password === 'string' ? parsed.data.password : ''
    if (password && (password.length < 8 || password.length > 128)) {
      return errorResponse(400, 'weak_password', '密码长度需在 8 到 128 个字符之间')
    }

    const verified = await verifyOTPCode(env.DB, email, parsed.data.code || '', 'register', now)
    if (!verified.ok) return verified.response

    if (await env.DB.prepare('SELECT id FROM users WHERE email = ?').bind(email).first<{ id: string }>()) {
      return errorResponse(409, 'account_exists', '这个邮箱已经注册过了，请直接登录')
    }
    const id = 'usr_' + randomHex(12)
    const secret = password ? await hashPassword(password) : null
    await env.DB.prepare(
      'INSERT INTO users (id, email, password_hash, password_salt, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)',
    )
      .bind(id, email, secret?.hash ?? null, secret?.salt ?? null, now, now)
      .run()
    const user: UserRow = {
      id,
      email,
      password_hash: secret?.hash ?? null,
      password_salt: secret?.salt ?? null,
      created_at: now,
      updated_at: now,
    }

    const issued = await upsertDeviceAndSession(env.DB, user, device, now)
    if ('error' in issued) return issued.error

    return jsonResponse({
      token: issued.token,
      user: { id: user.id, email: user.email, hasPassword: !!secret, createdAt: user.created_at },
      device,
      registered: true,
    })
  }

  // ---- 2b. Sign in with a login code (registered accounts only) ----
  if (path === '/v1/auth/otp/verify' && method === 'POST') {
    const parsed = await readJSON<{ email?: string; code?: string; device?: DeviceInfoInput }>(request)
    if (!parsed.ok) return parsed.response
    const email = normalizeEmail(parsed.data.email)
    if (!isValidEmail(email)) return errorResponse(400, 'invalid_email', '请输入有效的邮箱地址')
    const device = cleanDevice(parsed.data.device)
    if (!device) return errorResponse(400, 'invalid_device', '缺少有效的设备标识')

    const verified = await verifyOTPCode(env.DB, email, parsed.data.code || '', 'login', now)
    if (!verified.ok) return verified.response

    const user = await env.DB.prepare('SELECT * FROM users WHERE email = ?').bind(email).first<UserRow>()
    if (!user) {
      return errorResponse(404, 'account_not_found', '这个邮箱还没有注册，请先注册')
    }

    const issued = await upsertDeviceAndSession(env.DB, user, device, now)
    if ('error' in issued) return issued.error

    return jsonResponse({
      token: issued.token,
      user: { id: user.id, email: user.email, hasPassword: !!user.password_hash, createdAt: user.created_at },
      device,
    })
  }

  // ---- 3. Password Login ----
  if (path === '/v1/auth/login' && method === 'POST') {
    const parsed = await readJSON<{ email?: string; password?: string; device?: DeviceInfoInput }>(request)
    if (!parsed.ok) return parsed.response
    const email = normalizeEmail(parsed.data.email)
    const password = typeof parsed.data.password === 'string' ? parsed.data.password : ''
    if (!isValidEmail(email) || !password) {
      return errorResponse(400, 'invalid_credentials', '请输入邮箱和密码')
    }
    const device = cleanDevice(parsed.data.device)
    if (!device) return errorResponse(400, 'invalid_device', '缺少有效的设备标识')

    const allowed = await checkRateLimit(env.DB, `pwd:${email}`, 10, 15 * 60 * 1000, now)
    if (!allowed) {
      return errorResponse(429, 'rate_limited', '登录尝试过于频繁，请 15 分钟后再试或改用邮箱验证码登录')
    }

    const user = await env.DB.prepare('SELECT * FROM users WHERE email = ?').bind(email).first<UserRow>()
    if (!user) {
      return errorResponse(401, 'invalid_credentials', '邮箱或密码不正确')
    }
    if (!user.password_hash || !user.password_salt) {
      return errorResponse(400, 'password_not_set', '该账号尚未设置密码，请先使用邮箱验证码登录')
    }
    const ok = await verifyPassword(password, user.password_salt, user.password_hash)
    if (!ok) {
      return errorResponse(401, 'invalid_credentials', '邮箱或密码不正确')
    }

    const issued = await upsertDeviceAndSession(env.DB, user, device, now)
    if ('error' in issued) return issued.error

    return jsonResponse({
      token: issued.token,
      user: { id: user.id, email: user.email, hasPassword: true, createdAt: user.created_at },
      device,
    })
  }

  // ---- 4. Reset Password via OTP ----
  if (path === '/v1/auth/password/reset' && method === 'POST') {
    const parsed = await readJSON<{ email?: string; code?: string; newPassword?: string }>(request)
    if (!parsed.ok) return parsed.response
    const email = normalizeEmail(parsed.data.email)
    const newPassword = typeof parsed.data.newPassword === 'string' ? parsed.data.newPassword : ''
    if (!isValidEmail(email)) return errorResponse(400, 'invalid_email', '请输入有效的邮箱地址')
    if (newPassword.length < 8 || newPassword.length > 128) {
      return errorResponse(400, 'weak_password', '密码长度需在 8 到 128 个字符之间')
    }

    const verified = await verifyOTPCode(env.DB, email, parsed.data.code || '', 'reset', now)
    if (!verified.ok) return verified.response

    const user = await env.DB.prepare('SELECT * FROM users WHERE email = ?').bind(email).first<UserRow>()
    if (!user) return errorResponse(404, 'account_not_found', '该邮箱尚未注册')

    const { hash, salt } = await hashPassword(newPassword)
    await env.DB.prepare('UPDATE users SET password_hash = ?, password_salt = ?, updated_at = ? WHERE id = ?')
      .bind(hash, salt, now, user.id)
      .run()

    return jsonResponse({ ok: true })
  }

  // ---- Authenticated routes below ----
  const session = await authenticate(request, env.DB, now)
  if (!session) {
    return errorResponse(401, 'unauthorized', '登录状态已失效，请重新登录')
  }

  // ---- 5. Current user info ----
  if (path === '/v1/auth/me' && method === 'GET') {
    const user = await env.DB.prepare('SELECT * FROM users WHERE id = ?').bind(session.userId).first<UserRow>()
    if (!user) return errorResponse(401, 'unauthorized', '账号不存在')
    const cnt = await env.DB.prepare('SELECT COUNT(*) AS cnt FROM devices WHERE user_id = ?')
      .bind(session.userId)
      .first<{ cnt: number }>()
    return jsonResponse({
      user: { id: user.id, email: user.email, hasPassword: !!user.password_hash, createdAt: user.created_at },
      currentDeviceId: session.deviceId,
      devicesCount: cnt?.cnt ?? 0,
    })
  }

  // ---- 6. Set or Change Password ----
  if (path === '/v1/auth/password' && method === 'POST') {
    const parsed = await readJSON<{ currentPassword?: string; newPassword?: string }>(request)
    if (!parsed.ok) return parsed.response
    const newPassword = typeof parsed.data.newPassword === 'string' ? parsed.data.newPassword : ''
    if (newPassword.length < 8 || newPassword.length > 128) {
      return errorResponse(400, 'weak_password', '密码长度需在 8 到 128 个字符之间')
    }
    const user = await env.DB.prepare('SELECT * FROM users WHERE id = ?').bind(session.userId).first<UserRow>()
    if (!user) return errorResponse(401, 'unauthorized', '账号不存在')
    if (user.password_hash && user.password_salt) {
      const current = typeof parsed.data.currentPassword === 'string' ? parsed.data.currentPassword : ''
      if (!current) return errorResponse(400, 'current_password_required', '请填写当前密码')
      const valid = await verifyPassword(current, user.password_salt, user.password_hash)
      if (!valid) return errorResponse(401, 'invalid_credentials', '当前密码不正确')
    }
    const { hash, salt } = await hashPassword(newPassword)
    await env.DB.prepare('UPDATE users SET password_hash = ?, password_salt = ?, updated_at = ? WHERE id = ?')
      .bind(hash, salt, now, user.id)
      .run()
    return jsonResponse({ ok: true, hasPassword: true })
  }

  // ---- 7. Logout ----
  if (path === '/v1/auth/logout' && method === 'POST') {
    const parsed = await readJSON<{ unbindDevice?: boolean }>(request)
    const unbind = parsed.ok && !!parsed.data.unbindDevice
    const auth = (request.headers.get('Authorization') || '').slice(7).trim()
    const tokenHash = await sha256Hex(auth)
    await env.DB.prepare('DELETE FROM sessions WHERE token_hash = ?').bind(tokenHash).run()
    if (unbind) {
      await env.DB.prepare('DELETE FROM device_docs WHERE user_id = ? AND device_id = ?')
        .bind(session.userId, session.deviceId)
        .run()
      await env.DB.prepare('DELETE FROM devices WHERE user_id = ? AND device_id = ?')
        .bind(session.userId, session.deviceId)
        .run()
      await env.DB.prepare('DELETE FROM sessions WHERE user_id = ? AND device_id = ?')
        .bind(session.userId, session.deviceId)
        .run()
    }
    return jsonResponse({ ok: true })
  }

  // ---- 8. Sync Push (Layer 1 + Layer 2 + doc hash diff) ----
  if (path === '/v1/sync/push' && method === 'POST') {
    const parsed = await readJSON<SyncPushRequest>(request)
    if (!parsed.ok) return parsed.response
    const result = await handleSyncPush(env, session, parsed.data, now)
    return jsonResponse(result.payload, result.status)
  }

  // ---- 9. Sync Docs Push (Layer 3 incremental Markdown docs) ----
  if (path === '/v1/sync/docs' && method === 'PUT') {
    const parsed = await readJSON<SyncDocsRequest>(request)
    if (!parsed.ok) return parsed.response
    const result = await handleSyncDocsPush(env, session, parsed.data, now)
    return jsonResponse(result.payload, result.status)
  }

  // ---- 10. Multi-device Delta Snapshot Pull ----
  if (path === '/v1/sync/snapshot' && method === 'GET') {
    const sinceParam = Number.parseInt(url.searchParams.get('since') || '0', 10)
    const since = Number.isFinite(sinceParam) && sinceParam > 0 ? sinceParam : 0
    const res = await handleSyncSnapshot(env, session, since, request.headers.get('If-None-Match'), now)
    if (res.status === 304) {
      return new Response(null, { status: 304, headers: res.etag ? { ETag: res.etag } : undefined })
    }
    return jsonResponse(res.payload, res.status, res.etag ? { ETag: res.etag } : undefined)
  }

  // ---- 11. On-demand Remote Service Doc Fetch ----
  const docMatch = /^\/v1\/sync\/devices\/([^/]+)\/docs\/([^/]+)$/.exec(path)
  if (docMatch && method === 'GET') {
    const deviceId = decodeURIComponent(docMatch[1])
    const monitorId = decodeURIComponent(docMatch[2])
    const res = await handleGetDoc(env, session, deviceId, monitorId, request.headers.get('If-None-Match'))
    if (res.status === 304) {
      return new Response(null, { status: 304, headers: res.etag ? { ETag: res.etag } : undefined })
    }
    return jsonResponse(res.payload, res.status, res.etag ? { ETag: res.etag } : undefined)
  }

  // ---- 12. Device Rename / Unbind ----
  const deviceMatch = /^\/v1\/devices\/([^/]+)$/.exec(path)
  if (deviceMatch) {
    const targetId = decodeURIComponent(deviceMatch[1])
    if (method === 'PATCH') {
      const parsed = await readJSON<{ name?: string; avatar?: string }>(request)
      if (!parsed.ok) return parsed.response
      const name = typeof parsed.data.name === 'string' ? parsed.data.name.trim().slice(0, 40) : ''
      if (!name) return errorResponse(400, 'invalid_name', '设备名称不能为空')
      const found = await env.DB.prepare('SELECT device_id FROM devices WHERE user_id = ? AND device_id = ?')
        .bind(session.userId, targetId)
        .first<{ device_id: string }>()
      if (!found) return errorResponse(404, 'device_not_found', '账号下没有这台设备')
      const nextSeq = await nextUserSeq(env.DB, session.userId)
      await env.DB.prepare(
        'UPDATE devices SET name = ?, seq = ?, meta_seq = ?, updated_at = ? WHERE user_id = ? AND device_id = ?',
      )
        .bind(name, nextSeq, nextSeq, now, session.userId, targetId)
        .run()
      return jsonResponse({ ok: true, id: targetId, name })
    }
    if (method === 'DELETE') {
      await env.DB.prepare('DELETE FROM device_docs WHERE user_id = ? AND device_id = ?')
        .bind(session.userId, targetId)
        .run()
      await env.DB.prepare('DELETE FROM sessions WHERE user_id = ? AND device_id = ?')
        .bind(session.userId, targetId)
        .run()
      await env.DB.prepare('DELETE FROM devices WHERE user_id = ? AND device_id = ?')
        .bind(session.userId, targetId)
        .run()
      return jsonResponse({ ok: true, id: targetId })
    }
  }

  return errorResponse(404, 'not_found', '接口不存在')
}

export default {
  fetch(request: Request, env: Env): Promise<Response> {
    return handleRequest(request, env)
  },
}
