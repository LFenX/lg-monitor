/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { API } from './api'
import { GrowthPage } from './Growth'
import { I18nProvider } from './i18n'
import { State } from './state'

describe('GrowthPage admin conversion funnel', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    sessionStorage.setItem('lgmon-license-admin', 'fake-admin-session')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders downloads, activations, conversion funnel, and filters by activation key', async () => {
    const now = 2_000_000_000_000
    const json = (body: unknown) =>
      new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

    const mockFetch: typeof fetch = async (input, init) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
      if (url === '/api/v1/download/stats') {
        return json({
          downloads: 200,
          stars: 55,
          forks: 9,
          version: 'v0.14.4',
          repoUrl: 'https://github.com/LFenX/lg-monitor',
          updatedAt: now,
          releases: [
            { tag: 'v0.14.4', downloads: 120 },
            { tag: 'v0.14.3', downloads: 80 },
          ],
        })
      }
      if (url.endsWith('/api/v1/stream')) {
        return new Response(new ReadableStream(), { headers: { 'Content-Type': 'text/event-stream' } })
      }
      if (url.endsWith('/api/v1/license/admin')) {
        const body = JSON.parse(String(init?.body || '{}')) as { action?: string }
        if (body.action === 'keys') {
          return json({
            keys: [
              {
                id: 'k-team-a',
                label: '内测群A',
                createdAt: now - 86_400_000 * 5,
                expiresAt: 0,
                maxDevices: 10,
                revokedAt: 0,
                state: 'active',
                devices: [
                  {
                    id: 'd1',
                    name: 'PC-1',
                    version: '0.14.4',
                    firstSeen: now - 50_000,
                    lastSeen: now - 10_000,
                  },
                  {
                    id: 'd2',
                    name: 'PC-2',
                    version: '0.14.4',
                    firstSeen: now - 50_000,
                    lastSeen: now - 10_000,
                  },
                ],
              },
              {
                id: 'k-team-b',
                label: '同事B',
                createdAt: now - 86_400_000 * 3,
                expiresAt: 0,
                maxDevices: 5,
                revokedAt: 0,
                state: 'active',
                devices: [
                  {
                    id: 'd3',
                    name: 'PC-3',
                    version: '0.14.3',
                    firstSeen: now - 50_000,
                    lastSeen: now - 10_000,
                  },
                ],
              },
            ],
          })
        }
        if (body.action === 'admin_devices') return json({ devices: [] })
        if (body.action === 'analytics') {
          return json({
            telemetryReady: true,
            devices: [
              {
                deviceId: 'd1',
                keyId: 'k-team-a',
                usage: {
                  boardsCount: 2,
                  monitorsCount: 5,
                  hasFirstBoard: true,
                  hasFirstMonitor: true,
                  features: ['agent_mcp', 'docs', 'feishu'],
                },
              },
              {
                deviceId: 'd2',
                keyId: 'k-team-a',
                usage: {
                  boardsCount: 1,
                  monitorsCount: 2,
                  hasFirstBoard: true,
                  hasFirstMonitor: true,
                  features: ['lan_web'],
                },
              },
              {
                deviceId: 'd3',
                keyId: 'k-team-b',
                usage: {
                  boardsCount: 0,
                  monitorsCount: 0,
                  hasFirstBoard: false,
                  hasFirstMonitor: false,
                  features: [],
                },
              },
            ],
          })
        }
      }
      if (url.endsWith('/api/v1/config/targets')) return json({ targets: [] })
      if (url.endsWith('/api/v1/config/notifiers')) return json({ feishuApps: [], notifiers: [] })
      if (url.endsWith('/api/v1/query')) return json({ columns: [], rows: [] })
      if (url.endsWith('/api/v1/ui/lang')) return json({ lang: 'zh' })
      if (url.endsWith('/api/v1/guide')) return json({ install: {}, state: {} })
      if (url.endsWith('/api/v1/lan'))
        return json({ visible: false, web: false, name: 'LFen', hostname: 'PC' })
      if (url.endsWith('/api/v1/notifications/pause')) return json({ active: false })
      return json([])
    }
    vi.stubGlobal('fetch', mockFetch)
    const api = new API('token', mockFetch)

    render(
      <I18nProvider>
        <State api={api} scope="config" theme="light" now={() => now} keep={false}>
          <GrowthPage />
        </State>
      </I18nProvider>,
    )

    await waitFor(() => expect(screen.getAllByText('200').length).toBeGreaterThanOrEqual(2))
    expect(screen.getByText('用户转化漏斗')).toBeInTheDocument()
    expect(screen.getByText('内测群A', { selector: 'button span' })).toBeInTheDocument()
    expect(screen.getByText('v0.14.4')).toBeInTheDocument()

    // Filter down to key "同事B" (1 activated device, 0 first boards).
    await userEvent.click(screen.getByRole('button', { name: /同事B/ }))
    expect(screen.getByText(/当前按单个密钥筛选/)).toBeInTheDocument()
  })
})
