export type DashboardPanelType = 'stat' | 'line' | 'bar' | 'status_grid' | 'table' | 'alerts' | 'log'
export type DashboardPanelDefinition = {
  id: string
  type: DashboardPanelType
  title: string
  query: string
  width: number
}
export type DashboardDefinition = {
  id: string
  title: string
  refresh: string
  panels: DashboardPanelDefinition[]
}
export type PanelResult = { columns: string[]; rows: unknown[][]; truncated?: boolean }
