/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useState } from 'react'
import { APIError } from './api'
import { keepsSnapshot, useApp } from './state'
import type { LANOutgoing, LANRemoteBoard } from './types'

export type RemoteCopy = { board?: LANRemoteBoard; gone: string; error: string }

const remoteKey = (id: string) => 'lgmon-remote-' + id

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
}

export function forgetRemote(id: string) {
  try {
    localStorage.removeItem(remoteKey(id))
  } catch {
    // Nothing kept, nothing to forget.
  }
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
  const [copy, setCopy] = useState<RemoteCopy>(() => ({ board: readRemote(id), gone: '', error: '' }))
  useEffect(() => {
    const controller = new AbortController()
    api
      .request<LANRemoteBoard>('/lan/remote/' + encodeURIComponent(id), { signal: controller.signal })
      .then((board) => {
        writeRemote(board)
        setCopy({ board, gone: '', error: '' })
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        if (err instanceof APIError && (err.status === 403 || err.status === 404)) {
          forgetRemote(id)
          setCopy({ gone: err.message, error: '' })
        } else
          setCopy((previous) => ({ ...previous, error: err instanceof Error ? err.message : '读取失败' }))
      })
    return () => controller.abort()
  }, [api, id, pulse, tick])
  return copy
}

/*
 * Reads every shared board once in the background soon after lgmon opens, so the first click on one shows a copy
 * seconds old instead of waiting for the owner's computer.
 */
export function usePrefetchRemote(items: LANOutgoing[]) {
  const { api, now } = useApp()
  const ids = items.map((item) => item.id).join(',')
  useEffect(() => {
    if (!ids || !keepsSnapshot()) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      for (const id of ids.split(',').slice(0, 8)) {
        const kept = readRemote(id)
        if (kept?.fetchedAt && now() - kept.fetchedAt < 60_000) continue
        api
          .request<LANRemoteBoard>('/lan/remote/' + encodeURIComponent(id), { signal: controller.signal })
          .then(writeRemote)
          .catch(() => undefined)
      }
    }, 1500)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [api, ids, now])
}
