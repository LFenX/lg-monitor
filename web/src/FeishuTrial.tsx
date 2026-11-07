import { useEffect, useRef, useState } from 'react'
import { APIError } from './api'
import type { FeishuStep } from './FeishuInspection'
import type { Confirmation } from './FeishuSetup'
import { useApp, useResource } from './state'
import type { TemplateDefinition } from './templates'
import { Empty, ErrorNotice, Modal } from './ui'

export function FeishuTrial({
  draft,
  state,
  close,
  invalid,
}: {
  draft: TemplateDefinition
  state: 'firing' | 'resolved'
  close: () => void
  invalid: (error: APIError) => void
}) {
  const { api, data } = useApp()
  const apps = data?.notifiers.feishuApps || [],
    channels = data?.notifiers.notifiers || []
  const hooks = channels.filter((channel) => channel.type === 'feishu_webhook')
  const firstSaved = channels.find(
    (channel) => channel.type === 'feishu_app' && apps.some((app) => app.id === channel.app),
  )
  const [destination, setDestination] = useState(
    firstSaved
      ? 'app:' + firstSaved.app
      : apps.length
        ? 'app:' + apps[0].id
        : hooks.length
          ? 'hook:' + hooks[0].id
          : '',
  )
  const [chat, setChat] = useState(''),
    [busy, setBusy] = useState(false),
    [cooldown, setCooldown] = useState(0),
    [error, setError] = useState(''),
    [message, setMessage] = useState('')
  const action = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => action.current?.abort(), [])
  useEffect(() => {
    if (!cooldown) return
    const timer = setTimeout(() => setCooldown((value) => value - 1), 1000)
    return () => clearTimeout(timer)
  }, [cooldown])
  const app = destination.startsWith('app:') ? destination.slice(4) : ''
  const groups = useResource(
    (signal) =>
      app
        ? api.request<FeishuStep>('/feishu/chats?app=' + encodeURIComponent(app), { signal })
        : Promise.resolve<FeishuStep>({ step: 'chat', status: 'ok', message: '', chats: [] }),
    [api, app],
  )
  useEffect(() => {
    const chats = groups.value?.chats || []
    const saved = channels.find(
      (channel) =>
        channel.type === 'feishu_app' &&
        channel.app === app &&
        chats.some((chat) => chat.id === channel.chatId),
    )
    setChat(saved?.chatId || chats[0]?.id || '')
  }, [groups.value, app])
  const send = async () => {
    const controller = new AbortController()
    action.current = controller
    setBusy(true)
    setError('')
    setMessage('')
    try {
      const result = await api.request<Confirmation>(
        app ? '/feishu/test' : '/notifiers/' + encodeURIComponent(destination.slice(5)) + '/test',
        {
          method: 'POST',
          signal: controller.signal,
          body: JSON.stringify(app ? { app, chatId: chat, draft, state } : { draft }),
        },
      )
      if (!controller.signal.aborted) {
        setMessage(result.message)
        setCooldown(10)
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        if (err instanceof APIError && [400, 422].includes(err.status)) {
          invalid(err)
          close()
          return
        }
        setError(err instanceof Error ? err.message : '测试发送失败')
        if (err instanceof APIError && err.status === 429) setCooldown(10)
      }
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  return (
    <Modal title="发送到测试群" close={close}>
      <p className="muted">
        使用当前未保存的模板；不会保存模板或启用通知渠道。卡片带有测试标记，请在飞书中查看实际效果。
      </p>
      {destination ? (
        <div className="form-body">
          <label>
            发送方式
            <select
              value={destination}
              disabled={busy}
              onChange={(event) => {
                setDestination(event.target.value)
                setMessage('')
                setError('')
              }}
            >
              {apps.map((app) => (
                <option key={app.id} value={'app:' + app.id}>
                  应用：{app.id}
                </option>
              ))}
              {hooks.map((channel) => (
                <option key={channel.id} value={'hook:' + channel.id}>
                  自定义机器人：{channel.chatName || channel.id}
                </option>
              ))}
            </select>
          </label>
          {app ? (
            <>
              <ErrorNotice
                message={groups.error || (groups.value?.status === 'error' ? groups.value.message : '')}
              />
              <label>
                测试群
                <select
                  aria-label="测试群"
                  value={chat}
                  disabled={busy || groups.loading}
                  onChange={(event) => {
                    setChat(event.target.value)
                    setMessage('')
                  }}
                >
                  {!groups.value?.chats?.length && (
                    <option value="">{groups.loading ? '正在读取群…' : '尚未发现群'}</option>
                  )}
                  {groups.value?.chats?.map((chat) => (
                    <option key={chat.id} value={chat.id}>
                      {chat.name || '未命名的群'}
                    </option>
                  ))}
                </select>
              </label>
              <p className="small muted">
                试发状态：{state === 'resolved' ? '恢复' : '触发'}。可选择专门的测试群。
              </p>
            </>
          ) : (
            <p className="small muted">向已保存的自定义机器人发送触发测试卡片。</p>
          )}
          <ErrorNotice message={error} />
          {message && (
            <p className="notice" role="status">
              {message}
            </p>
          )}
          <div className="modal-actions">
            <button className="button" onClick={close}>
              关闭
            </button>
            <button
              className="button primary"
              disabled={busy || cooldown > 0 || (app !== '' && (!chat || groups.loading))}
              onClick={() => void send()}
            >
              {busy ? '正在发送…' : cooldown ? cooldown + ' 秒后可重发' : '发送当前草稿'}
            </button>
          </div>
        </div>
      ) : (
        <Empty title="先接入飞书或配置自定义机器人">
          <a href="#/notifications/setup" target="_blank" rel="noopener noreferrer">
            在新页面接入飞书 ↗
          </a>
        </Empty>
      )}
    </Modal>
  )
}
