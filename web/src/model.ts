import type { Metric, Period, Target } from './types'

export function title(targets: Target[], id: string) {
  if (id === '@eventlog') return '事件日志'
  return id ? targets.find((item) => item.id === id)?.name || id : '主机级告警'
}
export function timestamp(value?: number) {
  return value && value > 0 ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '暂无记录'
}
export function percent(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toFixed(1) + '%' : '暂无数据'
}
export function bytes(value?: number | null) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '暂无数据'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let index = 0
  while (Math.abs(value) >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return value.toFixed(index ? 1 : 0) + ' ' + units[index]
}
export function period(hours: number, now: number): Period {
  return { from: now - hours * 3600000, to: now, step: hours > 24 ? '1h' : hours > 6 ? '1m' : 'raw' }
}
export function queryPeriod(value: Period) {
  return new URLSearchParams({ from: String(value.from), to: String(value.to), step: value.step }).toString()
}
export function hostLink(id: string) {
  return '#/hosts/' + encodeURIComponent(id)
}
export function targetLink(collector: string, target: string) {
  if (target === '@eventlog') return hostLink(collector)
  return hostLink(collector) + (target ? '/targets/' + encodeURIComponent(target) : '')
}
export function targetState(value: string) {
  return (
    (
      {
        up: '正常',
        down: '异常',
        unknown: '未知',
        firing: '触发中',
        resolved: '已恢复',
        pending: '待定',
        critical: '严重',
        warning: '警告',
        info: '提示',
      } as Record<string, string>
    )[value] || value
  )
}
export function gapFor(step?: string) {
  return step === '1h' ? 3 * 3600000 : step === '1m' ? 180000 : 30000
}
export function series(points: Metric[], gap = 30000) {
  const result = new Map<string, { name: string; data: [number, number | null][] }>()
  for (const point of points) {
    const name = point.resource || point.name
    if (!result.has(name)) result.set(name, { name, data: [] })
    result.get(name)!.data.push([point.ts, point.average])
  }
  return [...result.values()].map((item) => ({
    ...item,
    data: insertGaps(
      item.data.sort((a, b) => a[0] - b[0]),
      gap,
    ),
  }))
}
export function insertGaps(points: [number, number | null][], gap: number) {
  return points.flatMap((point, i): [number, number | null][] =>
    i && point[0] - points[i - 1][0] > gap ? [[points[i - 1][0] + 1, null], point] : [point],
  )
}

export function clockWarning(offset?: number) {
  if (offset == null || !Number.isFinite(offset) || Math.abs(offset) <= 10000) return ''
  return (
    '这台电脑的时钟比监控器' +
    (offset > 0 ? '慢' : '快') +
    ' ' +
    Math.ceil(Math.abs(offset) / 1000) +
    ' 秒，请同步时间'
  )
}
