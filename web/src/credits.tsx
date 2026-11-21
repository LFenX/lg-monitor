/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import type { MouseEvent } from 'react'
import type { API } from './api'
import { useApp } from './state'
import './credits.css'

/*
 * The author's signature and places, shown small wherever lgmon shows its name: the small window's header, the
 * sidebar and Settings → 关于. The small window lets exactly these addresses open in the browser.
 */

export const author = {
  name: 'LFen',
  repository: 'https://github.com/LFenX/lg-monitor',
  site: 'https://lfen.ink/',
  margin: 'https://margin.lfen.ink/',
}

export function Signature() {
  return (
    <span className="lm-sign" title="作者 LFen">
      by <i>LFen</i>
    </span>
  )
}

/*
 * 留白 lets a licensed lgmon in without its login page: lgmon asks the license service for a five-minute ticket
 * and opens 留白's entry with it. Without one (no license, offline) 留白 opens as it always did.
 *
 * A browser only allows a new tab right after the click, so the tab opens at once and goes to the entry when the
 * ticket arrives. lgmon's own window replaces window.open and sends the address to the default browser.
 */
export function openMargin(event: MouseEvent, api: API) {
  if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  const launcher = !!(window as unknown as { lgmonLauncher?: boolean }).lgmonLauncher
  const tab = launcher ? null : window.open('', '_blank')
  if (tab) {
    tab.opener = null
    try {
      tab.document.title = '留白'
      tab.document.body.textContent = '正在打开留白…'
    } catch {
      // The blank tab only waits for its address.
    }
  }
  void api
    .request<{ url: string }>('/license/margin', { method: 'POST' })
    .then((value) => (value?.url?.startsWith('https://margin.lfen.ink/lgmon#') ? value.url : author.margin))
    .catch(() => author.margin)
    .then((url) => {
      if (tab) tab.location.href = url
      else window.open(url, '_blank', 'noreferrer')
    })
}

/* The author's places, small and quiet beside the name: the project's code, the personal site and 留白. */
export function Credits() {
  const { api } = useApp()
  return (
    <nav className="lm-credits" aria-label="作者的链接">
      <a
        href="https://github.com/LFenX/lg-monitor"
        target="_blank"
        rel="noreferrer"
        title="lgmon 的 GitHub 仓库"
        aria-label="GitHub 仓库"
      >
        <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true" focusable="false">
          <path
            fill="currentColor"
            d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
          />
        </svg>
      </a>
      <a
        href="https://lfen.ink/"
        target="_blank"
        rel="noreferrer"
        title="LFen 的个人网站 lfen.ink"
        aria-label="个人网站 lfen.ink"
      >
        <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true" focusable="false">
          <path
            fill="currentColor"
            d="M8 1.2c2.4 3 4.4 5.4 4.4 7.9A4.4 4.4 0 0 1 3.6 9.1C3.6 6.6 5.6 4.2 8 1.2Zm-1.9 8.4a.7.7 0 0 0-.7.7 2.6 2.6 0 0 0 2.6 2.6.7.7 0 1 0 0-1.4 1.2 1.2 0 0 1-1.2-1.2.7.7 0 0 0-.7-.7Z"
          />
        </svg>
      </a>
      <a
        href="https://margin.lfen.ink/"
        target="_blank"
        rel="noreferrer"
        title="留白 margin.lfen.ink"
        aria-label="留白 margin.lfen.ink"
        data-lgmon-opens=""
        onClick={(event) => openMargin(event, api)}
      >
        <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true" focusable="false">
          <path
            fill="none"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
            d="M3.5 2.2h6.3l2.7 2.7v8.9h-9zM9.8 2.2v2.7h2.7M5.6 8h3.2M5.6 10.4h4.8"
          />
        </svg>
      </a>
    </nav>
  )
}
