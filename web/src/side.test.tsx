/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'
import type { BoardView, LANOutgoing } from './types'

let bootstrap: HTMLScriptElement
let outgoing: LANOutgoing[]
let remoteBoards: Record<
  string,
  { ownerName: string; ownerAvatar?: string; board: BoardView; fail?: boolean }
>

function makeBoard(id: string, name: string, down = 0, services = 3, uptime = 100): BoardView {
  return {
    id,
    name,
    monitors: ['svc-1'],
    summary: {
      services,
      children: 1,
      up: services + 1 - down,
      down,
      unknown: 0,
      uptime,
      state: down > 0 ? 'down' : 'up',
    },
    url: '',
    unassigned: [],
    items: [
      {
        id: 'svc-1',
        name: name + ' API',
        watching: ['端口 8080'],
        state: down > 0 ? 'down' : 'up',
        overall: down > 0 ? 'down' : 'up',
        hosts: [],
        rules: [],
        firing: down,
        beats: Array(48).fill(down > 0 ? 'down' : 'up'),
        uptime,
        latencyMs: 8,
        kind: 'port',
        children: [],
        depth: 0,
      },
    ],
  }
}

beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  window.location.hash = '#/'
  outgoing = []
  remoteBoards = {}
  bootstrap = document.createElement('script')
  bootstrap.id = 'lgmon-session'
  bootstrap.type = 'application/json'
  bootstrap.textContent = JSON.stringify({ token: 'fake-local', scope: 'read' })
  document.head.append(bootstrap)

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input).replace('/api/v1', '')
      const headers = { 'X-Lgmon-Scope': 'config' }
      if (path === '/stream') {
        return new Response(new ReadableStream(), {
          headers: { ...headers, 'Content-Type': 'text/event-stream' },
        })
      }
      if (path === '/lan') {
        return Response.json(
          {
            visible: true,
            name: '我的电脑',
            avatar: 'fiora-3',
            hostname: 'fake-host',
            fingerprint: 'AAAA-BBBB-CCCC-DDDD',
            addresses: ['https://192.0.2.10:7445'],
            listening: true,
            pending: 0,
            viewers: 0,
          },
          { headers },
        )
      }
      if (path === '/lan/remote') return Response.json(outgoing, { headers })
      if (path.startsWith('/lan/remote/')) {
        const id = decodeURIComponent(path.slice('/lan/remote/'.length))
        const entry = remoteBoards[id]
        if (!entry || entry.fail) {
          return Response.json(
            { error: { code: 'unreachable', message: '连不上对方' } },
            { status: 502, headers },
          )
        }
        return Response.json(
          {
            id,
            ownerName: entry.ownerName,
            ownerAvatar: entry.ownerAvatar,
            address: 'https://192.0.2.20:7445',
            board: entry.board,
            fetchedAt: Date.now(),
          },
          { headers },
        )
      }
      if (path === '/departments') {
        return Response.json(
          {
            me: 'KEY-ME',
            pending: {},
            hosts: [
              {
                id: 'HOSTA',
                name: '测试主机',
                departments: [
                  {
                    id: 'd1',
                    name: 'LFenink_Department',
                    avatar: 'fiora-1',
                    people: [
                      { key: 'KEY-QZZW', name: 'qzzw', role: 'member' },
                      { key: 'KEY-WANG', name: '小王', role: 'leader' },
                    ],
                  },
                ],
              },
            ],
          },
          { headers },
        )
      }
      if (path === '/boards') return Response.json([makeBoard('main', '本机服务', 0, 2, 100)], { headers })
      if (path === '/boards/main') return Response.json(makeBoard('main', '本机服务', 0, 2, 100), { headers })
      if (path === '/guide') {
        return Response.json(
          {
            version: '0.16.3',
            steps: [],
            state: { agentSeenAt: 0 },
            install: { executable: 'C:\\Fake\\lgmon.exe', home: 'C:\\Fake', personal: true, url: '' },
            collectors: [],
          },
          { headers },
        )
      }
      if (path === '/notifications/pause') return Response.json({ paused: false, until: null }, { headers })
      if (path === '/config/targets') return Response.json({ targets: [] }, { headers })
      if (path === '/config/notifiers') return Response.json({ notifiers: [] }, { headers })
      if (
        path === '/collectors' ||
        path === '/targets' ||
        path === '/alerts' ||
        path === '/rule-errors' ||
        path === '/monitors' ||
        path === '/dashboards' ||
        path === '/agents'
      ) {
        return Response.json([], { headers })
      }
      return Response.json({}, { headers })
    }),
  )
})

