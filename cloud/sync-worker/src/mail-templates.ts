/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

/*
 * Verification emails. Mail clients ignore <style> blocks and SVG to varying degrees, so everything is
 * tables with inline styles, the logo is drawn with spans, and every value from a request is escaped.
 */

export type MailPurpose = 'login' | 'register' | 'reset'
export type MailLang = 'zh' | 'en'

export interface MailContext {
  email: string
  code: string
  purpose: MailPurpose
  lang: MailLang
  /** Name of the computer asking, shown in sign-in mail so a stranger's attempt stands out. */
  deviceName?: string
  os?: string
  ttlMinutes: number
}

export interface RenderedMail {
  subject: string
  text: string
  html: string
}

const brand = '#c96442'
const ink = '#141413'
const body = '#3d3d3a'
const muted = '#73726c'
const line = '#e8e5db'
const paper = '#faf9f5'
const canvas = '#f2efe6'
const font =
  "-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Hiragino Sans GB','Microsoft YaHei',Helvetica,Arial,sans-serif"
const mono = "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace"

export function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

function osLabel(os: string | undefined): string {
  switch ((os || '').toLowerCase()) {
    case 'windows':
      return 'Windows'
    case 'darwin':
      return 'macOS'
    case 'linux':
      return 'Linux'
    default:
      return ''
  }
}

const copy = {
  zh: {
    preheader: {
      login: '用这个验证码登录 lgmon，10 分钟内有效。',
      register: '确认邮箱，完成 lgmon 注册。',
      reset: '用这个验证码重置 lgmon 登录密码。',
    },
    subject: {
      login: (code: string) => `lgmon 登录验证码 ${code}`,
      register: (code: string) => `欢迎注册 lgmon · 验证码 ${code}`,
      reset: (code: string) => `lgmon 重置密码验证码 ${code}`,
    },
    eyebrow: { login: '登录验证', register: '欢迎加入', reset: '重置密码' },
    title: { login: '你的登录验证码', register: '确认邮箱，完成注册', reset: '重置你的登录密码' },
    lead: {
      login: '你正在用这个邮箱登录 lgmon，在登录窗口输入下面的验证码即可。',
      register: '感谢注册 lgmon。在注册窗口输入下面的验证码，这台电脑就会绑定到你的账号。',
      reset: '你正在为这个邮箱的 lgmon 账号设置新密码，在重置窗口输入下面的验证码。',
    },
    device: '请求来自',
    validity: (minutes: number) => `${minutes} 分钟内有效 · 只能使用一次`,
    ignore: {
      login: '不是你本人在登录？忽略这封邮件即可，没有验证码别人登录不了你的账号。',
      register: '没有注册过 lgmon？忽略这封邮件即可，不会创建任何账号。',
      reset: '没有申请重置密码？忽略这封邮件即可，原密码不受影响。',
    },
    featuresTitle: '注册之后',
    features: [
      ['多台电脑，一个面板', '家里、公司、服务器上的服务按设备分组，汇总在侧边栏。'],
      ['只传变化', '服务挂了或恢复时才上传状态，平时每分钟只有几十字节的心跳。'],
      ['本地优先', '监控历史和告警留在各自的电脑上，不登录也照样能用。'],
    ],
    footer: (email: string) => `这封邮件发送给 ${email}。`,
    madeBy: 'lgmon · 由 LFen 开发',
  },
  en: {
    preheader: {
      login: 'Use this code to sign in to lgmon. It expires in 10 minutes.',
      register: 'Confirm your email to finish signing up for lgmon.',
      reset: 'Use this code to reset your lgmon password.',
    },
    subject: {
      login: (code: string) => `Your lgmon sign-in code: ${code}`,
      register: (code: string) => `Welcome to lgmon · your code is ${code}`,
      reset: (code: string) => `Reset your lgmon password: ${code}`,
    },
    eyebrow: { login: 'Sign in', register: 'Welcome', reset: 'Password reset' },
    title: { login: 'Your sign-in code', register: 'Confirm your email', reset: 'Reset your password' },
    lead: {
      login: 'You are signing in to lgmon with this email. Enter the code below in the sign-in window.',
      register: 'Thanks for signing up for lgmon. Enter the code below and this computer joins your account.',
      reset: 'You are setting a new password for the lgmon account of this email. Enter the code below.',
    },
    device: 'Requested from',
    validity: (minutes: number) => `Expires in ${minutes} minutes · works once`,
    ignore: {
      login: "Not you? Ignore this email. Nobody can sign in without the code.",
      register: "Didn't sign up for lgmon? Ignore this email and no account is created.",
      reset: "Didn't ask for a reset? Ignore this email and your password stays the same.",
    },
    featuresTitle: 'Once you are in',
    features: [
      ['Every computer, one board', 'Services at home, at work and on servers, grouped by device in the sidebar.'],
      ['Only changes travel', 'Status goes up when a service fails or recovers; otherwise a heartbeat of a few dozen bytes a minute.'],
      ['Local first', 'History and alerts stay on each computer, and lgmon works fine signed out.'],
    ],
    footer: (email: string) => `This email was sent to ${email}.`,
    madeBy: 'lgmon · made by LFen',
  },
} as const

