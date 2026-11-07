import { render, screen, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { DashboardPanel } from './DashboardPanel'
import type { DashboardPanelDefinition, DashboardPanelType } from './dashboard'
import type { Query } from './types'

vi.mock('./Chart', () => ({
  Chart: ({ title, series }: { title: string; series: unknown }) => (
    <div role="img" aria-label={title}>
      {JSON.stringify(series)}
    </div>
  ),
}))
vi.mock('./DashboardBar', () => ({
  DashboardBar: ({ title, labels, values }: { title: string; labels: unknown; values: unknown }) => (
    <div role="img" aria-label={title}>
      {JSON.stringify({ labels, values })}
    </div>
  ),
}))
function definition(type: DashboardPanelType, title: string = type): DashboardPanelDefinition {
  return { id: type, type, title, width: 6, query: 'SELECT 1' }
}
function result(columns: string[], rows: unknown[][]): Query {
  return { columns, rows, truncated: false }
}

it('isolates bad column contracts to the affected panel and retains a real zero', () => {
  render(
    <>
      <DashboardPanel
        panel={definition('line', '错误曲线')}
        error="需要列 ts、value；实际返回：time、amount"
      />
      <DashboardPanel panel={definition('stat', '计数')} result={result(['value'], [[0]])} />
    </>,
  )
  expect(screen.getByRole('alert')).toHaveTextContent('需要列 ts、value；实际返回：time、amount')
  expect(screen.getByText('0', { exact: true })).toBeVisible()
})
it('renders all seven panel types with safe text, independent series and null gaps', () => {
  const samples: [DashboardPanelType, Query][] = [
    ['stat', result(['value'], [[42]])],
    [
      'line',
      result(
        ['ts', 'series', 'value'],
        [
          [2000, '主机甲', 2],
          [1000, '主机甲', null],
          [1500, '主机乙', 4],
        ],
      ),
    ],
    [
      'bar',
      result(
        ['label', 'value'],
        [
          ['主机甲', 3],
          ['主机乙', 0],
        ],
      ),
    ],
    [
      'status_grid',
      result(
        ['label', 'state'],
        [
          ['网站', 'up'],
          ['数据库', 'unknown'],
        ],
      ),
    ],
    ['table', result(['名称', '值'], [['<script>bad()</script>', 0]])],
    ['alerts', result(['summary', 'severity', 'state'], [['虚构故障', 'critical', 'firing']])],
    ['log', result(['ts', 'message', 'level'], [[0, '<b>原始日志</b>', 'info']])],
  ]
  const { container } = render(
    <>
      {samples.map(([type, data]) => (
        <DashboardPanel key={type} panel={definition(type)} result={data} />
      ))}
    </>,
  )
  expect(screen.getByText('42', { exact: true })).toBeVisible()
  expect(screen.getByRole('img', { name: 'line' })).toHaveTextContent('[1000,null],[2000,2]')
  expect(screen.getByRole('img', { name: 'bar' })).toHaveTextContent('"values":[3,0]')
  expect(screen.getByText('未知', { exact: true })).toBeVisible()
  expect(screen.getByText('<script>bad()</script>')).toBeVisible()
  expect(container.querySelector('script')).toBeNull()
  expect(screen.getByText('<b>原始日志</b>')).toBeVisible()
  expect(screen.getByText('虚构故障')).toBeVisible()
})
it('keeps empty results distinct from zero', () => {
  const { container } = render(<DashboardPanel panel={definition('stat')} result={result(['value'], [])} />)
  expect(within(container).getByText('这段时间暂无数据')).toBeVisible()
  expect(within(container).queryByText('0', { exact: true })).not.toBeInTheDocument()
})
it('shows query failures and truncation without hiding them behind an empty state', () => {
  const { rerender } = render(<DashboardPanel panel={definition('table')} error="只读 SQL 超时" />)
  expect(screen.getByRole('alert')).toHaveTextContent('只读 SQL 超时')
  expect(screen.queryByText('尚未查询')).not.toBeInTheDocument()
  rerender(
    <DashboardPanel panel={definition('table')} result={{ ...result(['x'], [[1]]), truncated: true }} />,
  )
  expect(screen.getByText(/查询结果已截断/)).toBeVisible()
})
