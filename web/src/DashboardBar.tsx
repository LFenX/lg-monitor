import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, AriaComponent } from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'
import { tokens } from './Chart'
import { display } from './format'
import type { Unit } from './format'
import { useApp } from './state'
echarts.use([BarChart, GridComponent, TooltipComponent, AriaComponent, SVGRenderer])

export function DashboardBar({
  title,
  labels,
  values,
  unit,
}: {
  title: string
  labels: string[]
  values: (number | null)[]
  unit?: Unit
}) {
  const { theme } = useApp()
  const root = useRef<HTMLDivElement>(null),
    instance = useRef<ReturnType<typeof echarts.init> | null>(null)
  useEffect(() => {
    if (!root.current) return
    const chart = echarts.init(root.current, undefined, { renderer: 'svg' })
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
    const colors = tokens(root.current)
    const format = (value: unknown) => display(value, unit)
    instance.current?.setOption({
      animationDuration: 400,
      backgroundColor: 'transparent',
      aria: { enabled: true, description: title },
      textStyle: { color: colors.muted },
      tooltip: {
        trigger: 'axis',
        renderMode: 'richText',
        valueFormatter: format,
        backgroundColor: colors.surface,
        borderColor: colors.border,
        textStyle: { color: colors.text },
      },
      grid: { left: 6, right: 14, top: 16, bottom: 6, containLabel: true },
      xAxis: {
        type: 'category',
        data: labels,
        axisLabel: { hideOverlap: true, color: colors.muted },
        axisLine: { lineStyle: { color: colors.border } },
        axisTick: { show: false },
      },
      yAxis: {
        type: 'value',
        axisLabel: { formatter: format, color: colors.muted },
        splitLine: { lineStyle: { color: colors.border, type: 'dashed' } },
      },
      series: [
        {
          type: 'bar',
          data: values,
          itemStyle: {
            borderRadius: [6, 6, 0, 0],
            color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
              { offset: 0, color: colors.series[0] },
              { offset: 1, color: colors.series[1] },
            ]),
          },
          barMaxWidth: 44,
        },
      ],
    })
  }, [title, labels, values, theme, unit])
  return <div ref={root} className="chart" role="img" aria-label={title} />
}
