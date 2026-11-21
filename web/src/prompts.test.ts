import { describe, expect, it } from 'vitest'
import { locales } from './locales'
import {
  agentSetup,
  dependencyPrompt,
  docsPrompt,
  editServicesPrompt,
  lanPrompt,
  promptChoices,
  startPrompt,
} from './prompts'

const cjk = /[\u4e00-\u9fff]/

describe('decoupled locales & bilingual prompts', () => {
  it('keeps zh and en locale bundles in strict key parity', () => {
    const zhKeys = Object.keys(locales.zh).sort()
    const enKeys = Object.keys(locales.en).sort()
    expect(enKeys).toEqual(zhKeys)
    for (const key of enKeys) {
      expect(locales.en[key as keyof typeof locales.en].trim().length).toBeGreaterThan(0)
    }
  })

  it('includes docs, edit, and lan scenarios in promptChoices', () => {
    const zhChoices = promptChoices('', true, { id: 'main', name: '我的服务' }, 'zh')
    const ids = zhChoices.map((choice) => choice.id)
    expect(ids).toContain('docs')
    expect(ids).toContain('edit')
    expect(ids).toContain('lan')
    expect(zhChoices.find((c) => c.id === 'docs')?.title).toContain('文档')
  })

  it('renders pure English prompts when lang is en', () => {
    const setup = agentSetup(
      { executable: 'C:\\lgmon\\lgmon.exe', home: 'C:\\lgmon', url: 'http://127.0.0.1:7444' },
      'all',
      'en',
    )
    expect(setup).not.toMatch(cjk)
    const board = { id: 'main', name: 'Production' }
    expect(startPrompt(setup, true, 'en')).not.toMatch(cjk)
    expect(docsPrompt(setup, board, 'en')).not.toMatch(cjk)
    expect(editServicesPrompt(setup, board, 'en')).not.toMatch(cjk)
    expect(lanPrompt(setup, 'en')).not.toMatch(cjk)
    expect(dependencyPrompt(board, setup, 'en')).not.toMatch(cjk)
    const enChoices = promptChoices(setup, true, board, 'en')
    expect(enChoices.length).toBeGreaterThanOrEqual(8)
    for (const choice of enChoices) {
      expect(choice.title).not.toMatch(cjk)
      expect(choice.lead).not.toMatch(cjk)
      expect(choice.text).not.toMatch(cjk)
    }
  })
})
