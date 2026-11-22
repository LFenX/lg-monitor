/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useState } from 'react'
import { APIError } from './api'
import type { API } from './api'
import { keepsSnapshot, useApp } from './state'
import type { LANOutgoing, LANRemoteBoard } from './types'

export type RemoteCopy = { board?: LANRemoteBoard; gone: string; error: string }
export type RemoteSummaryEntry = { board?: LANRemoteBoard; offline: boolean }

const remoteKey = (id: string) => 'lgmon-remote-' + id

const listeners = new Set<() => void>()
const offlineByAPI = new WeakMap<API, Set<string>>()
const memoryByAPI = new WeakMap<API, Map<string, LANRemoteBoard>>()

function notify() {
  for (const listener of listeners) listener()
}

function setMemory(api: API, board: LANRemoteBoard) {
  let map = memoryByAPI.get(api)
  if (!map) {
    map = new Map()
    memoryByAPI.set(api, map)
  }
  map.set(board.id, board)
  notify()
}

function deleteMemory(api: API, id: string) {
  if (memoryByAPI.get(api)?.delete(id)) notify()
}

function readMemoryOrDisk(api: API, id: string): LANRemoteBoard | undefined {
  return memoryByAPI.get(api)?.get(id) ?? readRemote(id)
}

function setOffline(api: API, id: string, offline: boolean) {
  let set = offlineByAPI.get(api)
  if (offline) {
    if (!set) {
      set = new Set()
      offlineByAPI.set(api, set)
    }
    if (!set.has(id)) {
      set.add(id)
      notify()
    }
  } else if (set?.delete(id)) {
    notify()
  }
}

function isOffline(api: API, id: string): boolean {
  return offlineByAPI.get(api)?.has(id) || false
}

export function readRemote(id: string): LANRemoteBoard | undefined {
  if (!keepsSnapshot()) return undefined
  try {
    const value = JSON.parse(localStorage.getItem(remoteKey(id)) || 'null') as LANRemoteBoard | null
    return value?.board ? value : undefined
  } catch {
    return undefined
  }
}

export function writeRemote(board: LANRemoteBoard) {
  if (!keepsSnapshot()) return
  try {
    localStorage.setItem(remoteKey(board.id), JSON.stringify(board))
  } catch {
    // Without storage the page still reads the owner each time.
  }
  notify()
}

export function forgetRemote(id: string) {
  try {
    localStorage.removeItem(remoteKey(id))
  } catch {
    // Nothing kept, nothing to forget.
  }
  notify()
}

/* Ticks every few seconds while a shared board is open, so updates on the owner's computer show up soon. */
export function useTick(ms: number) {
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), ms)
    return () => clearInterval(timer)
  }, [ms])
  return tick
}

/*
 * Someone else's board, read through this computer and kept between reads. A refusal is final and drops the copy;
 * anything else (the owner busy, a hiccup on the network) keeps the last copy and only shows how old it is.
 */
export function useRemoteBoard(id: string) {
  const { api, pulse } = useApp()
  const tick = useTick(5000)
  // The copy from the last visit shows at once, with its age, while the owner is asked again.
  const [copy, setCopy] = useState<RemoteCopy>(() => ({
    board: readMemoryOrDisk(api, id),
    gone: '',
    error: '',
  }))
  useEffect(() => {
    const controller = new AbortController()
    api
      .request<LANRemoteBoard>('/lan/remote/' + encodeURIComponent(id), { signal: controller.signal })
      .then((board) => {
        setOffline(api, id, false)
        setMemory(api, board)
        writeRemote(board)
        setCopy({ board, gone: '', error: '' })
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        if (err instanceof APIError && (err.status === 403 || err.status === 404)) {
          setOffline(api, id, false)
          deleteMemory(api, id)
          forgetRemote(id)
          setCopy({ gone: err.message, error: '' })
        } else {
          setOffline(api, id, true)
          setCopy((previous) => ({ ...previous, error: err instanceof Error ? err.message : '读取失败' }))
        }
      })
    return () => controller.abort()
  }, [api, id, pulse, tick])
  return copy
}

/* Live cached summaries for a list of shared boards, updated whenever prefetch or an open view hears from a peer. */
export function useRemoteSummaries(ids: string[]): Map<string, RemoteSummaryEntry> {
  const { api } = useApp()
  const key = ids.join(',')
  const [, setVersion] = useState(0)
  useEffect(() => {
    const bump = () => setVersion((v) => v + 1)
    listeners.add(bump)
    return () => {
      listeners.delete(bump)
    }
  }, [])
  const map = new Map<string, RemoteSummaryEntry>()
  if (!key) return map
  for (const id of key.split(',')) {
    map.set(id, { board: readMemoryOrDisk(api, id), offline: isOffline(api, id) })
  }
  return map
}

/*
 * Keeps every shared board's summary warm in the background so the sidebar shows real health dots and counts
 * without making the user click each colleague first.
 */
export function usePrefetchRemote(items: LANOutgoing[]) {
  const { api, now, pulse } = useApp()
  const tick = useTick(30_000)
  const ids = items.map((item) => item.id).join(',')
  useEffect(() => {
    if (!ids) return
    const controller = new AbortController()
    const list = ids.split(',').slice(0, 16)
    const minAge = pulse > 0 && tick === 0 ? 4_000 : 25_000
    const timer = setTimeout(() => {
      let cursor = 0
      const worker = async () => {
        while (!controller.signal.aborted) {
          const index = cursor++
          if (index >= list.length) return
          const id = list[index]
          const kept = readMemoryOrDisk(api, id)
          if (kept?.fetchedAt && now() - kept.fetchedAt < minAge && !isOffline(api, id)) continue
          try {
            const board = await api.request<LANRemoteBoard>('/lan/remote/' + encodeURIComponent(id), {
              signal: controller.signal,
            })
            if (controller.signal.aborted) return
            setOffline(api, id, false)
            setMemory(api, board)
            writeRemote(board)
          } catch (err: unknown) {
            if (controller.signal.aborted) return
            if (err instanceof APIError && (err.status === 403 || err.status === 404)) {
              setOffline(api, id, false)
              deleteMemory(api, id)
              forgetRemote(id)
            } else {
              setOffline(api, id, true)
            }
          }
        }
      }
      const workers = Math.min(4, list.length)
      for (let i = 0; i < workers; i++) void worker()
    }, 250)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [api, ids, now, pulse, tick])
}
