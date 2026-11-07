import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  AriaComponent,
} from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'
import { useApp } from './state'
import { Empty } from './ui'
import { bytes } from './model'

echarts.use([
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  AriaComponent,
  SVGRenderer,
])
export type ChartSeries = { name: string; data: [number, number | null][] }
export function Chart({
  title,
  series,
  unit = '',
  state = false,
}: {
  title: string
  series: ChartSeries[]
  unit?: string
  state?: boolean
}) {
  const ref = useRef<HTMLDivElement>(null),
    { theme } = useApp()
  const instance = useRef<ReturnType<typeof echarts.init> | null>(null)
  const hasData = series.some((item) => item.data.length > 0)
  useEffect(() => {
    if (!ref.current || !hasData) return
    const chart = echarts.init(ref.current, theme === 'dark' ? 'dark' : undefined, { renderer: 'svg' })
    instance.current = chart
    const observer = new ResizeObserver(() => chart.resize())
    observer.observe(ref.current)
    return () => {
      observer.disconnect()
      chart.dispose()
      instance.current = null
    }
  }, [theme, hasData])
  useEffect(() => {
    const chart = instance.current
    if (!chart) return
    const format = (value: unknown) => {
      if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
      if (state) return ['异常', '未知', '正常'][value] ?? '未知'
      return unit.startsWith('bytes')
        ? bytes(value) + (unit.endsWith('/s') ? '/s' : '')
        : value.toFixed(1) + unit
    }
    chart.setOption(
      {
        backgroundColor: 'transparent',
        textStyle: { fontFamily: "'Microsoft YaHei UI', 'Noto Sans SC', 'Segoe UI', sans-serif" },
        color: ['#3b82f6', '#10b981', '#8b5cf6', '#f59e0b', '#ef4444'],
        animation: false,
        aria: { enabled: true, description: title },
        tooltip: {
          trigger: 'axis',
          renderMode: 'richText',
          valueFormatter: format,
        },
        legend: { bottom: 0, type: 'scroll', icon: 'roundRect', itemWidth: 12, itemHeight: 3 },
        grid: { left: 12, right: 18, top: 18, bottom: 45, containLabel: true },
        xAxis: {
          type: 'time',
          axisLine: { lineStyle: { color: theme === 'dark' ? '#42506a' : '#dbe1ec' } },
          axisLabel: { hideOverlap: true },
          splitLine: { show: false },
        },
        yAxis: {
          type: 'value',
          min: state || unit === '%' ? 0 : undefined,
          max: state ? 2 : unit === '%' ? 100 : undefined,
          minInterval: state ? 1 : undefined,
          axisLabel: {
            formatter: format,
          },
          splitLine: { lineStyle: { color: theme === 'dark' ? '#293449' : '#edf0f5', type: 'dashed' } },
        },
        dataZoom: [{ type: 'inside', filterMode: 'none' }],
        series: series.map((item) => ({
          ...item,
          id: item.name,
          type: 'line',
          step: state ? 'end' : false,
          smooth: !state,
          showSymbol: item.data.length < 3,
          connectNulls: false,
          lineStyle: { width: 2 },
          areaStyle: { opacity: state ? 0.1 : 0.035 },
        })),
      },
      { replaceMerge: ['series'] },
    )
  }, [series, theme, hasData, title, unit, state])
  return hasData ? (
    <div ref={ref} className="chart" role="img" aria-label={title} />
  ) : (
    <Empty title="这段时间暂无数据">采集到数据后，曲线会自动更新。</Empty>
  )
}
