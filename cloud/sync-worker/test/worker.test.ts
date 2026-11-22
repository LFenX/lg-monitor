/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

// Runs the real worker against a node:sqlite stand-in for D1: node --test test/worker.test.ts
import assert from 'node:assert/strict'
import { gzipSync } from 'node:zlib'
import { beforeEach, test } from 'node:test'
import { openLocalD1 } from '../dev/d1.ts'
import type { LocalD1 } from '../dev/d1.ts'
import { handleRequest } from '../src/index.ts'

type Sent = { email: string; code: string; purpose: string }

let db: LocalD1
let clock: number
let mail: Sent[]

beforeEach(() => {
  db = openLocalD1()
  clock = 1_795_350_000_000
  mail = []
})

function env() {
  // The stand-in implements only the D1 calls the worker uses.
  return { DB: db as unknown as D1Database }
}

async function call(method: string, path: string, body?: unknown, headers: Record<string, string> = {}) {
  const init: RequestInit = { method, headers: { ...headers } }
  if (body instanceof Uint8Array) {
    init.body = body as Uint8Array<ArrayBuffer>
  } else if (body !== undefined) {
    init.body = JSON.stringify(body)
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
  }
  const res = await handleRequest(new Request('https://sync.test' + path, init), env(), {
    now: () => clock,
    mail: {
      sendOTP: async (email: string, code: string, purpose: 'login' | 'reset') => {
        mail.push({ email, code, purpose })
      },
    },
  })
  const text = res.status === 304 ? '' : await res.text()
  return { status: res.status, etag: res.headers.get('ETag') || '', json: text ? JSON.parse(text) : undefined }
}

const auth = (token: string) => ({ Authorization: 'Bearer ' + token })

async function signIn(email: string, deviceId: string, name = deviceId, avatar = '') {
  const sent = await call('POST', '/v1/auth/otp/request', { email })
  assert.equal(sent.status, 200, JSON.stringify(sent.json))
  const code = mail.at(-1)!.code
  const verified = await call('POST', '/v1/auth/otp/verify', {
    email,
    code,
    device: { id: deviceId, name, avatar, os: 'windows', version: '0.17.0' },
  })
  assert.equal(verified.status, 200, JSON.stringify(verified.json))
  clock += 61_000 // past the per-email resend cooldown
  return verified.json.token as string
}

function meta(monitors: { id: string; n: string; dh?: string }[]) {
  return { boards: [{ id: 'main', name: '核心', m: monitors.map((m) => m.id) }], monitors }
}

function state(s: number, ts = clock) {
  return { ts, m: { api: { s, u: 10000, l: 12 } } }
}

test('email code signs in, creates the account once and rejects wrong or reused codes', async () => {
  await call('POST', '/v1/auth/otp/request', { email: 'User@Example.com ' })
  assert.equal(mail.length, 1)
  assert.equal(mail[0].email, 'user@example.com')
  assert.match(mail[0].code, /^\d{6}$/)

  const again = await call('POST', '/v1/auth/otp/request', { email: 'user@example.com' })
  assert.equal(again.status, 429, 'a second code inside the cooldown is refused')

  const wrongCode = mail[0].code === '000000' ? '111111' : '000000'
  const device = { id: 'dev-a', name: '公司主机' }
  const wrong = await call('POST', '/v1/auth/otp/verify', { email: 'user@example.com', code: wrongCode, device })
  assert.equal(wrong.status, 401)

  const ok = await call('POST', '/v1/auth/otp/verify', { email: 'user@example.com', code: mail[0].code, device })
  assert.equal(ok.status, 200)
  assert.match(ok.json.token, /^lgs_/)
  assert.equal(ok.json.user.hasPassword, false)

  const reused = await call('POST', '/v1/auth/otp/verify', { email: 'user@example.com', code: mail[0].code, device })
  assert.equal(reused.status, 400, 'a used code cannot sign in twice')

  const me = await call('GET', '/v1/auth/me', undefined, auth(ok.json.token))
  assert.equal(me.json.user.email, 'user@example.com')
  assert.equal(me.json.devicesCount, 1)
})

test('a code that never reached the inbox is not left behind', async () => {
  const res = await handleRequest(
    new Request('https://sync.test/v1/auth/otp/request', { method: 'POST', body: JSON.stringify({ email: 'a@b.cn' }) }),
    env(),
    { now: () => clock, mail: { sendOTP: async () => Promise.reject(new Error('Resend 宕机')) } },
  )
  assert.equal(res.status, 502)
  const left = await db.prepare('SELECT COUNT(*) AS n FROM otp_codes').first<{ n: number }>()
  assert.equal(left?.n, 0)
})

