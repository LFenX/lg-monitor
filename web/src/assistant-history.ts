/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

/*
 * 波西米亚's last few conversations, kept in this browser on this computer so one can be picked up again later.
 * Each window keeps working on its own conversation; all of them land here, newest first, five at most.
 */

export type SavedChat<T> = { id: string; title: string; at: number; turns: T[] }

const key = 'lgmon-assistant-chats'
export const keptChats = 5
// A long conversation keeps its latest questions; the oldest ones would only crowd the browser's storage.
const keptTurns = 30

function valid<T>(value: unknown): value is SavedChat<T> {
  const chat = value as SavedChat<T>
  return (
    !!chat &&
    typeof chat.id === 'string' &&
    typeof chat.title === 'string' &&
    typeof chat.at === 'number' &&
    Array.isArray(chat.turns)
  )
}

export function readChats<T>(): SavedChat<T>[] {
  try {
    const value = JSON.parse(localStorage.getItem(key) || '[]')
    return Array.isArray(value) ? value.filter(valid<T>).slice(0, keptChats) : []
  } catch {
    return []
  }
}

function write<T>(chats: SavedChat<T>[]) {
  for (let count = chats.length; count >= 0; count--) {
    try {
      localStorage.setItem(key, JSON.stringify(chats.slice(0, count)))
      return chats.slice(0, count)
    } catch {
      // Storage is full: keep fewer conversations rather than none.
    }
  }
  return []
}

/* Saves a conversation as the newest one; an empty one is not kept. */
export function keepChat<T extends { question: string; at: number }>(id: string, turns: T[]): SavedChat<T>[] {
  const others = readChats<T>().filter((chat) => chat.id !== id)
  if (!turns.length) return write(others)
  const title = turns[0].question.replace(/\s+/g, ' ').trim().slice(0, 60)
  const at = turns[turns.length - 1].at || Date.now()
  return write([{ id, title, at, turns: turns.slice(-keptTurns) }, ...others].slice(0, keptChats))
}

export function dropChat<T>(id: string): SavedChat<T>[] {
  return write(readChats<T>().filter((chat) => chat.id !== id))
}

export function newChatID() {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}
