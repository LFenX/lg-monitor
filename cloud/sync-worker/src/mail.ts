/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import type { Env } from './types.ts'

export interface MailSender {
  sendOTP(email: string, code: string, purpose: 'login' | 'reset'): Promise<void>
}

export function createMailSender(env: Env): MailSender {
  return {
    async sendOTP(email: string, code: string, purpose: 'login' | 'reset'): Promise<void> {
      const appName = env.APP_NAME || 'lgmon'
      const from = env.MAIL_FROM || 'lgmon <noreply@lfen.ink>'
      const action = purpose === 'reset' ? '重置密码' : '登录并绑定设备'
      const subject = `[${appName}] 你的验证码是 ${code}`
      const text = [
        `你好，`,
        ``,
        `你正在 ${appName} 中${action}，本次邮箱验证码为：`,
        ``,
        `    ${code}`,
        ``,
        `验证码 10 分钟内有效，请勿泄露给他人。如果不是本人操作，直接忽略本邮件即可。`,
      ].join('\n')

      const html = `<!doctype html>
<html lang="zh-CN">
<body style="margin:0;padding:24px;background:#faf9f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;color:#141413;">
  <div style="max-width:460px;margin:0 auto;background:#ffffff;border:1px solid #e8e5db;border-radius:16px;padding:28px 26px;">
    <div style="font-size:18px;font-weight:700;letter-spacing:-0.02em;color:#c96442;margin-bottom:14px;">${appName}</div>
    <p style="margin:0 0 14px;font-size:14px;line-height:1.65;color:#3d3d3a;">你正在 ${appName} 中<strong>${action}</strong>，请使用下方 6 位验证码继续：</p>
    <div style="margin:18px 0;padding:16px;border-radius:12px;background:#f2f0e8;text-align:center;font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:30px;font-weight:700;letter-spacing:0.28em;color:#141413;">${code}</div>
    <p style="margin:0;font-size:12.5px;line-height:1.6;color:#73726c;">验证码 <strong>10 分钟</strong>内有效，请勿转发给他人。如果这不是你本人的操作，可以放心忽略此邮件。</p>
  </div>
</body>
</html>`

      if (!env.RESEND_API_KEY) {
        if (env.DEV_MODE === 'true') {
          console.log(`[DEV_MODE] OTP for ${email} (${purpose}): ${code}`)
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
        body: JSON.stringify({
          from,
          to: [email],
          subject,
          text,
          html,
        }),
      })

      if (!res.ok) {
        const detail = await res.text().catch(() => '')
        throw new Error(`验证码发送失败（Resend HTTP ${res.status}）${detail ? ': ' + detail.slice(0, 160) : ''}`)
      }
    },
  }
}
