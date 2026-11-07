import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, AriaComponent } from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'
import { useApp } from './state'
echarts.use([BarChart, GridComponent, TooltipComponent, AriaComponent, SVGRenderer])

export function DashboardBar({
  title,
  labels,
  values,
}: {
  title: string
  labels: string[]
  values: (number | null)[]
}) {
  const { theme } = useApp()
  const root = useRef<HTMLDivElement>(null),
    instance = useRef<ReturnType<typeof echarts.init> | null>(null)
  useEffect(() => {
    if (!root.current) return
    const chart = echarts.init(root.current, theme === 'dark' ? 'dark' : undefined, { renderer: 'svg' })
    instance.current = chart
    const observer = new ResizeObserver(() => chart.resize())
    observer.observe(root.current)
    return () => {
      observer.disconnect()
      chart.dispose()
      instance.current = null
    }
  }, [theme])
  useEffect(() => {
    instance.current?.setOption({
      animation: false,
      backgroundColor: 'transparent',
      aria: { enabled: true, description: title },
      tooltip: { trigger: 'axis', renderMode: 'richText' },
      grid: { left: 18, right: 18, top: 20, bottom: 20, containLabel: true },
      xAxis: { type: 'category', data: labels, axisLabel: { hideOverlap: true } },
      yAxis: {
        type: 'value',
        splitLine: { lineStyle: { color: theme === 'dark' ? '#293449' : '#edf0f5', type: 'dashed' } },
      },
      series: [
        {
          type: 'bar',
          data: values,
          itemStyle: { color: '#3b82f6', borderRadius: [4, 4, 0, 0] },
          barMaxWidth: 48,
        },
      ],
    })
  }, [title, labels, values, theme])
  return <div ref={root} className="chart" role="img" aria-label={title} />
}
