import { useEffect, useState } from 'react'
import { Glyph } from './glyphs'
import { Row } from './settings-rows'

/*
 * Updates happen in lgmon's own window: it finds a newer release, downloads and checks it, and on "重启并更新"
 * hands over to the new installer. The page only shows what the window reports and asks it to act.
 */

export type HostUpdate = {
  type: 'update'
  state: 'dev' | 'checking' | 'none' | 'available' | 'downloading' | 'ready' | 'error'
  during?: 'check' | 'download'
  message?: string
  current?: string
  version?: string
  notes?: string
  size?: number
  done?: number
  total?: number
}

type Host = { chrome?: { webview?: { postMessage: (message: string) => void } }; lgmonLauncher?: boolean }

const host = () => window as unknown as Host
export const inLauncher = () => !!host().lgmonLauncher && !!host().chrome?.webview

function ask(type: string) {
  host().chrome?.webview?.postMessage(JSON.stringify({ type }))
}

export function useHostUpdate(): HostUpdate | null {
  const [update, setUpdate] = useState<HostUpdate | null>(null)
  useEffect(() => {
    if (!inLauncher()) return
    const receive = (event: Event) => {
      const detail = (event as CustomEvent).detail
      if (detail?.type === 'update') setUpdate(detail)
    }
    window.addEventListener('lgmon-host', receive)
    ask('update-check')
    return () => window.removeEventListener('lgmon-host', receive)
  }, [])
  return update
}

const megabytes = (bytes = 0) => (bytes / 1e6).toFixed(1)

/* The start page's update card: there only while a newer version is on offer. */
export function UpdateCard() {
  const update = useHostUpdate()
  if (!update) return null
  const failedDownload = update.state === 'error' && update.during === 'download'
  if (
    update.state !== 'available' &&
    update.state !== 'downloading' &&
    update.state !== 'ready' &&
    !failedDownload
  )
    return null
  const percent = update.total ? Math.min(100, Math.round(((update.done || 0) / update.total) * 100)) : 0
  return (
    <section className="lm-start-update" data-state={update.state} aria-label="更新">
      <span className="lm-start-update-ic" aria-hidden="true">
        <Glyph name={failedDownload ? 'refresh' : 'sparkle'} size={16} />
      </span>
      <div className="lm-start-update-t">
        {update.state === 'available' && (
          <>
            <b>lgmon {update.version} 可以更新了</b>
            <small>
              {update.notes && (
                <button className="lm-start-update-link" onClick={() => ask('update-notes')}>
                  更新内容
                </button>
              )}
              {update.notes && update.size ? ' · ' : ''}
              {update.size ? '约 ' + megabytes(update.size) + ' MB' : ''}
            </small>
          </>
        )}
        {update.state === 'downloading' && (
          <>
            <b>正在下载 {update.version}</b>
            <small>
              {megabytes(update.done)} / {megabytes(update.total)} MB
            </small>
            <span className="lm-start-update-bar" role="progressbar" aria-valuenow={percent}>
              <i style={{ width: percent + '%' }} />
            </span>
          </>
        )}
        {update.state === 'ready' && (
          <>
            <b>{update.version} 已经下载好</b>
            <small>重启 lgmon 就会更新，配置和数据都保留。</small>
          </>
        )}
        {failedDownload && (
          <>
            <b>更新没有下载好</b>
            <small>{update.message}</small>
          </>
        )}
      </div>
      {update.state === 'available' && (
        <button className="lm-start-update-btn" onClick={() => ask('update-download')}>
          下载更新
        </button>
      )}
      {update.state === 'ready' && (
        <button className="lm-start-update-btn primary" onClick={() => ask('update-apply')}>
          重启并更新
        </button>
      )}
      {failedDownload && (
        <button className="lm-start-update-btn" onClick={() => ask('update-download')}>
          重试
        </button>
      )}
    </section>
  )
}

/* The version row in settings. Updating happens in lgmon's window, which this page in a browser is not. */
export function UpdateRow({ version }: { version: string }) {
  const dev = !version || version === 'dev'
  return (
    <Row
      title="版本"
      hint={dev ? '开发版，不检查更新' : 'lgmon ' + version + ' · 有新版本时，lgmon 的小窗口顶部会提示更新'}
    />
  )
}
