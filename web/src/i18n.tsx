import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { API } from './api'
import { defaultLang, formatMessage, isLang, locales } from './locales'
import type { Lang, MessageKey } from './locales'

export type { Lang, MessageKey }
export { langOptions } from './locales'

const langKey = 'lgmon-lang'
const langEvent = 'lgmon:lang'

function initialLang(): Lang {
  try {
    const saved = localStorage.getItem(langKey)
    if (isLang(saved)) return saved
  } catch {
    // Ignore private-mode storage errors.
  }
  return defaultLang
}

function saveLocalLang(next: Lang) {
  try {
    localStorage.setItem(langKey, next)
  } catch {
    // Ignore storage errors.
  }
}

/* Broadcast a language update received from SSE so every mounted provider adopts it without circular imports. */
export function notifyServerLang(next: unknown) {
  if (!isLang(next)) return
  saveLocalLang(next)
  window.dispatchEvent(new CustomEvent<Lang>(langEvent, { detail: next }))
}

type LangSaver = (next: Lang) => void

type I18nValue = {
  lang: Lang
  setLang: (next: Lang) => void
  t: (key: MessageKey, params?: Record<string, string | number>) => string
  registerSaver: (saver: LangSaver | null) => void
  applyRemoteLang: (next: Lang) => void
}

const I18nContext = createContext<I18nValue>({
  lang: defaultLang,
  setLang: () => {},
  t: (key, params) => formatMessage(locales[defaultLang][key], params),
  registerSaver: () => {},
  applyRemoteLang: () => {},
})

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(initialLang)
  const saverRef = useRef<LangSaver | null>(null)

  const registerSaver = useCallback((saver: LangSaver | null) => {
    saverRef.current = saver
  }, [])

  const applyRemoteLang = useCallback((next: Lang) => {
    saveLocalLang(next)
    setLangState((current) => (current === next ? current : next))
  }, [])

  const setLang = useCallback((next: Lang) => {
    saveLocalLang(next)
    setLangState(next)
    saverRef.current?.(next)
  }, [])

  useEffect(() => {
    document.documentElement.lang = lang === 'en' ? 'en' : 'zh-CN'
  }, [lang])

  // Keep multiple browser tabs and the launcher window in sync via both storage events and SSE notifications.
  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key === langKey && isLang(event.newValue)) {
        setLangState(event.newValue)
      }
    }
    const onServerEvent = (event: Event) => {
      const detail = (event as CustomEvent<unknown>).detail
      if (isLang(detail)) {
        setLangState(detail)
      }
    }
    window.addEventListener('storage', onStorage)
    window.addEventListener(langEvent, onServerEvent)
    return () => {
      window.removeEventListener('storage', onStorage)
      window.removeEventListener(langEvent, onServerEvent)
    }
  }, [])

  const value = useMemo<I18nValue>(
    () => ({
      lang,
      setLang,
      t: (key, params) => formatMessage(locales[lang][key], params),
      registerSaver,
      applyRemoteLang,
    }),
    [lang, setLang, registerSaver, applyRemoteLang],
  )
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  return useContext(I18nContext)
}

/*
 * Bridges I18nProvider with the local lgmon backend so switching language in either the startup window
 * or any browser tab persists and immediately syncs to every other open window.
 */
export function useLangSync(api: API, guest: boolean) {
  const { registerSaver, applyRemoteLang } = useI18n()
  useEffect(() => {
    if (guest) {
      registerSaver(null)
      return
    }
    registerSaver((next) => {
      void api.request('/ui/lang', { method: 'PUT', body: JSON.stringify({ lang: next }) }).catch(() => {})
    })
    return () => registerSaver(null)
  }, [api, guest, registerSaver])

  useEffect(() => {
    if (guest) return
    const controller = new AbortController()
    api
      .request<{ lang?: string }>('/ui/lang', { signal: controller.signal })
      .then((res) => {
        if (!controller.signal.aborted && isLang(res?.lang)) {
          applyRemoteLang(res.lang)
        }
      })
      .catch(() => {})
    return () => controller.abort()
  }, [api, guest, applyRemoteLang])
}
