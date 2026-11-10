export type DashboardPanelType =
  | 'stat'
  | 'gauge'
  | 'trend'
  | 'line'
  | 'area'
  | 'bar'
  | 'toplist'
  | 'pie'
  | 'status_grid'
  | 'timeline'
  | 'table'
  | 'alerts'
  | 'log'
export type DashboardPanelDefinition = {
  id: string
  type: DashboardPanelType
  title: string
  query: string
  width: number
  unit?: 'percent' | 'bytes' | 'bytes_per_sec' | 'ms' | 'seconds' | 'count'
  max?: number | null
  warn?: number | null
  critical?: number | null
  hint?: string
}
export type DashboardDefinition = {
  id: string
  title: string
  refresh: string
  panels: DashboardPanelDefinition[]
}
export type PanelResult = { columns: string[]; rows: unknown[][]; truncated?: boolean }
