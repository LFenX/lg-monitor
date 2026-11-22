/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { renderMail } from './mail-templates.ts'
import type { MailContext } from './mail-templates.ts'
import type { Env } from './types.ts'

export interface MailSender {
  sendOTP(ctx: MailContext): Promise<void>
}

export function createMailSender(env: Env): MailSender {
  return {
    async sendOTP(ctx: MailContext): Promise<void> {
      const from = env.MAIL_FROM || 'lgmon <noreply@lfen.ink>'
      const mail = renderMail(ctx)

      if (!env.RESEND_API_KEY) {
        if (env.DEV_MODE === 'true') {
          console.log(`[DEV_MODE] OTP for ${ctx.email} (${ctx.purpose}): ${ctx.code}`)
          return
        }
        throw new Error('邮件服务尚未配置（缺少 RESEND_API_KEY）')
      }

      const res = await fetch('https://api.resend.com/emails', {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${env.RESEND_API_KEY}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from, to: [ctx.email], subject: mail.subject, text: mail.text, html: mail.html }),
      })

      if (!res.ok) {
        const detail = await res.text().catch(() => '')
        throw new Error(`验证码发送失败（Resend HTTP ${res.status}）${detail ? ': ' + detail.slice(0, 160) : ''}`)
      }
    },
  }
}
