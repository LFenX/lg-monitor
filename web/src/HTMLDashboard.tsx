import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useApp } from './state'
import { createHTMLBridge } from './html-dashboard'
import type { HTMLContext } from './html-dashboard'
import { ErrorNotice } from './ui'

let nextFrame = 0

export function HTMLDashboard({
  html,
  version,
  title,
  refresh,
  params,
  revision,
}: {
  html: { file: string; source: string }
  version: string
  title: string
  refresh: string
  params: Record<string, unknown>
  revision: number
}) {
  const { api, theme, status, subscribeLive } = useApp()
  const frame = useRef<HTMLIFrameElement>(null)
  const context = useRef<HTMLContext>({ title, theme, refresh, params })
  const currentStatus = useRef(status)
  context.current = { title, theme, refresh, params }
  currentStatus.current = status
  const generation = useMemo(() => ++nextFrame, [api, html.file, html.source, version, revision])
  const connection = useRef<{ bridge: ReturnType<typeof createHTMLBridge>; port: MessagePort } | undefined>(
    undefined,
  )
  const loads = useRef(0)
  const rejected = useRef(false)
  const [error, setError] = useState('')
  useLayoutEffect(() => {
    setError('')
    loads.current = 0
    rejected.current = false
    let closed = false
    const receive = (event: MessageEvent) => {
      const child = frame.current?.contentWindow
      if (
        closed ||
        rejected.current ||
        connection.current ||
        event.source !== child ||
        event.data?.type !== 'lgmon-ready' ||
        event.data?.version !== 1 ||
        !child
      )
        return
      const channel = new MessageChannel()
      const bridge = createHTMLBridge({
        api,
        context: () => context.current,
        status: () => currentStatus.current,
        send: (value) => {
          if (!closed) channel.port1.postMessage(value)
        },
        subscribe: subscribeLive,
        error: (message) => setError('HTML 面板脚本出错：' + message),
      })
      connection.current = { bridge, port: channel.port1 }
      channel.port1.onmessage = (message) => bridge.receive(message.data)
      channel.port1.start()
      child.postMessage(
        { type: 'lgmon-init', version: 1, html: html.source, context: context.current },
        '*',
        [channel.port2],
      )
    }
    window.addEventListener('message', receive)
    return () => {
      closed = true
      window.removeEventListener('message', receive)
      connection.current?.bridge.close()
      connection.current?.port.close()
      connection.current = undefined
    }
  }, [api, generation, html.source, subscribeLive])
  useEffect(() => {
    connection.current?.bridge.contextChanged()
  }, [theme, title, refresh, params])
  return (
    <div className="html-dashboard">
      <ErrorNotice message={error} />
      <iframe
        ref={frame}
        key={generation}
        className="html-dashboard-frame"
        title={title + '（只读 HTML）'}
        src="/lgmon-frame.html"
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        onLoad={() => {
          loads.current++
          if (loads.current > 1) {
            rejected.current = true
            connection.current?.bridge.close()
            connection.current?.port.close()
            connection.current = undefined
            setError('HTML 面板已离开运行容器，请刷新面板重新加载')
          }
        }}
      />
    </div>
  )
}
