import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { APIError } from './api'
import { SharedTeamView } from './SharedTeams'
import type { Shared } from './team'

let api: { request: ReturnType<typeof vi.fn> }
vi.mock('./state', () => ({ useApp: () => ({ api }) }))
vi.mock('./DashboardPanel', () => ({
  DashboardPanel: ({ result, error }: { result?: { rows: string[][] }; error?: string }) => (
    <article>{error ? <div role="alert">{error}</div> : result?.rows.flat().join(',')}</article>
  ),
}))
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

it('refreshes definition before each card exactly once, polls from one clock and clears revoked rows', async () => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  const shared: Shared = {
    id: 'share-test',
    teamId: 'team-test',
    teamName: '团队',
    ownerName: '负责人',
    dashboardId: 'overview',
    title: '共享测试',
    revision: 1,
    version: 'v1',
    refresh: '10s',
    defaultRange: '1h',
    collectors: [],
    panels: [{ id: 'hosts', type: 'table', title: '主机', width: 12 }],
  }
  let epoch = 1,
    previous = 0,
    revoked = false
  const cards: number[] = []
  api = {
    request: vi.fn(async (path: string) => {
      if (!path.includes('/panels/')) {
        if (revoked) throw new APIError('分享已撤销', 404)
        return { ...shared, version: 'v' + epoch }
      }
      cards.push(epoch)
      if (previous === epoch) throw new APIError('同一张卡片十秒内只能读取一次', 429)
      previous = epoch
      return {
        columns: ['value'],
        rows: [['scope-row-' + epoch]],
        params: {},
        version: 'v' + epoch,
        revision: epoch,
      }
    }),
  }
  render(<SharedTeamView joined="joined-test" share="share-test" />)
  await screen.findByText('scope-row-1')
  epoch = 2
  fireEvent.click(screen.getByRole('button', { name: '刷新共享面板' }))
  await screen.findByText('scope-row-2')
  expect(cards).toEqual([1, 2])
  epoch = 3
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000)
  })
  await screen.findByText('scope-row-3')
  expect(cards).toEqual([1, 2, 3])
  revoked = true
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000)
  })
  await waitFor(() => expect(screen.queryByText('scope-row-3')).not.toBeInTheDocument())
  expect(screen.getByText(/已清空上次结果/)).toBeVisible()
  expect(cards).toEqual([1, 2, 3])
})
