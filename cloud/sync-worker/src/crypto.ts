/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

const encoder = new TextEncoder()

export function toHex(bytes: Uint8Array): string {
  let out = ''
  for (let i = 0; i < bytes.length; i++) {
    out += bytes[i].toString(16).padStart(2, '0')
  }
  return out
}

export function fromHex(hex: string): Uint8Array<ArrayBuffer> {
  const clean = hex.trim()
  if (clean.length % 2 !== 0) return new Uint8Array(0)
  const out = new Uint8Array(clean.length / 2)
  for (let i = 0; i < out.length; i++) {
    out[i] = Number.parseInt(clean.slice(i * 2, i * 2 + 2), 16)
  }
  return out
}

export function randomHex(bytes: number): string {
  const buf = new Uint8Array(bytes)
  crypto.getRandomValues(buf)
  return toHex(buf)
}

export function randomToken(): string {
  const buf = new Uint8Array(32)
  crypto.getRandomValues(buf)
  let str = ''
  for (let i = 0; i < buf.length; i++) str += String.fromCharCode(buf[i])
  return 'lgs_' + btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** Cryptographically uniform 6-digit OTP (000000..999999). */
export function randomOTP(): string {
  const limit = 1_000_000
  const max = Math.floor(0xffffffff / limit) * limit
  const buf = new Uint32Array(1)
  while (true) {
    crypto.getRandomValues(buf)
    if (buf[0] < max) {
      return String(buf[0] % limit).padStart(6, '0')
    }
  }
}

export async function sha256Hex(input: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', encoder.encode(input))
  return toHex(new Uint8Array(digest))
}

export function constantTimeEqual(a: string, b: string): boolean {
  if (a.length !== b.length) return false
  let diff = 0
  for (let i = 0; i < a.length; i++) {
    diff |= a.charCodeAt(i) ^ b.charCodeAt(i)
  }
  return diff === 0
}

/** PBKDF2-HMAC-SHA256 (100k iterations), natively accelerated in Cloudflare Workers Web Crypto. */
export async function hashPassword(password: string, saltHex?: string): Promise<{ hash: string; salt: string }> {
  const salt = saltHex || randomHex(16)
  const key = await crypto.subtle.importKey('raw', encoder.encode(password), 'PBKDF2', false, ['deriveBits'])
  const bits = await crypto.subtle.deriveBits(
    { name: 'PBKDF2', hash: 'SHA-256', salt: fromHex(salt), iterations: 100_000 },
    key,
    256,
  )
  return { hash: toHex(new Uint8Array(bits)), salt }
}

export async function verifyPassword(password: string, salt: string, expectedHash: string): Promise<boolean> {
  if (!salt || !expectedHash) return false
  const { hash } = await hashPassword(password, salt)
  return constantTimeEqual(hash, expectedHash)
}

export function normalizeEmail(raw: unknown): string {
  if (typeof raw !== 'string') return ''
  return raw.trim().toLowerCase()
}

const EMAIL_RE = /^[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)+$/

export function isValidEmail(email: string): boolean {
  return email.length >= 5 && email.length <= 254 && EMAIL_RE.test(email)
}