afterEach(() => {
  bootstrap.remove()
  vi.unstubAllGlobals()
})

it('deduplicates same-name single remote board, shows live health summary, and folds departments', async () => {
  outgoing = [
    {
      id: 'out-qzzw',
      address: 'https://192.0.2.21:7445',
      ownerName: 'qzzw',
      ownerAvatar: 'lg-cat-1',
      ownerAccount: 'KEY-QZZW',
      fingerprint: '1'.repeat(64),
      boardId: 'main',
      boardName: 'qzzw',
      code: '111 222',
      status: 'approved',
      createdAt: Date.now(),
    },
    {
      id: 'out-wang-1',
      address: 'https://192.0.2.22:7445',
      ownerName: '小王',
      ownerAvatar: 'huaji-18',
      ownerAccount: 'KEY-WANG',
      fingerprint: '2'.repeat(64),
      boardId: 'prod',
      boardName: '线上核心',
      code: '222 333',
      status: 'approved',
      createdAt: Date.now(),
    },
    {
      id: 'out-wang-2',
      address: 'https://192.0.2.22:7445',
      ownerName: '小王',
      ownerAvatar: 'huaji-18',
      ownerAccount: 'KEY-WANG',
      fingerprint: '2'.repeat(64),
      boardId: 'test',
      boardName: '测试环境',
      code: '333 444',
      status: 'approved',
      createdAt: Date.now(),
    },
    {
      id: 'out-offline',
      address: 'https://192.0.2.23:7445',
      ownerName: '小陈',
      fingerprint: '3'.repeat(64),
      boardId: 'dev',
      boardName: '后端开发',
      code: '444 555',
      status: 'approved',
      createdAt: Date.now(),
    },
  ]
  remoteBoards = {
    'out-qzzw': { ownerName: 'qzzw', ownerAvatar: 'lg-cat-1', board: makeBoard('main', 'qzzw', 0, 5, 100) },
    'out-wang-1': {
      ownerName: '小王',
      ownerAvatar: 'huaji-18',
      board: makeBoard('prod', '线上核心', 2, 4, 98.4),
    },
    'out-wang-2': {
      ownerName: '小王',
      ownerAvatar: 'huaji-18',
      board: makeBoard('test', '测试环境', 0, 2, 100),
    },
    'out-offline': { ownerName: '小陈', board: makeBoard('dev', '后端开发'), fail: true },
  }

  render(<App />)

  const sharedNav = await screen.findByRole('navigation', { name: '别人的面板' })
  const deptGroup = await within(sharedNav).findByRole('group', { name: 'LFenink_Department' })

  // Same-name single board (qzzw -> qzzw) shows qzzw once + live service count & uptime from background prefetch.
  const qzzwGroup = within(deptGroup).getByRole('group', { name: 'qzzw 的面板' })
  await waitFor(() => expect(qzzwGroup).toHaveTextContent('6 个服务 · 100%'))
  expect(within(qzzwGroup).getAllByText('qzzw')).toHaveLength(1)

  // Multi-board owner (小王) shows nested board rows with live crimson down count on 线上核心.
  const wangGroup = within(deptGroup).getByRole('group', { name: '小王 的面板' })
  await waitFor(() =>
    expect(within(wangGroup).getByRole('link', { name: /线上核心/ })).toHaveTextContent('2'),
  )
  expect(within(wangGroup).getByRole('link', { name: /测试环境/ })).toHaveTextContent('3')

  // Unassigned colleague that fails prefetch displays offline cue without crashing.
  const otherGroup = within(sharedNav).getByRole('group', { name: '其他' })
  await waitFor(() => expect(otherGroup).toHaveTextContent('连不上'))

  // Department header collapses/expands cleanly while preserving its abnormal count badge.
  const deptToggle = within(deptGroup).getByRole('button', { name: '收起部门 LFenink_Department' })
  expect(deptToggle).toHaveAttribute('aria-expanded', 'true')
  expect(deptToggle).toHaveTextContent('2')
  await userEvent.click(deptToggle)
  expect(within(deptGroup).queryByRole('group', { name: 'qzzw 的面板' })).not.toBeInTheDocument()
  await userEvent.click(within(deptGroup).getByRole('button', { name: '展开部门 LFenink_Department' }))
  expect(within(deptGroup).getByRole('group', { name: 'qzzw 的面板' })).toBeVisible()
})
