/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { Beats } from './status'

const hour = 3600 * 1000

it('fills the summary-only hours of a partly raw day instead of leaving them empty', () => {
  render(
    <Beats
      size="day"
      slots={[
        {
          from: 0,
          span: 24 * hour,
          state: 'up',
          uptime: 100,
          segments: [
            { from: 0, to: 12 * hour, state: 'up', summary: true, uptime: 100 },
            { from: 12 * hour, to: 24 * hour, state: 'up' },
          ],
        },
      ]}
    />,
  )
  const day = screen.getByRole('img', { name: '状态历史' }).firstElementChild as HTMLElement
  const segments = Array.from(day.querySelectorAll<HTMLElement>('.lm-beat-segment'))
  const covered = segments.reduce(
    (sum, segment) => sum + parseFloat(segment.style.getPropertyValue('--segment-width')),
    0,
  )
  expect(covered).toBe(100)
  expect(segments[0]).toHaveAttribute('data-summary', 'true')
  expect(segments[0]).toHaveAttribute('data-uptime', '100')
  expect(segments[0].title).toContain('汇总 正常 100%')
  expect(segments[0].title).toContain('未保留精确时点')
  expect(segments[1]).not.toHaveAttribute('data-summary')
  expect(segments[1]).not.toHaveAttribute('data-uptime')
  expect(day.title).toContain('部分时段只有汇总占比')
})

it('shows a summary hour with a failure as its share and an unknown hour as waiting', () => {
  render(
    <Beats
      size="day"
      slots={[
        {
          from: 0,
          span: 24 * hour,
          state: 'down',
          uptime: 99,
          segments: [
            { from: 2 * hour, to: 3 * hour, state: 'down', summary: true, uptime: 94.4 },
            { from: 3 * hour, to: 4 * hour, state: 'unknown', summary: true, uptime: null },
          ],
        },
      ]}
    />,
  )
  const [failed, unknown] = Array.from(
    screen.getByRole('img', { name: '状态历史' }).querySelectorAll<HTMLElement>('.lm-beat-segment'),
  )
  expect(failed.style.getPropertyValue('--segment-up')).toBe('94.4%')
  expect(failed.title).toContain('汇总 正常 94.4%')
  expect(unknown).not.toHaveAttribute('data-uptime')
  expect(unknown.title).toContain('汇总 等待')
})
