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

it('renders a recovered 90s outage only at its real interval and shows green tail after recovery', () => {
  const slotFrom = Date.parse('2026-11-24T20:15:00+09:00')
  const downFrom = Date.parse('2026-11-24T20:21:20+09:00')
  const downTo = Date.parse('2026-11-24T20:22:50+09:00')
  const slotTo = Date.parse('2026-11-24T20:30:00+09:00')
  const span = 15 * 60 * 1000
  const { rerender } = render(
    <Beats
      size="large"
      liveState="down"
      slots={[
        {
          from: slotFrom,
          span,
          state: 'down',
          uptime: 90,
          segments: [
            { from: slotFrom, to: downFrom, state: 'up' },
            { from: downFrom, to: downTo, state: 'down' },
            { from: downTo, to: slotTo, state: 'up' },
          ],
        },
      ]}
    />,
  )
  const slotEl = screen.getByRole('img', { name: '状态历史' }).firstElementChild as HTMLElement
  expect(slotEl).toHaveAttribute('data-live', 'true')

  // Once recovered, data-live disappears and the confirmed 90s red segment stays surrounded by green.
  rerender(
    <Beats
      size="large"
      liveState="up"
      slots={[
        {
          from: slotFrom,
          span,
          state: 'down',
          uptime: 90,
          segments: [
            { from: slotFrom, to: downFrom, state: 'up' },
            { from: downFrom, to: downTo, state: 'down' },
            { from: downTo, to: slotTo, state: 'up' },
          ],
        },
      ]}
    />,
  )
  expect(slotEl).not.toHaveAttribute('data-live')
  const segments = Array.from(slotEl.querySelectorAll<HTMLElement>('.lm-beat-segment'))
  expect(segments).toHaveLength(3)
  expect(segments[0]).toHaveAttribute('data-state', 'up')
  expect(segments[1]).toHaveAttribute('data-state', 'down')
  expect(parseFloat(segments[1].style.getPropertyValue('--segment-width'))).toBeCloseTo(10, 2)
  expect(segments[1].title).toContain('90 秒')
  expect(segments[2]).toHaveAttribute('data-state', 'up')
})

it('labels legacy half-hour summary down slots without fabricating 0% uptime', () => {
  const from = Date.parse('2026-11-24T20:00:00+09:00')
  const span = 30 * 60 * 1000
  render(
    <Beats
      size="row"
      liveState="up"
      slots={[
        {
          from,
          span,
          state: 'down',
          uptime: null,
          segments: [{ from, to: from + span, state: 'down', summary: true }],
        },
      ]}
    />,
  )
  const slotEl = screen.getByRole('img', { name: '状态历史' }).firstElementChild as HTMLElement
  expect(slotEl).not.toHaveAttribute('data-uptime')
  expect(slotEl.title).toContain('该时段曾有异常')
  expect(slotEl.title).not.toContain('0%')
  const seg = slotEl.querySelector<HTMLElement>('.lm-beat-segment')!
  expect(seg).toHaveAttribute('data-summary', 'true')
  expect(seg).not.toHaveAttribute('data-uptime')
  expect(seg.title).toContain('该时段曾有异常')
  expect(seg.title).not.toContain('0%')
})
