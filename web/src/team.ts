import { useCallback, useEffect, useRef, useState } from 'react'
import type { DashboardPanelType } from './dashboard'
import type { Query } from './types'

export type Team = { id: string; name: string; ownerName: string; createdAt: number }
export type Member = {
  id: string
  teamId: string
  name: string
  joinedAt: number
  revokedAt: number
  lastAccess: number
  accessCount: number
}
export type Share = {
  id: string
  teamId: string
  dashboardId: string
  panels?: string[]
  collectors?: string[]
  targets?: string[]
  members?: string[]
  revision: number
  revokedAt: number
}
export type Joined = {
  id: string
  teamId: string
  teamName: string
  ownerName: string
  memberId: string
  address: string
  fingerprint: string
  joinedAt: number
}
export type Shared = {
  id: string
  teamId: string
  teamName: string
  ownerName: string
  dashboardId: string
  title: string
  revision: number
  version: string
  refresh: string
  defaultRange: string
  panels: { id: string; type: DashboardPanelType; title: string; width: number }[]
  collectors: { id: string; name: string }[]
}
export type SharedData = Query & {
  version: string
  revision: number
  params: { from: number; to: number; collector: string | null }
}
export type TeamState = { owned: number; joined: number; enabled: boolean }
export const teamPath = (value: string) => encodeURIComponent(value)

// Remote results are transient: an error or a changed scope immediately clears the old rows.
export function useTeamResource<T>(
  load: (signal: AbortSignal) => Promise<T>,
  dependencies: unknown[],
  interval = 10000,
) {
  const [value, setValue] = useState<T>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  const loader = useRef(load)
  loader.current = load
  const reload = useCallback(() => setRevision((v) => v + 1), [])
  useEffect(() => {
    const controller = new AbortController()
    let active = true,
      busy = false
    setValue(undefined)
    setError('')
    const run = async () => {
      if (busy) return
      busy = true
      setLoading(true)
      try {
        const next = await loader.current(controller.signal)
        if (active) {
          setValue(next)
          setError('')
        }
      } catch (err) {
        if (active && !controller.signal.aborted) {
          setValue(undefined)
          setError(err instanceof Error ? err.message : '团队读取失败，请重试')
        }
      } finally {
        busy = false
        if (active) setLoading(false)
      }
    }
    void run()
    const timer = interval > 0 ? setInterval(() => void run(), interval) : undefined
    return () => {
      active = false
      controller.abort()
      if (timer) clearInterval(timer)
    }
  }, [...dependencies, revision, interval])
  return { value, error, loading, reload }
}

export async function copyTeamText(text: string) {
  if (!navigator.clipboard) throw new Error('复制不可用，请手动选择文本复制')
  await navigator.clipboard.writeText(text)
}
