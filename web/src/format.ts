/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { bytes } from './model'

export type Unit = 'percent' | 'bytes' | 'bytes_per_sec' | 'ms' | 'seconds' | 'count' | '' | undefined

function trim(value: number, digits: number) {
  return Number(value.toFixed(digits)).toLocaleString('zh-CN', { maximumFractionDigits: digits })
}

function span(seconds: number) {
  if (seconds < 60) return [trim(seconds, 0), '秒']
  if (seconds < 3600) return [trim(seconds / 60, 0), '分钟']
  if (seconds < 86400) return [trim(seconds / 3600, 1), '小时']
  return [trim(seconds / 86400, 1), '天']
}

/* Splits a reading into the figure and its unit, so the unit can be set smaller. */
export function parts(value: unknown, unit: Unit): [string, string] {
  if (value == null) return ['—', '']
  if (typeof value !== 'number' || !Number.isFinite(value)) return [String(value), '']
  switch (unit) {
    case 'percent':
      return [trim(value, 1), '%']
    case 'bytes':
    case 'bytes_per_sec': {
      const [figure, suffix] = bytes(value).split(' ')
      return [figure, suffix + (unit === 'bytes_per_sec' ? '/s' : '')]
    }
    case 'ms':
      return value >= 1000 ? [trim(value / 1000, 2), '秒'] : [trim(value, value < 10 ? 1 : 0), 'ms']
    case 'seconds': {
      const [figure, suffix] = span(value)
      return [figure, suffix]
    }
    case 'count':
      return [trim(value, 0), '']
    default:
      return [trim(value, 2), '']
  }
}

export function display(value: unknown, unit: Unit) {
  const [figure, suffix] = parts(value, unit)
  return suffix ? (suffix === '%' || suffix === 'ms' ? figure + suffix : figure + ' ' + suffix) : figure
}

export function level(value: unknown, warn?: number | null, critical?: number | null) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return undefined
  if (critical != null && value >= critical) return 'critical'
  if (warn != null && value >= warn) return 'warn'
  return undefined
}

export function chartUnit(unit: Unit) {
  return unit === 'percent'
    ? '%'
    : unit === 'bytes'
      ? 'bytes'
      : unit === 'bytes_per_sec'
        ? 'bytes/s'
        : unit === 'ms'
          ? 'ms'
          : ''
}
