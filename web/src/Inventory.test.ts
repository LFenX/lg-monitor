import { expect, it } from 'vitest'
import { taskResult } from './Inventory'

it('keeps Scheduler status codes distinct from success and never-run tasks', () => {
  const task = { enabled: true, state: 'ready', lastRun: 1, nextRun: 0, lastResult: 0, missedRuns: 0 }
  expect(taskResult(task)).toBe('成功 · 0x00000000')
  expect(taskResult({ ...task, lastRun: 0 })).toBe('从未运行')
  expect(taskResult({ ...task, state: 'running', lastResult: 0x41301 })).toBe('0x00041301')
  expect(taskResult({ ...task, lastResult: 0x80070005 })).toBe('0x80070005')
})
