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
  /* Optional overrides for cloud-synced personal devices; omit for LAN shared boards. */
  apiPrefix?: string
  boardHref?: string
  monitorHref?: (monitorId: string) => string
  badgeLabel?: string
  badgeTitle?: string
  /* Cloud-synced devices also carry batch job daily records; LAN shared boards do not. */
  supportsJobs?: boolean
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

export function sharedBoardHref(shared: Shared) {
  return shared.boardHref || sharedHref(shared.id)
}

export function sharedApiPrefix(shared: Shared) {
  return shared.apiPrefix || '/lan/remote/' + encodeURIComponent(shared.id)
}

/* Where a service link goes: its own page here, or its read-only page in the shared/cloud board being viewed. */
export function useMonitorHref() {
  const shared = useShared()
  return (id: string) => {
    if (!shared) return '#/monitors/' + encodeURIComponent(id)
    return shared.monitorHref ? shared.monitorHref(id) : sharedHref(shared.id, id)
  }
}

/* The services a page can name: this computer's, or the shared board's. */
export function useMonitors() {
  const { monitors } = useApp()
  return useShared()?.monitors || monitors
}
