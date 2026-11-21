import { en } from './en'
import { zh } from './zh'
import type { MessageKey } from './zh'

export type { MessageKey }

export const locales = {
  zh,
  en,
} as const

export type Lang = keyof typeof locales

export const defaultLang: Lang = 'zh'

export const langOptions: ReadonlyArray<{ id: Lang; label: string }> = [
  { id: 'zh', label: '中文' },
  { id: 'en', label: 'English' },
]

export function isLang(value: unknown): value is Lang {
  return typeof value === 'string' && value in locales
}

export function formatMessage(template: string, params?: Record<string, string | number>): string {
  if (!params) return template
  return template.replace(/\{(\w+)\}/g, (_, key: string) =>
    key in params ? String(params[key]) : `{${key}}`,
  )
}
