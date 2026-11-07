import { expect, it } from 'vitest'
import { ruleTemplatePatch } from './templates'

it('updates only the chosen rule template and preserves every other current configuration field', () => {
  const rules = [
    {
      id: 'fake-sql',
      type: 'sql',
      query: 'SELECT 1 AS value',
      interval: '1m',
      notify: ['fake-channel'],
      template: 'standard',
    },
    { id: 'fake-process', target: 'fake-target', renotify: '5m', threshold: 0 },
  ]
  const before = structuredClone(rules)
  const changed = ruleTemplatePatch(rules, 'fake-sql', 'compact')
  expect(changed[0]).toEqual({ ...before[0], template: 'compact' })
  expect(changed[1]).toEqual(before[1])
  expect(rules).toEqual(before)
  expect(() => ruleTemplatePatch(rules, 'removed-rule', 'standard')).toThrow('规则已不存在')
})
