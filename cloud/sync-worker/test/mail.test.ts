/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { renderMail } from '../src/mail-templates.ts'
import type { MailContext } from '../src/mail-templates.ts'

const base: MailContext = { email: 'me@example.com', code: '042917', purpose: 'login', lang: 'zh', ttlMinutes: 10 }

test('each purpose and language has its own subject carrying the code', () => {
  const subjects = new Set<string>()
  for (const lang of ['zh', 'en'] as const) {
    for (const purpose of ['register', 'login', 'reset'] as const) {
      const mail = renderMail({ ...base, lang, purpose })
      assert.ok(mail.subject.includes('042917'), mail.subject)
      assert.ok(mail.text.includes('042917'))
      subjects.add(mail.subject)
    }
  }
  assert.equal(subjects.size, 6)
  assert.match(renderMail({ ...base, purpose: 'register' }).subject, /欢迎注册/)
  assert.match(renderMail({ ...base, purpose: 'register', lang: 'en' }).subject, /Welcome/)
})

test('the code is laid out one digit per cell, with leading zeros kept', () => {
  const { html } = renderMail(base)
  for (const digit of '042917') assert.ok(html.includes(`>${digit}</td>`), `digit ${digit}`)
  assert.ok(html.indexOf('>0</td>') < html.indexOf('>4</td>'))
})

test('registration mail welcomes and explains; sign-in mail names the computer', () => {
  const register = renderMail({ ...base, purpose: 'register', deviceName: '公司主机' })
  assert.match(register.html, /多台电脑，一个面板/)
  assert.ok(!register.html.includes('公司主机'), 'registration does not need the device line')

  const login = renderMail({ ...base, deviceName: '家里电脑', os: 'windows' })
  assert.match(login.html, /请求来自/)
  assert.match(login.html, /家里电脑 · Windows/)
  assert.match(login.text, /请求来自: 家里电脑 · Windows/)
  assert.ok(!login.html.includes('多台电脑，一个面板'))

  const anonymous = renderMail(base)
  assert.ok(!anonymous.html.includes('请求来自'), 'no empty device line')
})

test('values from the request are escaped', () => {
  const { html } = renderMail({
    ...base,
    email: 'a"<b>@example.com',
    deviceName: '<img src=x onerror=alert(1)>',
  })
  assert.ok(!html.includes('<img src=x'))
  assert.ok(html.includes('&lt;img src=x onerror=alert(1)&gt;'))
  assert.ok(!html.includes('a"<b>'))
})
