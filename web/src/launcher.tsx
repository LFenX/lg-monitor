/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { Row } from './settings-rows'

/*
 * Whether this page runs inside lgmon's own window. Kept apart from the update code so a browser, and the
 * settings page, never load it.
 */
export type Host = {
  chrome?: { webview?: { postMessage: (message: string) => void } }
  lgmonLauncher?: boolean
}

export const inLauncher = () => {
  const host = window as unknown as Host
  return !!host.lgmonLauncher && !!host.chrome?.webview
}

/* The version row in settings. Updating happens in lgmon's window, which this page in a browser is not. */
export function UpdateRow({ version }: { version: string }) {
  const dev = !version || version === 'dev'
  return (
    <Row
      title="版本"
      hint={
        dev
          ? '开发版，不检查更新'
          : 'lgmon ' + version + ' · 有新版本时，lgmon 的小窗口会提示更新；也可以在小窗口底部点“检查更新”'
      }
    />
  )
}
