/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'

let script: HTMLScriptElement
let authorizations: string[]

beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  window.location.hash = '#/'
  script = document.createElement('script')
  script.id = 'lgmon-session'
  script.type = 'application/json'
  script.textContent = JSON.stringify({ token: 'fake-old-local', scope: 'read' })
  document.head.append(script)
  authorizations = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      // lgmon restarted: the page it serves now carries a new session.
      if (path === '/' || path === location.pathname)
        return new Response(
          '<!doctype html><script id="lgmon-session" type="application/json">{"token":"fake-new-local","scope":"read"}</script>',
          { headers: { 'Content-Type': 'text/html' } },
        )
      const authorization = String((init?.headers as Record<string, string>)?.Authorization)
      authorizations.push(authorization)
      if (authorization.includes('fake-old-local'))
        return Response.json(
          { error: { code: 'unauthorized', message: '请提供有效的访问令牌' } },
          { status: 401 },
        )
      const headers = { 'X-Lgmon-Scope': 'read' }
      if (path.endsWith('/stream'))
        return new Response(new ReadableStream(), {
          headers: { ...headers, 'Content-Type': 'text/event-stream' },
        })
      if (path.endsWith('/config/targets')) return Response.json({ targets: [] }, { headers })
      if (path.endsWith('/config/notifiers'))
        return Response.json({ feishuApps: [], notifiers: [] }, { headers })
      if (path.endsWith('/notifications/pause')) return Response.json({ paused: false }, { headers })
      if (path.endsWith('/query'))
        return Response.json({ columns: [], rows: [], truncated: false }, { headers })
      if (
        /\/(collectors|targets|alerts|rule-errors|monitors|boards|dashboards|agents|lan\/remote)$/.test(path)
      )
        return Response.json([], { headers })
      return Response.json({}, { headers })
    }),
  )
})
afterEach(() => {
  script.remove()
  vi.unstubAllGlobals()
})

it('picks up the new session lgmon put in its page instead of asking for a token after a restart', async () => {
  render(<App />)
  await waitFor(() => expect(authorizations.some((value) => value.includes('fake-new-local'))).toBe(true))
  await waitFor(() => expect(screen.queryByText('正在读取监控数据…')).not.toBeInTheDocument())
  expect(screen.queryByText('访问令牌已失效，请重新连接监控器。')).not.toBeInTheDocument()
  expect(screen.queryByPlaceholderText('粘贴令牌')).not.toBeInTheDocument()
})

it('still asks for a token when the page has no new session to offer', async () => {
  script.textContent = JSON.stringify({ token: 'fake-old-local', scope: 'read' })
  vi.mocked(fetch).mockImplementation(async (input: RequestInfo | URL) => {
    if (String(input) === '/' || String(input) === location.pathname)
      return new Response('<!doctype html><p>no session here</p>', {
        headers: { 'Content-Type': 'text/html' },
      })
    return Response.json(
      { error: { code: 'unauthorized', message: '请提供有效的访问令牌' } },
      { status: 401 },
    )
  })
  render(<App />)
  expect(await screen.findByText('访问令牌已失效，请重新连接监控器。')).toBeVisible()
})
