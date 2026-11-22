/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

/*
 * A D1Database stand-in over node:sqlite, for the worker tests and the local dev server only.
 * It covers just the calls the worker makes: prepare().bind().first() / all() / run().
 */
import { readFileSync } from 'node:fs'
import { DatabaseSync } from 'node:sqlite'

type Value = string | number | bigint | null | Uint8Array

function toValue(value: unknown): Value {
  if (value === undefined || value === null) return null
  if (typeof value === 'boolean') return value ? 1 : 0
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'bigint') return value
  if (value instanceof Uint8Array) return value
  throw new TypeError('D1 cannot bind ' + typeof value)
}

class Statement {
  db: DatabaseSync
  sql: string
  args: Value[]

  constructor(db: DatabaseSync, sql: string, args: Value[] = []) {
    this.db = db
    this.sql = sql
    this.args = args
  }

  bind(...args: unknown[]): Statement {
    return new Statement(this.db, this.sql, args.map(toValue))
  }

  async first<T>(column?: string): Promise<T | null> {
    const row = this.db.prepare(this.sql).get(...this.args) as Record<string, unknown> | undefined
    if (!row) return null
    return (column ? row[column] : { ...row }) as T
  }

  async all<T>(): Promise<{ results: T[]; success: true; meta: Record<string, never> }> {
    const rows = this.db.prepare(this.sql).all(...this.args) as Record<string, unknown>[]
    return { results: rows.map((row) => ({ ...row }) as T), success: true, meta: {} }
  }

  async run(): Promise<{ success: true; meta: { changes: number; last_row_id: number } }> {
    const result = this.db.prepare(this.sql).run(...this.args)
    return { success: true, meta: { changes: Number(result.changes), last_row_id: Number(result.lastInsertRowid) } }
  }
}

export type LocalD1 = { prepare(sql: string): Statement; close(): void; raw: DatabaseSync }

/* Opens (or creates) a database with the worker's schema applied; D1 always enforces foreign keys. */
export function openLocalD1(path = ':memory:'): LocalD1 {
  const db = new DatabaseSync(path)
  db.exec('PRAGMA foreign_keys = ON')
  db.exec(readFileSync(new URL('../schema.sql', import.meta.url), 'utf8'))
  return {
    prepare: (sql: string) => new Statement(db, sql),
    close: () => db.close(),
    raw: db,
  }
}
