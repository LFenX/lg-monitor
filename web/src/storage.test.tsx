/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { API } from './api'
import { I18nProvider } from './i18n'
import { StorageSettings } from './StorageSettings'
import { State } from './state'
import { isNewerVersion, WebUpdatePill } from './WebUpdate'
import type { StorageStatus, SystemUpdateState } from './types'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const baseStorage: StorageStatus = {
  home: 'C:\\ProgramData\\lg-monitor',
  defaultHome: 'C:\\ProgramData\\lg-monitor',
  redirected: false,
  dataBytes: 24 * 1024 * 1024,
  dbBytes: 20 * 1024 * 1024,
  configBytes: 2 * 1024 * 1024,
  logsBytes: 1 * 1024 * 1024,
  otherBytes: 1 * 1024 * 1024,
  fileCount: 18,
  currentDrive: {
    root: 'C:\\',
    totalBytes: 256 * 1024 ** 3,
    freeBytes: 42 * 1024 ** 3,
    usedBytes: 214 * 1024 ** 3,
    current: true,
  },
  drives: [
    {
      root: 'C:\\',
      totalBytes: 256 * 1024 ** 3,
      freeBytes: 42 * 1024 ** 3,
      usedBytes: 214 * 1024 ** 3,
      suggestedPath: 'C:\\lgmon-data',
      current: true,
    },
    {
      root: 'D:\\',
      totalBytes: 1024 * 1024 ** 3,
      freeBytes: 720 * 1024 ** 3,
      usedBytes: 304 * 1024 ** 3,
      suggestedPath: 'D:\\lgmon-data',
      current: false,
    },
  ],
}

describe('StorageSettings & WebUpdate', () => {
  it('compares semver strings accurately', () => {
    expect(isNewerVersion('v0.15.0', '0.14.5')).toBe(true)
    expect(isNewerVersion('v0.15.0', 'v0.15.0')).toBe(false)
    expect(isNewerVersion('v0.14.5', '0.15.0')).toBe(false)
    expect(isNewerVersion('v0.15.0', 'dev')).toBe(false)
  })

  it('displays drives, launches migration after confirmation, and shows live progress bar', async () => {
    const api = new API('fake-token')
    let migrated = false
    vi.spyOn(api, 'request').mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === '/storage' && (!init || !init.method || init.method === 'GET')) {
        if (migrated) {
          return {
            ...baseStorage,
            migration: {
              state: 'done',
              source: baseStorage.home,
              target: 'D:\\lgmon-data',
              percent: 100,
              copiedBytes: baseStorage.dataBytes,
              totalBytes: baseStorage.dataBytes,
              copiedFiles: 18,
              totalFiles: 18,
              needsRestart: true,
            },
          }
        }
        return baseStorage
      }
      if (path === '/storage/compact' && init?.method === 'POST') {
        return {
          beforeBytes: baseStorage.dataBytes,
          afterBytes: 4 * 1024 * 1024,
          freedBytes: 20 * 1024 * 1024,
          storage: {
            ...baseStorage,
            dataBytes: 4 * 1024 * 1024,
            dbBytes: 3 * 1024 * 1024,
            logsBytes: 512 * 1024,
          },
        }
      }
      if (path === '/storage/migrate' && init?.method === 'POST') {
        migrated = true
        return {
          ...baseStorage,
          migration: {
            state: 'copying',
            source: baseStorage.home,
            target: 'D:\\lgmon-data',
            percent: 45,
            copiedBytes: 10 * 1024 * 1024,
            totalBytes: baseStorage.dataBytes,
            copiedFiles: 8,
            totalFiles: 18,
            currentFile: 'data/lgmon.db',
          },
        }
      }
      if (path === '/system/restart' && init?.method === 'POST') {
        return { ok: true, mode: 'service', message: '正在重启 lgmon Windows 服务…' }
      }
      return {}
    })

    render(
      <I18nProvider>
        <State api={api} scope="config" theme="light">
          <StorageSettings />
        </State>
      </I18nProvider>,
    )

    // Renders active path and auto-suggests D:\lgmon-data
    await waitFor(() => expect(screen.getByText('C:\\ProgramData\\lg-monitor')).toBeInTheDocument())
    const input = screen.getByLabelText('目标目录路径') as HTMLInputElement
    expect(input.value).toBe('D:\\lgmon-data')

    // Click One-Click Compact Storage and verify freed space banner
    fireEvent.click(screen.getByRole('button', { name: '一键瘦身' }))
    await waitFor(() => expect(screen.getByText(/存储空间瘦身完成/)).toBeInTheDocument())

    // Click Start Migration -> confirm in modal -> verify live progress bar appears
    fireEvent.click(screen.getByRole('button', { name: '开始迁移数据' }))
    await waitFor(() => expect(screen.getByText('确认迁移数据目录')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '确认并开始迁移' }))

    await waitFor(() => expect(screen.getByRole('progressbar')).toBeInTheDocument())
    expect(screen.getByText('45%')).toBeInTheDocument()
    expect(screen.getByText(/data\/lgmon\.db/)).toBeInTheDocument()

    // Polling advances to 100% completion banner with Restart Now button
    await waitFor(() => expect(screen.getByText('数据迁移已完成')).toBeInTheDocument(), { timeout: 2000 })
    expect(screen.getByText('100%')).toBeInTheDocument()
    const restartBtn = screen.getByRole('button', { name: '立即重启 lgmon' })
    fireEvent.click(restartBtn)
    await waitFor(() => expect(screen.getByText(/正在重启 lgmon/)).toBeInTheDocument())
  })

  it('runs the full in-app update flow (available -> download -> ready -> apply) in WebUpdatePill', async () => {
    const api = new API('fake-token')
    let updateState: SystemUpdateState = {
      state: 'available',
      current: '0.16.1',
      version: '0.16.2',
      size: 20_000_000,
    }
    const actions: string[] = []
    vi.spyOn(api, 'request').mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === '/guide') {
        return {
          version: '0.16.1',
          steps: [],
          monitors: [],
          boards: [],
          collectors: [],
          rules: [],
          notifiers: [],
        }
      }
      if (path === '/system/update' && (!init || !init.method || init.method === 'GET')) {
        return updateState
      }
      if (path === '/system/update' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body || '{}')) as { action: string }
        actions.push(body.action)
        if (body.action === 'download') {
          updateState = { ...updateState, state: 'ready', done: 20_000_000, total: 20_000_000 }
        } else if (body.action === 'apply') {
          updateState = { ...updateState, state: 'applying' }
        }
        return updateState
      }
      return {}
    })

    render(
      <I18nProvider>
        <State api={api} scope="config" theme="light">
          <WebUpdatePill />
        </State>
      </I18nProvider>,
    )

    // Shows in-app update button (NOT an <a download> static link)
    const dlBtn = await screen.findByRole('button', { name: /发现新版本 v0\.16\.2/ })
    fireEvent.click(dlBtn)

    // Transitions to ready -> click Restart & Update -> triggers apply
    const applyBtn = await screen.findByRole('button', { name: /重启并更新/ })
    fireEvent.click(applyBtn)

    await waitFor(() => expect(screen.getByText(/正在启动更新并重启 lgmon/)).toBeInTheDocument())
    expect(actions).toEqual(['download', 'apply'])
  })
})
