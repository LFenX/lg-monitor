/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { I18nProvider, useI18n } from './i18n'
import { State } from './state'
import { GuestVisitorBar, HostVisitorsPill } from './VisitorBar'

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function LangToggle() {
  const { lang, setLang } = useI18n()
  return (
    <button type="button" onClick={() => setLang(lang === 'zh' ? 'en' : 'zh')}>
      toggle-lang
    </button>
  )
}

function makeStubAPI(
  visitors: { ip: string; avatar: string; firstSeen: number; lastSeen: number; connections: number }[],
) {
  return new API('fake-token', (async (input: RequestInfo | URL) => {
    const path = String(input).replace('/api/v1', '')
    if (path === '/stream')
      return new Response(new ReadableStream(), { headers: { 'Content-Type': 'text/event-stream' } })
    if (path === '/lan')
      return Response.json({
        visible: false,
        web: true,
        webAddresses: ['http://192.168.1.10:7444/'],
        name: '爽朗的狐狸',
        avatar: 'fiora-3',
        hostname: 'DEV-PC',
        fingerprint: 'AAAA-BBBB',
        addresses: [],
        listening: false,
        pending: 0,
        viewers: 0,
      })
    if (path === '/lan/visitors') return Response.json(visitors)
    if (path === '/lan/remote' || path === '/boards' || path === '/monitors' || path === '/dashboards')
      return Response.json([])
    if (
      path === '/collectors' ||
      path === '/targets' ||
      path === '/alerts' ||
      path === '/silences' ||
      path === '/rule-errors'
    )
      return Response.json([])
    if (path === '/notifiers') return Response.json({ notifiers: [] })
    if (path === '/notifications/pause') return Response.json({ paused: false, until: null })
    if (path === '/guide')
      return Response.json({
        product: 'lgmon',
        version: '0.14.0',
        scope: 'read',
        canConfigure: false,
        install: { executable: 'lgmon', home: 'home', personal: true, url: 'http://127.0.0.1:7444' },
        state: {},
        collectors: [],
        steps: [],
        modules: [],
        workflow: [],
      })
    return Response.json({})
  }) as typeof fetch)
}

it('shows guest visitor card with Bohemia read-only button, direct download link, and bilingual switch', async () => {
  const api = makeStubAPI([])
  const openAssistant = vi.fn()
  render(
    <I18nProvider>
      <State api={api} scope="read" guest theme="light" keep={false}>
        <LangToggle />
        <GuestVisitorBar openAssistant={openAssistant} />
      </State>
    </I18nProvider>,
  )
  const banner = await screen.findByLabelText('内网访客提示')
  expect(banner).toHaveTextContent('爽朗的狐狸')
  expect(banner).toHaveTextContent('只读')
  const dl = within(banner).getByRole('link', { name: /立即下载最新版/ })
  expect(dl).toHaveAttribute('href', '/api/v1/download/latest?channel=guest')
  await userEvent.click(within(banner).getByRole('button', { name: /问波西米亚/ }))
  expect(openAssistant).toHaveBeenCalledTimes(1)

  // Switch to English
  await userEvent.click(screen.getByRole('button', { name: 'toggle-lang' }))
  expect(await screen.findByLabelText('LAN Visitor Banner')).toHaveTextContent('Download Latest')
})

it('shows active LAN visitors in the host sidebar pill and opens the IP details dialog', async () => {
  const now = Date.now()
  const api = makeStubAPI([
    {
      ip: '192.168.1.42',
      avatar: 'lg-cat-2',
      firstSeen: now - 60_000,
      lastSeen: now - 2_000,
      connections: 1,
    },
  ])
  render(
    <I18nProvider>
      <State api={api} scope="config" guest={false} theme="light" keep={false}>
        <HostVisitorsPill />
      </State>
    </I18nProvider>,
  )
  const pill = await screen.findByRole('button', { name: /1 位内网访客正在查看/ })
  await userEvent.click(pill)
  const dialog = await screen.findByRole('dialog', { name: '当前内网访客' })
  await waitFor(() => expect(dialog).toHaveTextContent('192.168.1.42'))
  expect(dialog).toHaveTextContent('http://192.168.1.10:7444/')
})
