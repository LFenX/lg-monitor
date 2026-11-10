import { Chart } from './Chart'
import type { ChartSeries } from './Chart'
import { DashboardBar } from './DashboardBar'
import type { DashboardPanelDefinition, PanelResult } from './dashboard'
import { chartUnit } from './format'
import type { Query } from './types'
import { Badge, Empty, ErrorNotice, Panel } from './ui'
import { Donut, Gauge, Stat, TopList, Timeline, Trend } from './Viz'
import './dashboards.css'

export function DashboardPanel({
  panel,
  result,
  error = '',
  loading = false,
  params,
}: {
  panel: DashboardPanelDefinition
  result?: Query
  error?: string
  loading?: boolean
  params?: { from: number; to: number; collector: string | null }
}) {
  const data = result
  return (
    <div
      className="dashboard-cell"
      style={{ gridColumn: 'span ' + panel.width }}
      data-type={panel.type}
      data-width={panel.width}
    >
      <Panel title={panel.title} extra={loading && !data && <span className="small muted">正在读取…</span>}>
        {panel.hint && <p className="panel-hint">{panel.hint}</p>}
        <div className="panel-body">
          <ErrorNotice message={error ? '面板「' + panel.title + '」：' + error : ''} />
          {!error &&
            (data ? (
              data.rows.length ? (
                <Contents panel={panel} data={data} period={params} />
              ) : (
                <Empty title={panel.type === 'alerts' ? '没有告警' : '这段时间暂无数据'} />
              )
            ) : (
              <Empty title={loading ? '正在读取查询结果' : '尚未查询'} />
            ))}
          {data?.truncated && (
            <p className="notice small">查询结果已截断，请缩小时间范围或在 SQL 中限制行数。</p>
          )}
        </div>
      </Panel>
    </div>
  )
}

function cell(value: unknown) {
  return value == null ? '—' : typeof value === 'object' ? JSON.stringify(value) : String(value)
}
function Contents({
  panel,
  data,
  period,
}: {
  panel: DashboardPanelDefinition
  data: PanelResult
  period?: { from: number; to: number }
}) {
  const index = (column: string) => data.columns.indexOf(column)
  const get = (row: unknown[], column: string) => row[index(column)]
  const number = (value: unknown) => (typeof value === 'number' && Number.isFinite(value) ? value : null)
  const limits = { unit: panel.unit, warn: panel.warn, critical: panel.critical }
  const labelled = () =>
    data.rows.map((row): [string, number | null] => [String(get(row, 'label')), number(get(row, 'value'))])
  switch (panel.type) {
    case 'stat':
      return <Stat value={get(data.rows[0], 'value')} {...limits} />
    case 'gauge':
      return <Gauge value={get(data.rows[0], 'value')} max={panel.max} label={panel.title} {...limits} />
    case 'trend':
      return (
        <Trend
          label={panel.title}
          points={data.rows
            .map((row): [number, number | null] => [get(row, 'ts') as number, number(get(row, 'value'))])
            .sort((a, b) => a[0] - b[0])}
          {...limits}
        />
      )
    case 'line':
    case 'area': {
      const groups = new Map<string, ChartSeries>()
      for (const row of data.rows) {
        const name =
          index('series') < 0 || get(row, 'series') == null ? panel.title : String(get(row, 'series'))
        if (!groups.has(name)) groups.set(name, { name, data: [] })
        groups.get(name)!.data.push([get(row, 'ts') as number, get(row, 'value') as number | null])
      }
      for (const group of groups.values()) group.data.sort((a, b) => a[0] - b[0])
      return (
        <Chart
          title={panel.title}
          series={[...groups.values()]}
          period={period}
          unit={chartUnit(panel.unit)}
          area={panel.type === 'area'}
        />
      )
    }
    case 'bar':
      return (
        <DashboardBar
          title={panel.title}
          labels={data.rows.map((row) => String(get(row, 'label')))}
          values={data.rows.map((row) => get(row, 'value') as number | null)}
          unit={panel.unit}
        />
      )
    case 'toplist':
      return <TopList rows={labelled()} {...limits} />
    case 'pie':
      return <Donut rows={labelled()} unit={panel.unit} label={panel.title} />
    case 'timeline':
      return (
        <Timeline
          rows={data.rows.map((row): [number, string, string] => [
            get(row, 'ts') as number,
            String(get(row, 'label')),
            String(get(row, 'state')),
          ])}
          period={period}
        />
      )
    case 'status_grid':
      return (
        <div className="dashboard-status-grid">
          {data.rows.map((row, i) => (
            <div className="dashboard-status" key={i} data-state={String(get(row, 'state'))}>
              <strong title={String(get(row, 'label'))}>{String(get(row, 'label'))}</strong>
              <Badge state={String(get(row, 'state'))} />
            </div>
          ))}
        </div>
      )
    case 'alerts':
      return (
        <ul className="dashboard-alerts">
          {data.rows.map((row, i) => (
            <li key={i}>
              <div className="inline">
                <Badge state={String(get(row, 'severity'))} />
                <Badge state={String(get(row, 'state'))} />
              </div>
              <p>{String(get(row, 'summary'))}</p>
              {index('started_at') >= 0 &&
                typeof get(row, 'started_at') === 'number' &&
                Number.isFinite(new Date(get(row, 'started_at') as number).getTime()) && (
                  <small className="muted">
                    {new Date(get(row, 'started_at') as number).toLocaleString()}
                  </small>
                )}
            </li>
          ))}
        </ul>
      )
    case 'log':
      return (
        <ol className="dashboard-log">
          {data.rows.map((row, i) => (
            <li key={i}>
              <time>{new Date(get(row, 'ts') as number).toLocaleString()}</time>
              {index('level') >= 0 && <span className="soft-label">{cell(get(row, 'level'))}</span>}
              <pre>{String(get(row, 'message'))}</pre>
            </li>
          ))}
        </ol>
      )
  }
  return (
    <div className="table-scroll">
      <table>
        <thead>
          <tr>
            {data.columns.map((column) => (
              <th key={column}>{column}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {data.rows.map((row, i) => (
            <tr key={i}>
              {row.map((value, j) => (
                <td key={j}>{cell(value)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
