import { useEffect, useRef, useState } from 'react'
import { useApp } from './state'
import type { Notifier } from './types'
import { Empty, ErrorNotice, Panel } from './ui'

type Status = { notifier: string; app: string; enabled: boolean; state: string; updatedAt: number }
const states: Record<string, string> = {
  disabled: '未授权',
  waiting: '等待监控器加载',
  connecting: '正在连接',
  connected: '已连接',
  reconnecting: '正在重连',
  error: '连接或签名密钥不可用，请检查应用和网络',
  disconnected: '连接中断',
  stopped: '已停止',
}

export function CardActionsSettings({ channels }: { channels: Notifier[] }) {
  const { api, scope, refresh } = useApp()
  const available = channels.filter((channel) => channel.type === 'feishu_app' && !channel.openId)
  const [selected, setSelected] = useState(''),
    [revision, setRevision] = useState(0)
  const id = available.some((channel) => channel.id === selected) ? selected : available[0]?.id || ''
  const [status, setStatus] = useState<Status>(),
    [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const saving = useRef<AbortController | undefined>(undefined)
  useEffect(
    () => () => {
      saving.current?.abort()
    },
    [api, id],
  )
  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    if (busy) return () => controller.abort()
    setStatus(undefined)
    setError('')
    const load = async () => {
      try {
        const result = await api.request<Status>('/notifiers/' + encodeURIComponent(id) + '/card-actions', {
          signal: controller.signal,
        })
        if (!controller.signal.aborted) {
          setStatus(result)
          setError('')
        }
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '卡片动作状态读取失败')
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(() => void load(), 5000)
      }
    }
    if (id) void load()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [api, id, revision, busy])
  const save = async () => {
    if (!status || scope !== 'admin' || busy) return
    const controller = new AbortController()
    saving.current = controller
    setBusy(true)
    setError('')
    try {
      const result = await api.request<Status>('/notifiers/' + encodeURIComponent(id) + '/card-actions', {
        method: 'PUT',
        signal: controller.signal,
        body: JSON.stringify({ enabled: !status.enabled }),
      })
      if (!controller.signal.aborted) {
        setStatus(result)
        refresh()
      }
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '卡片动作授权保存失败')
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  return (
    <Panel title="飞书卡片操作">
      <div className="form-body">
        <p>
          启用后，此通知群原消息的收件人可以直接确认对应告警，或将同一主机、目标和规则实例静音 1
          小时。其他告警组不受影响，确认后恢复仍会通知。
        </p>
        <p className="small muted">
          先在自己的飞书应用后台将卡片回传交互配置为长连接接收，并订阅
          card.action.trigger。同一应用只连接一台监控器；连接成功后产生的新触发卡片才带操作按钮。
        </p>
        {available.length ? (
          <>
            <label>
              通知群
              <select
                aria-label="卡片操作通知群"
                value={id}
                disabled={busy}
                onChange={(event) => setSelected(event.target.value)}
              >
                {available.map((channel) => (
                  <option key={channel.id} value={channel.id}>
                    {channel.chatName || channel.id}
                  </option>
                ))}
              </select>
            </label>
            <p role="status">
              {status?.notifier === id
                ? (status.enabled ? '已授权 · ' : '') + (states[status.state] || '状态未知')
                : '正在读取授权状态…'}
            </p>
            <ErrorNotice message={error} />
            <div className="actions">
              <button
                className="button"
                disabled={scope !== 'admin' || busy || !status || !!error}
                onClick={() => void save()}
              >
                {busy ? '正在保存…' : status?.enabled ? '撤销卡片操作' : '启用卡片操作'}
              </button>
              <button className="button" disabled={busy} onClick={() => setRevision((value) => value + 1)}>
                刷新连接状态
              </button>
            </div>
            {scope !== 'admin' && (
              <p className="small muted">授权开关需要管理员访问令牌；当前权限只能查看。</p>
            )}
          </>
        ) : (
          <Empty title="先接入飞书应用通知群" />
        )}
      </div>
    </Panel>
  )
}
