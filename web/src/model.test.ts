import { describe, expect, it } from 'vitest'
import { bytes, clockWarning, insertGaps, percent, period, series, targetLink, title } from './model'

describe('monitoring values', () => {
  it('labels global event rules and links them to their host', () => {
    expect(title([], '@eventlog')).toBe('事件日志')
    expect(targetLink('fake-host', '@eventlog')).toBe('#/hosts/fake-host')
  })
  it('only warns beyond ten seconds and distinguishes fast from slow clocks', () => {
    for (const offset of [undefined, 0, 10000, -10000, Number.NaN]) expect(clockWarning(offset)).toBe('')
    expect(clockWarning(40000)).toBe('这台电脑的时钟比监控器慢 40 秒，请同步时间')
    expect(clockWarning(-300000)).toBe('这台电脑的时钟比监控器快 300 秒，请同步时间')
  })
  it('distinguishes observed zero from unknown values', () => {
    expect(percent(0)).toBe('0.0%')
    expect(percent(undefined)).toBe('暂无数据')
    expect(bytes(0)).toBe('0 B')
    expect(bytes(undefined)).toBe('暂无数据')
  })
  it('keeps missing samples disconnected and chronological', () => {
    expect(
      insertGaps(
        [
          [1000, 1],
          [50000, 2],
        ],
        30000,
      ),
    ).toEqual([
      [1000, 1],
      [1001, null],
      [50000, 2],
    ])
    expect(
      series([
        { collectorId: 'fake', name: 'cpu', ts: 2000, average: 2, max: 2, samples: 1 },
        { collectorId: 'fake', name: 'cpu', ts: 1000, average: 1, max: 1, samples: 1 },
      ])[0].data,
    ).toEqual([
      [1000, 1],
      [2000, 2],
    ])
  })
  it('uses coarse history for week ranges and URL-encodes identities', () => {
    expect(period(168, 1000000000).step).toBe('1h')
    expect(period(24, 1000000000).step).toBe('1m')
    expect(targetLink('fake host', 'a/b')).toBe('#/hosts/fake%20host/targets/a%2Fb')
    expect(targetLink('fake host', '')).toBe('#/hosts/fake%20host')
    expect(title([], '')).toBe('主机级告警')
  })
})