test('password can be set, used to sign in, and reset with an email code', async () => {
  const token = await signIn('pw@example.com', 'dev-a')
  assert.equal((await call('POST', '/v1/auth/password', { newPassword: 'short' }, auth(token))).status, 400)
  assert.equal((await call('POST', '/v1/auth/password', { newPassword: 'Correct-Horse-1' }, auth(token))).status, 200)

  const device = { id: 'dev-b', name: '家里电脑' }
  const bad = await call('POST', '/v1/auth/login', { email: 'pw@example.com', password: 'nope-nope', device })
  assert.equal(bad.status, 401)
  const good = await call('POST', '/v1/auth/login', { email: 'pw@example.com', password: 'Correct-Horse-1', device })
  assert.equal(good.status, 200)
  assert.equal(good.json.user.hasPassword, true)

  await call('POST', '/v1/auth/otp/request', { email: 'pw@example.com', purpose: 'reset' })
  const reset = await call('POST', '/v1/auth/password/reset', {
    email: 'pw@example.com',
    code: mail.at(-1)!.code,
    newPassword: 'Another-Horse-2',
  })
  assert.equal(reset.status, 200)
  const old = await call('POST', '/v1/auth/login', { email: 'pw@example.com', password: 'Correct-Horse-1', device })
  assert.equal(old.status, 401)
})

test('signing in again on a device replaces its session', async () => {
  const first = await signIn('s@example.com', 'dev-a')
  const second = await signIn('s@example.com', 'dev-a')
  assert.equal((await call('GET', '/v1/auth/me', undefined, auth(first))).status, 401)
  assert.equal((await call('GET', '/v1/auth/me', undefined, auth(second))).status, 200)
  const sessions = await db.prepare('SELECT COUNT(*) AS n FROM sessions').first<{ n: number }>()
  assert.equal(sessions?.n, 1)
})

test('push stores only what changed and pull answers 304 until something does', async () => {
  const a = await signIn('sync@example.com', 'dev-a', '公司主机', 'data:image/png;base64,' + 'A'.repeat(4000))
  const b = await signIn('sync@example.com', 'dev-b', '家里电脑')

  const empty = await call('POST', '/v1/sync/push', { metaHash: 'm1', stateHash: 's1' }, auth(a))
  assert.equal(empty.json.needMeta, true, 'a device the server knows nothing about is asked for its meta')

  const full = await call(
    'POST',
    '/v1/sync/push',
    { metaHash: 'm1', stateHash: 's1', meta: meta([{ id: 'api', n: '接口' }]), state: state(1) },
    auth(a),
  )
  assert.equal(full.status, 200)
  assert.equal(full.json.needMeta, false)
  const seq = full.json.seq as number

  const beat = await call('POST', '/v1/sync/push', { metaHash: 'm1', stateHash: 's1' }, auth(a))
  assert.equal(beat.json.seq, seq, 'a heartbeat does not advance the sequence')

  const snap = await call('GET', '/v1/sync/snapshot?since=0', undefined, auth(b))
  const peer = snap.json.devices.find((d: { id: string }) => d.id === 'dev-a')
  assert.equal(peer.meta.monitors[0].n, '接口')
  assert.equal(peer.state.m.api.s, 1)
  assert.ok(peer.avatar.startsWith('data:image/png'), 'the first pull carries the avatar')

  const same = await call('GET', '/v1/sync/snapshot?since=' + snap.json.seq, undefined, {
    ...auth(b),
    'If-None-Match': snap.etag,
  })
  assert.equal(same.status, 304)

  await call('POST', '/v1/sync/push', { metaHash: 'm1', stateHash: 's2', state: state(2) }, auth(a))
  const delta = await call('GET', '/v1/sync/snapshot?since=' + snap.json.seq, undefined, {
    ...auth(b),
    'If-None-Match': snap.etag,
  })
  assert.equal(delta.status, 200)
  const changed = delta.json.devices.find((d: { id: string }) => d.id === 'dev-a')
  assert.equal(changed.state.m.api.s, 2)
  assert.equal(changed.meta, undefined, 'unchanged meta is not sent again')
  assert.equal(changed.avatar, undefined, 'nor is the avatar that travels with it')
})

