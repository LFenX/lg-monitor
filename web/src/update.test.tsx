/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { UpdateCard, UpdateLink } from './update'

type Host = { chrome?: unknown; lgmonLauncher?: boolean }

afterEach(() => {
  delete (window as unknown as Host).chrome
  delete (window as unknown as Host).lgmonLauncher
})

function fromWindow(detail: Record<string, unknown>) {
  act(() => {
    window.dispatchEvent(new CustomEvent('lgmon-host', { detail: { type: 'update', ...detail } }))
  })
}

function inWindow() {
  const posted: unknown[] = []
  ;(window as unknown as Host).lgmonLauncher = true
  ;(window as unknown as Host).chrome = {
    webview: { postMessage: (message: string) => posted.push(JSON.parse(message)) },
  }
  return posted
}

it('keeps checking in the footer and shows the card only when there is an update to act on', () => {
  const posted = inWindow()
  render(
    <>
      <div data-testid="top">
        <UpdateCard />
      </div>
      <div data-testid="foot">
        <UpdateLink />
      </div>
    </>,
  )
  // Both parts share one question to the window.
  expect(posted).toEqual([{ type: 'update-check' }])
  const top = screen.getByTestId('top')
  fromWindow({ state: 'dev' })
  expect(top).toBeEmptyDOMElement()
  expect(screen.getByTestId('foot')).toBeEmptyDOMElement()
  fromWindow({ state: 'checking' })
  expect(screen.getByRole('button', { name: '正在检查更新…' })).toBeDisabled()
  expect(top).toBeEmptyDOMElement()
  fromWindow({ state: 'error', during: 'check', message: '连不上发布页' })
  expect(top).toBeEmptyDOMElement()
  fireEvent.click(screen.getByRole('button', { name: '检查更新失败，点击重试' }))
  fromWindow({ state: 'none', current: '0.8.0' })
  expect(top).toBeEmptyDOMElement()
  fireEvent.click(screen.getByRole('button', { name: 'v0.8.0 · 自动检查更新' }))

  fromWindow({ state: 'available', version: '0.9.0', hasPage: true, size: 28_400_000 })
  expect(screen.getByText('发现新版本 0.9.0')).toBeVisible()
  expect(screen.getByText(/约 28.4 MB/)).toBeVisible()
  expect(screen.getByTestId('foot')).toBeEmptyDOMElement()
  fireEvent.click(screen.getByRole('button', { name: '更新内容' }))
  fireEvent.click(screen.getByRole('button', { name: '立即更新' }))
  fromWindow({ state: 'downloading', version: '0.9.0', done: 7_100_000, total: 28_400_000 })
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
  expect(screen.getByText('7.1 / 28.4 MB')).toBeVisible()
  fromWindow({ state: 'error', during: 'download', message: '下载中断了，请重试' })
  expect(screen.getByText('下载中断了，请重试')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '重试' }))
  fromWindow({ state: 'ready', version: '0.9.0' })
  expect(screen.getByText('0.9.0 已下载完成')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '重启并更新' }))
  expect(posted).toEqual([
    { type: 'update-check' },
    { type: 'update-refresh' },
    { type: 'update-refresh' },
    { type: 'update-notes' },
    { type: 'update-download' },
    { type: 'update-download' },
    { type: 'update-apply' },
  ])
})

it('stays out of the way in an ordinary browser', () => {
  const post = vi.fn()
  ;(window as unknown as Host).chrome = { webview: { postMessage: post } }
  const { container } = render(
    <>
      <UpdateCard />
      <UpdateLink />
    </>,
  )
  fromWindow({ state: 'available', version: '0.9.0' })
  expect(container).toBeEmptyDOMElement()
  expect(post).not.toHaveBeenCalled()
})
