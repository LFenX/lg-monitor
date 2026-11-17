import { fireEvent, render, screen, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { API } from './api'
import { JobBriefText, JobSection, jobTone } from './Job'
import type { JobAttempt, JobDay, JobDayDetail, JobDays, Monitor } from './types'

let api: API
// 2026-11-18 10:00 in Beijing.
const now = Date.UTC(2026, 10, 18, 2)
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ api, pulse: 0, now: () => now }),
}))

const at = (hour: number, minute = 0) => Date.UTC(2026, 10, 17, hour - 8, minute)

const run = (
  id: string,
  trigger: JobAttempt['trigger'],
  status: JobAttempt['status'],
  extra: Partial<JobAttempt> = {},
): JobAttempt => ({
  id,
  trigger,
  status,
  summary: status === 'success' ? '全部阶段成功' : '海外 S3 上传失败',
  startedAt: at(2),
  finishedAt: at(2, 30),
  lastEventAt: at(2, 30),
  events: 9,
  stages: [
    {
      id: 'fetch',
      name: '生产数据获取',
      state: 'success',
      startedAt: at(2, 1),
      finishedAt: at(2, 6),
      critical: true,
      updates: 2,
    },
    {
      id: 'upload_overseas',
      name: '海外 S3 上传',
      state: status === 'success' ? 'success' : 'failed',
      finishedAt: at(2, 20),
      error: status === 'success' ? undefined : 'S3 PutObject timeout',
      receipt: status === 'success' ? 'ETag fake' : undefined,
      needsReceipt: true,
      updates: 1,
    },
  ],
  partitions: [
    {
      region: 'cn',
      regionName: '国内',
      hour: Date.UTC(2026, 10, 16, 0),
      hourLabel: '2026/11/16 08 时（UTC+8）',
      uniqueRecipients: 812,
      receivedRows: 10233,
      gzFiles: 1,
      compressedBytes: 3355443,
      targetDir: 'oss://fake/2026/11/16/08/',
      verify: 'ok',
      upload: 'ok',
      ts: at(2, 20),
    },
    {
      region: 'overseas',
      regionName: '海外',
      hour: Date.UTC(2026, 10, 16, 0),
      hourLabel: '2026/11/16 00 时（UTC+0）',
      verify: 'ok',
      upload: status === 'success' ? 'ok' : 'failed',
      ts: at(2, 20),
    },
  ],
  ...extra,
})

const day = (date: string, status: JobDay['status'], attempts: JobAttempt[] = [], summary = ''): JobDay => ({
  date,
  status,
  summary: summary || status,
  enabled: status !== 'disabled',
  attempts,
})

const job = {
  id: 'aics',
  name: 'AICS 每日上传',
  schedule: '每天 02:00（北京时间）',
  stages: [],
  source: 'api' as const,
  production: ['db'],
}

it('opens the latest day with news and keeps dependency state apart from the run’s own fetch', async () => {
  const days: JobDays = {
    job,
    days: [
      day('2026-11-18', 'pending', [], '计划 02:00 运行'),
      day(
        '2026-11-17',
        'success',
        [run('m1', 'manual', 'success', { startedAt: at(9) }), run('a1', 'scheduled', 'partial')],
        '全部阶段成功（共 2 次运行）',
      ),
      day('2026-11-16', 'missed', [], '计划 02:00 运行，过了宽限 2 小时还没有任何运行'),
      day('2026-11-15', 'disabled'),
      day('2026-11-14', 'disabled'),
      day('2026-11-13', 'disabled'),
    ],
  }
  const detail: JobDayDetail = {
    job,
    day: days.days[0],
    production: [{ id: 'db', name: '生产库', state: 'down', issue: '端口不通' }],
  }
  api = new API('fake', (async (input: RequestInfo | URL) => {
    const path = String(input)
    return Response.json(path.includes('/days/2026-11-18') ? detail : days)
  }) as unknown as typeof fetch)
  render(<JobSection monitor={{ id: 'aics', name: 'AICS 每日上传', kind: 'job' } as Monitor} />)
  const opened = (await screen.findByText('11月17日 周二')).closest('li')!
  expect(within(opened).getByRole('button', { expanded: true })).toBeVisible()
  // The rerun is listed first, and the earlier failure is still there.
  const runs = within(opened).getAllByRole('article')
  expect(runs[0]).toHaveTextContent('第 2 次 · 手动补跑')
  expect(runs[1]).toHaveTextContent('第 1 次 · 计划运行')
  expect(runs[1]).toHaveTextContent('S3 PutObject timeout')
  // The path is broken now, yet this run fetched its data: both are shown, neither hides the other.
  expect(within(opened).getByText('生产通路现在').parentElement).toHaveTextContent('1 个不正常')
  expect(within(opened).getByText('本次生产取数').parentElement).toHaveTextContent('成功')
  const table = within(runs[1]).getByRole('region', { name: '小时分区数据' })
  expect(table).toHaveTextContent('2026/11/16 08 时（UTC+8）')
  expect(table).toHaveTextContent('2026/11/16 00 时（UTC+0）')
  expect(table).toHaveTextContent('10,233')
  expect(table).toHaveTextContent('3.2 MiB')
  expect(within(table).getAllByText('失败')[0]).toHaveAttribute('data-outcome', 'failed')
  // Three quiet days fold into one line, and a missed day is closed until asked for.
  expect(screen.getByText('未启用，共 3 天（不算漏跑）')).toBeVisible()
  const missed = screen.getByText('11月16日 周一').closest('button')!
  expect(missed).toHaveAttribute('aria-expanded', 'false')
  fireEvent.click(missed)
  expect(await screen.findByText('这一天还没有收到任何运行的事件。', { exact: false })).toBeVisible()
  expect(screen.getByText('lgmon job key aics')).toBeVisible()
})

it('never colours an unknown or pending day as healthy', () => {
  expect(jobTone('success')).toBe('up')
  for (const status of ['failed', 'partial', 'missed'] as const) expect(jobTone(status)).toBe('down')
  expect(jobTone('unknown')).toBe('unknown')
  expect(jobTone('pending')).toBe('waiting')
  render(<JobBriefText brief={{ date: '2026-11-15', status: 'unknown', summary: '国内 OSS 上传没有回执' }} />)
  expect(screen.getByText('11月15日 结果未知 · 国内 OSS 上传没有回执')).toHaveClass('wait')
})
