/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import { useApp } from './state'
import type { Monitor } from './types'

/*
 * Inside someone else's shared board, every page shows what its owner sees, read-only: service links stay
 * within that board's pages here, and lookups by service ID use the board's own services.
 */
export type Shared = {
  /* This computer's record of the approval, which names the board in local URLs. */
  id: string
  boardName: string
  ownerName: string
  ownerAvatar?: string
  monitors: Monitor[]
}

const SharedContext = createContext<Shared | undefined>(undefined)

export function SharedScope({ value, children }: { value: Shared; children: ReactNode }) {
  return <SharedContext.Provider value={value}>{children}</SharedContext.Provider>
}

export function useShared() {
  return useContext(SharedContext)
}

export function sharedHref(shared: string, monitor?: string) {
  return (
    '#/lan/remote/' + encodeURIComponent(shared) + (monitor ? '/monitors/' + encodeURIComponent(monitor) : '')
  )
}

/* Where a service link goes: its own page here, or its read-only page in the shared board being viewed. */
export function useMonitorHref() {
  const shared = useShared()
  return (id: string) => (shared ? sharedHref(shared.id, id) : '#/monitors/' + encodeURIComponent(id))
}

/* The services a page can name: this computer's, or the shared board's. */
export function useMonitors() {
  const { monitors } = useApp()
  return useShared()?.monitors || monitors
}
