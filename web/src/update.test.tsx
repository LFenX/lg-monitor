import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { UpdateCard } from './update'

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

it('offers a newer lgmon only inside its window, then downloads it and restarts into it', () => {
  const posted: unknown[] = []
  ;(window as unknown as Host).lgmonLauncher = true
  ;(window as unknown as Host).chrome = {
    webview: { postMessage: (message: string) => posted.push(JSON.parse(message)) },
  }
  const { container } = render(<UpdateCard />)
  expect(posted).toEqual([{ type: 'update-check' }])
  fromWindow({ state: 'dev' })
  expect(container).toBeEmptyDOMElement()
  fromWindow({ state: 'checking' })
  expect(screen.getByText('正在检查更新…')).toBeVisible()
  fromWindow({ state: 'error', during: 'check', message: '连不上发布页' })
  expect(screen.getByText('连不上发布页')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '重试检查' }))
  fromWindow({ state: 'none', current: '0.8.0' })
  expect(screen.getByText('已是最新版本')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '检查更新' }))
  fromWindow({ state: 'available', version: '0.9.0', hasPage: true, size: 28_400_000 })
  expect(screen.getByText('lgmon 0.9.0 可以更新了')).toBeVisible()
  expect(screen.getByText(/约 28.4 MB/)).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '更新内容' }))
  fireEvent.click(screen.getByRole('button', { name: '下载更新' }))
  fromWindow({ state: 'downloading', version: '0.9.0', done: 7_100_000, total: 28_400_000 })
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
  expect(screen.getByText('7.1 / 28.4 MB')).toBeVisible()
  fromWindow({ state: 'error', during: 'download', message: '下载中断了，请重试' })
  expect(screen.getByText('下载中断了，请重试')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '重试' }))
  fromWindow({ state: 'ready', version: '0.9.0' })
  expect(screen.getByText('0.9.0 已经下载好')).toBeVisible()
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
  const { container } = render(<UpdateCard />)
  fromWindow({ state: 'available', version: '0.9.0' })
  expect(container).toBeEmptyDOMElement()
  expect(post).not.toHaveBeenCalled()
})
