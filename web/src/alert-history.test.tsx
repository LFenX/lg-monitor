import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { API } from './api'
import { AlertHistory } from './Alerts'
import type { Alert } from './types'

const now = new Date(2026, 10, 19, 15, 0).getTime()
const hour = 3600 * 1000
let api: API
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ api, data: { definitions: [] }, version: 0, now: () => now }),
}))

function resolved(id: string, startedAt: number, minutes: number, summary: string): Alert {
  return {
    id,
    ruleId: 'down',
    collectorId: 'fake-host',
    targetId: 'fake-app',
    state: 'resolved',
    severity: 'critical',
    startedAt,
    resolvedAt: startedAt + minutes * 60000,
    summary,
  }
}

it('keeps ended alerts readable by day, newest first, with how long each lasted', async () => {
  const asked: string[] = []
  api = new API('fake-token')
  api.request = (async (path: string) => {
    asked.push(path)
    return [
      resolved('older', now - 26 * hour, 3, '虚构网站打不开'),
      resolved('newer', now - 2 * hour, 75, '虚构数据库断开'),
    ]
  }) as typeof api.request
  const open = vi.fn()
  render(<AlertHistory open={open} />)
  const history = screen.getByRole('region', { name: '历史告警' })
  const days = await within(history).findAllByRole('heading', { level: 3 })
  expect(days.map((day) => day.textContent)).toEqual(['今天1 条', '昨天1 条'])
  const newest = within(history).getByRole('button', { name: /虚构数据库断开/ })
  expect(newest).toHaveTextContent('13:00→ 14:15')
  expect(newest).toHaveTextContent('持续 1 小时 15 分')
  await userEvent.click(newest)
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ id: 'newer' }))
  expect(asked[0]).toBe('/alerts?state=resolved&since=' + (now - 7 * 24 * hour))
  await userEvent.click(within(history).getByRole('button', { name: '30 天' }))
  expect(asked.at(-1)).toBe('/alerts?state=resolved&since=' + (now - 30 * 24 * hour))
})