test('the sequence never goes back when the newest device is unbound', async () => {
  const a = await signIn('seq@example.com', 'dev-a')
  const b = await signIn('seq@example.com', 'dev-b')
  const c = await signIn('seq@example.com', 'dev-c')
  await call('POST', '/v1/sync/push', { metaHash: 'ma', stateHash: 'sa', meta: meta([{ id: 'api', n: 'A' }]), state: state(1) }, auth(a))
  const newest = await call('POST', '/v1/sync/push', { metaHash: 'mb', stateHash: 'sb', meta: meta([{ id: 'api', n: 'B' }]), state: state(1) }, auth(b))
  const seen = (await call('GET', '/v1/sync/snapshot?since=0', undefined, auth(c))).json.seq
  assert.equal(seen, newest.json.seq)

  assert.equal((await call('DELETE', '/v1/devices/dev-b', undefined, auth(c))).status, 200)
  const after = await call('POST', '/v1/sync/push', { metaHash: 'ma', stateHash: 'sa2', state: state(2) }, auth(a))
  assert.ok(after.json.seq > seen, `seq ${after.json.seq} must pass what clients already saw (${seen})`)

  const pulled = await call('GET', '/v1/sync/snapshot?since=' + seen, undefined, auth(c))
  const devA = pulled.json.devices.find((d: { id: string }) => d.id === 'dev-a')
  assert.equal(devA.state.m.api.s, 2, 'the change made after the unbind still reaches the other device')
  assert.equal(pulled.json.devices.some((d: { id: string }) => d.id === 'dev-b'), false)
  assert.equal((await call('POST', '/v1/sync/push', { metaHash: 'mb' }, auth(b))).status, 401, 'the unbound device is signed out')
})

test('docs are requested only when their hash changes and served with an ETag', async () => {
  const a = await signIn('doc@example.com', 'dev-a')
  const b = await signIn('doc@example.com', 'dev-b')
  const first = await call(
    'POST',
    '/v1/sync/push',
    { metaHash: 'm1', stateHash: 's1', meta: meta([{ id: 'api', n: '接口', dh: 'h1' }]), state: state(1) },
    auth(a),
  )
  assert.deepEqual(first.json.needDocs, ['api'])
  await call('PUT', '/v1/sync/docs', { docs: [{ monitorId: 'api', hash: 'h1', markdown: '# 接口\n部署在 8080' }] }, auth(a))
  const beat = await call('POST', '/v1/sync/push', { metaHash: 'm1', stateHash: 's1' }, auth(a))
  assert.deepEqual(beat.json.needDocs, [])

  const doc = await call('GET', '/v1/sync/devices/dev-a/docs/api', undefined, auth(b))
  assert.equal(doc.json.markdown, '# 接口\n部署在 8080')
  const cached = await call('GET', '/v1/sync/devices/dev-a/docs/api', undefined, { ...auth(b), 'If-None-Match': doc.etag })
  assert.equal(cached.status, 304)

  const other = await signIn('someone-else@example.com', 'dev-x')
  assert.equal((await call('GET', '/v1/sync/devices/dev-a/docs/api', undefined, auth(other))).status, 404, 'another account cannot read it')
})

test('a gzip body that inflates past the cap is refused', async () => {
  const token = await signIn('zip@example.com', 'dev-a')
  const bomb = gzipSync(Buffer.from('{"metaHash":"' + ' '.repeat(2 * 1024 * 1024) + '"}'))
  assert.ok(bomb.length < 10 * 1024)
  const res = await call('POST', '/v1/sync/push', new Uint8Array(bomb), { ...auth(token), 'Content-Encoding': 'gzip' })
  assert.equal(res.status, 413)

  const small = gzipSync(Buffer.from(JSON.stringify({ metaHash: 'm1', stateHash: 's1' })))
  const ok = await call('POST', '/v1/sync/push', new Uint8Array(small), { ...auth(token), 'Content-Encoding': 'gzip' })
  assert.equal(ok.status, 200)
})

test('signing out with unbind removes the device and its data', async () => {
  const a = await signIn('out@example.com', 'dev-a')
  const b = await signIn('out@example.com', 'dev-b')
  await call('POST', '/v1/sync/push', { metaHash: 'm1', stateHash: 's1', meta: meta([{ id: 'api', n: '接口', dh: 'h1' }]), state: state(1) }, auth(a))
  await call('PUT', '/v1/sync/docs', { docs: [{ monitorId: 'api', hash: 'h1', markdown: 'x' }] }, auth(a))
  assert.equal((await call('POST', '/v1/auth/logout', { unbindDevice: true }, auth(a))).status, 200)
  assert.equal((await call('GET', '/v1/auth/me', undefined, auth(a))).status, 401)
  const snap = await call('GET', '/v1/sync/snapshot?since=0', undefined, auth(b))
  assert.deepEqual(snap.json.devices.map((d: { id: string }) => d.id), ['dev-b'])
  const docs = await db.prepare('SELECT COUNT(*) AS n FROM device_docs').first<{ n: number }>()
  assert.equal(docs?.n, 0)
})