function logo(size: number, light: boolean): string {
  const box = light ? paper : brand
  const bar = light ? brand : paper
  const bars = [0.42, 0.6, 0.8, 1]
    .map(
      (opacity) =>
        `<span style="display:inline-block;width:${Math.round(size * 0.1)}px;height:${Math.round(size * 0.42)}px;margin:0 ${Math.max(1, Math.round(size * 0.03))}px;background:${bar};opacity:${opacity};border-radius:2px;vertical-align:middle;"></span>`,
    )
    .join('')
  return `<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="border-collapse:separate;"><tr><td width="${size}" height="${size}" align="center" valign="middle" style="width:${size}px;height:${size}px;background:${box};border-radius:${Math.round(size * 0.28)}px;line-height:0;font-size:0;">${bars}</td></tr></table>`
}

function wordmark(color: string, sub: string): string {
  return `<span style="font-family:${font};font-size:19px;font-weight:700;letter-spacing:-0.02em;color:${color};vertical-align:middle;">lgmon</span><span style="font-family:Georgia,serif;font-style:italic;font-size:12px;color:${sub};vertical-align:middle;padding-left:6px;">by LFen</span>`
}

function codePanel(code: string, validity: string): string {
  // Six 38px cells and five gaps stay inside a 375px phone screen without media queries.
  const gap = '<td style="width:6px;font-size:0;line-height:0;">&nbsp;</td>'
  const digits = code
    .split('')
    .map(
      (digit) =>
        `<td align="center" valign="middle" style="width:38px;height:52px;background:#ffffff;border:1px solid ${line};border-radius:10px;font-family:${mono};font-size:26px;font-weight:700;color:${ink};">${escapeHtml(digit)}</td>`,
    )
    .join(gap)
  return `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f7f5ef;border:1px solid #eee9dd;border-radius:14px;">
  <tr><td align="center" style="padding:22px 8px 6px;">
    <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>${digits}</tr></table>
  </td></tr>
  <tr><td align="center" style="padding:6px 12px 18px;font-family:${font};font-size:12.5px;color:${muted};">${escapeHtml(validity)}</td></tr>
</table>`
}

function deviceChip(label: string, deviceName: string, os: string): string {
  const name = [deviceName, os].filter(Boolean).map(escapeHtml).join(' · ')
  return `<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 18px;"><tr><td style="background:${paper};border:1px solid ${line};border-radius:999px;padding:7px 14px;font-family:${font};font-size:13px;color:${body};"><span style="color:${muted};">${escapeHtml(label)}</span>&nbsp;&nbsp;<strong style="color:${ink};font-weight:600;">${name}</strong></td></tr></table>`
}

function features(title: string, rows: readonly (readonly [string, string])[]): string {
  const items = rows
    .map(
      ([head, text], index) => `<tr>
  <td valign="top" width="34" style="width:34px;padding:0 0 16px;">
    <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td width="24" height="24" align="center" valign="middle" style="width:24px;height:24px;border-radius:12px;background:#f5e4dc;font-family:${font};font-size:12px;font-weight:700;color:${brand};">${index + 1}</td></tr></table>
  </td>
  <td valign="top" style="padding:2px 0 16px;font-family:${font};">
    <div style="font-size:14px;font-weight:600;color:${ink};line-height:1.4;">${escapeHtml(head)}</div>
    <div style="font-size:13px;color:${muted};line-height:1.6;padding-top:2px;">${escapeHtml(text)}</div>
  </td>
</tr>`,
    )
    .join('')
  return `<tr><td style="padding:8px 28px 4px;">
  <div style="border-top:1px solid ${line};padding-top:24px;font-family:${font};font-size:12px;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;color:${muted};padding-bottom:14px;">${escapeHtml(title)}</div>
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">${items}</table>
</td></tr>`
}

