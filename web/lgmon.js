;(() => {
  if (Object.prototype.hasOwnProperty.call(window, 'lgmon')) return
  const parentWindow = window.parent
  const target = document.getElementById('lgmon-content')
  const topics = new Set([
    'target',
    'alert',
    'sample',
    'dashboard',
    'pause',
    'ruleErrors',
    '*',
    'context',
    'connection',
  ])
  const listeners = new Map()
  const pending = new Map()
  let port,
    context = Object.freeze({}),
    sequence = 0
  const send = (value) => port?.postMessage(value)
  const report = (message) => send({ type: 'error', message: String(message).slice(0, 500) })
  const dispatch = (event, data) => {
    for (const name of [event, '*']) {
      for (const callback of listeners.get(name)?.values() || []) {
        try {
          callback(data, event)
        } catch (error) {
          report(error instanceof Error ? error.message : error)
        }
      }
    }
  }
  const updateContext = (value) => {
    context = Object.freeze({ ...value, params: Object.freeze({ ...value.params }) })
    document.documentElement.dataset.theme = value.theme === 'dark' ? 'dark' : 'light'
    if (typeof value.title === 'string') document.title = value.title
    dispatch('context', context)
  }
  const api = {
    version: 1,
    get context() {
      return context
    },
    query(sql, params, options = {}) {
      if (!port) return Promise.reject(new Error('lgmon.js 尚未连接到宿主页，请在内置 HTML 面板中调用'))
      if (typeof sql !== 'string') return Promise.reject(new Error('query 的 sql 必须是字符串'))
      if (options.signal?.aborted) return Promise.reject(new DOMException('查询已取消', 'AbortError'))
      const id = ++sequence
      return new Promise((resolve, reject) => {
        const finish = (error, value) => {
          clearTimeout(timer)
          options.signal?.removeEventListener('abort', abort)
          pending.delete(id)
          if (error) reject(error)
          else resolve(value)
        }
        const abort = () => {
          send({ type: 'cancel', id })
          finish(new DOMException('查询已取消', 'AbortError'))
        }
        const timer = setTimeout(() => {
          send({ type: 'cancel', id })
          finish(new Error('只读查询超时，请缩小查询范围'))
        }, 15000)
        pending.set(id, finish)
        options.signal?.addEventListener('abort', abort, { once: true })
        try {
          send({ type: 'query', id, sql, ...(params === undefined ? {} : { params }) })
        } catch {
          finish(new Error('查询参数无法传输，请只使用 JSON 参数'))
        }
      })
    },
    subscribe(event, callback) {
      if (!topics.has(event) || typeof callback !== 'function')
        throw new Error('subscribe 需要受支持的事件名和回调函数')
      if (!port) throw new Error('lgmon.js 尚未连接到宿主页')
      let callbacks = listeners.get(event)
      if (!callbacks) {
        callbacks = new Map()
        listeners.set(event, callbacks)
        send({ type: 'subscribe', event })
      }
      const id = ++sequence
      callbacks.set(id, callback)
      if (event === 'context')
        queueMicrotask(() => {
          if (callbacks.has(id)) callback(context, event)
        })
      return () => {
        callbacks.delete(id)
        if (callbacks.size === 0 && listeners.get(event) === callbacks) {
          listeners.delete(event)
          send({ type: 'unsubscribe', event })
        }
      }
    },
  }
  Object.defineProperty(window, 'lgmon', { value: Object.freeze(api), writable: false, configurable: false })
  const initialize = (message) => {
    if (
      port ||
      parentWindow === window ||
      message.source !== parentWindow ||
      message.data?.type !== 'lgmon-init' ||
      message.data?.version !== 1 ||
      message.ports.length !== 1 ||
      typeof message.data.html !== 'string' ||
      !target
    )
      return
    port = message.ports[0]
    window.removeEventListener('message', initialize)
    port.onmessage = ({ data }) => {
      if (data?.type === 'result') {
        const finish = pending.get(data.id)
        if (!finish) return
        if (data.ok) finish(null, data.value)
        else {
          const error = new Error(data.error?.message || '只读查询失败')
          error.code = data.error?.code
          error.status = data.error?.status
          finish(error)
        }
      } else if (data?.type === 'context' && data.value && typeof data.value === 'object')
        updateContext(data.value)
      else if (data?.type === 'event' && topics.has(data.event)) {
        dispatch(data.event, data.data)
        send({ type: 'event_ack', id: data.id })
      }
    }
    port.start()
    updateContext(message.data.context || {})
    const template = document.createElement('template')
    template.innerHTML = message.data.html
    const scripts = [...template.content.querySelectorAll('script')]
    target.replaceChildren(template.content)
    for (const original of scripts) {
      if (!original.parentNode) continue
      const script = document.createElement('script')
      for (const attribute of original.attributes) script.setAttribute(attribute.name, attribute.value)
      script.textContent = original.textContent
      original.replaceWith(script)
    }
  }
  window.addEventListener('message', initialize)
  window.addEventListener('error', (event) => report(event.message || 'HTML 面板脚本运行失败'))
  window.addEventListener('unhandledrejection', (event) =>
    report(event.reason instanceof Error ? event.reason.message : 'HTML 面板异步操作失败'),
  )
  window.addEventListener('pagehide', () => {
    for (const id of pending.keys()) send({ type: 'cancel', id })
    send({ type: 'close' })
    for (const finish of [...pending.values()]) finish(new Error('HTML 面板已关闭，查询已取消'))
    listeners.clear()
    port?.close()
    port = undefined
  })
  if (parentWindow !== window) parentWindow.postMessage({ type: 'lgmon-ready', version: 1 }, '*')
})()