export function renderMail(ctx: MailContext): RenderedMail {
  const t = copy[ctx.lang]
  const purpose = ctx.purpose
  const accent = purpose === 'reset' ? '#d4a043' : brand
  const os = osLabel(ctx.os)
  const deviceName = (ctx.deviceName || '').trim()
  const showDevice = purpose !== 'register' && deviceName !== ''
  const validity = t.validity(ctx.ttlMinutes)
  const htmlLang = ctx.lang === 'zh' ? 'zh-CN' : 'en'

  const header =
    purpose === 'register'
      ? `<tr><td style="background:${brand};border-radius:18px 18px 0 0;padding:30px 28px 34px;">
  <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td style="padding-right:10px;">${logo(30, true)}</td><td>${wordmark(paper, '#f3d9cd')}</td></tr></table>
  <div style="font-family:${font};font-size:12px;font-weight:700;letter-spacing:0.12em;text-transform:uppercase;color:#f3d9cd;padding-top:28px;">${escapeHtml(t.eyebrow.register)}</div>
  <div style="font-family:${font};font-size:26px;line-height:1.3;font-weight:700;letter-spacing:-0.01em;color:#ffffff;padding-top:6px;">${escapeHtml(t.title.register)}</div>
</td></tr>`
      : `<tr><td style="padding:28px 28px 0;">
  <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td style="padding-right:10px;">${logo(30, false)}</td><td>${wordmark(ink, muted)}</td></tr></table>
  <div style="font-family:${font};font-size:12px;font-weight:700;letter-spacing:0.12em;text-transform:uppercase;color:${accent};padding-top:28px;">${escapeHtml(t.eyebrow[purpose])}</div>
  <div style="font-family:${font};font-size:24px;line-height:1.3;font-weight:700;letter-spacing:-0.01em;color:${ink};padding-top:6px;">${escapeHtml(t.title[purpose])}</div>
</td></tr>`

  const html = `<!doctype html>
<html lang="${htmlLang}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<title>${escapeHtml(t.subject[purpose](ctx.code))}</title>
</head>
<body style="margin:0;padding:0;background:${canvas};-webkit-text-size-adjust:100%;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;mso-hide:all;">${escapeHtml(t.preheader[purpose])}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:${canvas};">
<tr><td align="center" style="padding:32px 14px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:520px;background:#ffffff;border:1px solid ${line};border-radius:18px;">
${header}
<tr><td style="padding:${purpose === 'register' ? '28px' : '14px'} 28px 0;font-family:${font};font-size:14.5px;line-height:1.7;color:${body};">${escapeHtml(t.lead[purpose])}</td></tr>
<tr><td style="padding:22px 28px 0;">
${showDevice ? deviceChip(t.device, deviceName, os) : ''}${codePanel(ctx.code, validity)}
</td></tr>
<tr><td style="padding:18px 28px 26px;font-family:${font};font-size:12.5px;line-height:1.65;color:${muted};">${escapeHtml(t.ignore[purpose])}</td></tr>
${purpose === 'register' ? features(t.featuresTitle, t.features) : ''}
</table>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:520px;">
<tr><td align="center" style="padding:20px 24px 4px;font-family:${font};font-size:12px;line-height:1.7;color:${muted};">${escapeHtml(t.footer(ctx.email))}</td></tr>
<tr><td align="center" style="padding:0 24px;font-family:${font};font-size:12px;line-height:1.7;color:${muted};">${escapeHtml(t.madeBy)} · <a href="https://github.com/LFenX/lg-monitor" style="color:${brand};text-decoration:none;">GitHub</a> · <a href="https://lfen.ink/" style="color:${brand};text-decoration:none;">lfen.ink</a></td></tr>
</table>
</td></tr>
</table>
</body>
</html>`

  const device = showDevice ? `${t.device}: ${[deviceName, os].filter(Boolean).join(' · ')}` : ''
  const text = [
    t.title[purpose],
    '',
    t.lead[purpose],
    ...(device ? ['', device] : []),
    '',
    `    ${ctx.code}`,
    '',
    validity,
    '',
    t.ignore[purpose],
    ...(purpose === 'register'
      ? ['', t.featuresTitle, ...t.features.map(([head, detail]) => `- ${head}: ${detail}`)]
      : []),
    '',
    '--',
    t.madeBy,
    'https://github.com/LFenX/lg-monitor',
  ].join('\n')

  return { subject: t.subject[purpose](ctx.code), text, html }
}
