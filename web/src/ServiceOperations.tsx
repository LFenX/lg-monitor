import { useEffect, useRef, useState } from 'react'
import { APIError } from './api'
import { timestamp } from './model'
import { useApp, useResource } from './state'
import { Empty, ErrorNotice, Modal, Panel } from './ui'
import './operations.css'

type Grant = { id: string; service: string }
type Policy = { enabled: boolean; version: string; services: Grant[]; reason: string }
type Record = {
  requestId: string
  collectorId: string
  service: string
  state: string
  message: string
  startedAt: number
  finishedAt: number
}
type Review = { grant: Grant; version: string; requestId: string }
const states: { [key: string]: string } = {
  running: '等待确认结果',
  succeeded: '已核对运行',
  failed: '未完成',
  denied: '已拒绝',
  unknown: '结果未知，需核对',
}
function storageKey(collector: string) {
  return 'lgmon-operation:' + collector
}
function lastRequest(collector: string) {
  try {
    return sessionStorage.getItem(storageKey(collector)) || ''
  } catch {
    return ''
  }
}
function newRequestId() {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
}

export function ServiceOperations({ collector, hostname }: { collector: string; hostname: string }) {
  const { api, scope } = useApp()
  const [revision, setRevision] = useState(0)
  const policy = useResource(
    (signal) =>
      api.request<Policy>('/collectors/' + encodeURIComponent(collector) + '/operations', { signal }),
    [api, collector, revision],
    collector,
  )
  const [review, setReview] = useState<Review>(),
    [reason, setReason] = useState(''),
    [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [record, setRecord] = useState<Record>()
  const [requestId, setRequestId] = useState(() => lastRequest(collector))
  const pending = useRef<AbortController | undefined>(undefined)
  useEffect(() => {
    setReview(undefined)
    setRecord(undefined)
    setRequestId(lastRequest(collector))
    setError('')
    setBusy(false)
    return () => pending.current?.abort()
  }, [api, collector])
  const open = (grant: Grant) => {
    if (scope !== 'admin' || !policy.value?.enabled || busy) return
    try {
      setReview({ grant: { ...grant }, version: policy.value.version, requestId: newRequestId() })
      setConfirmed(false)
      setReason('')
      setError('')
    } catch {
      setError('无法生成操作请求 ID，请检查浏览器后重试')
    }
  }
  const query = async () => {
    if (!/^[a-f0-9]{32}$/.test(requestId)) {
      setError('请填写 32 位小写十六进制请求 ID')
      return
    }
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setBusy(true)
    setError('')
    try {
      const result = await api.request<Record>('/operations/' + encodeURIComponent(requestId), {
        signal: controller.signal,
      })
      if (!controller.signal.aborted) setRecord(result)
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '操作记录读取失败')
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  const execute = async () => {
    if (
      !review ||
      !confirmed ||
      !reason.trim() ||
      Array.from(reason).length > 200 ||
      scope !== 'admin' ||
      busy
    )
      return
    const selected = review
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setRequestId(selected.requestId)
    setRecord(undefined)
    setBusy(true)
    setError('')
    try {
      sessionStorage.setItem(storageKey(collector), selected.requestId)
    } catch {
      /* The visible request ID remains available for manual copying. */
    }
    try {
      const result = await api.request<Record>(
        '/collectors/' + encodeURIComponent(collector) + '/operations',
        {
          method: 'POST',
          signal: controller.signal,
          body: JSON.stringify({
            requestId: selected.requestId,
            grantId: selected.grant.id,
            version: selected.version,
            reason,
            confirmed: true,
          }),
        },
      )
      if (!controller.signal.aborted) {
        setRecord(result)
        setReview(undefined)
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        setError(err instanceof Error ? err.message : '执行结果未确认，请按原请求 ID 查询')
        if (err instanceof APIError && err.status === 409) setRevision((value) => value + 1)
      }
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  return (
    <Panel
      title="受限服务操作"
      extra={
        <button className="button" disabled={busy} onClick={() => setRevision((value) => value + 1)}>
          刷新本机授权
        </button>
      }
    >
      <div className="operation-body">
        <p className="small muted">
          默认关闭，只能在采集器本机由管理员逐条授权精确服务。执行需要管理员访问令牌和本人逐次确认。
        </p>
        {policy.error ? (
          <p className="muted">{policy.error}</p>
        ) : policy.value?.enabled ? (
          <>
            <p>{policy.value.reason}</p>
            <div className="app-list">
              {policy.value.services.map((grant) => (
                <article key={grant.id}>
                  <div style={{ minWidth: 0 }}>
                    <strong style={{ overflowWrap: 'anywhere' }}>{grant.service}</strong>
                    <p className="small muted">无参数重启此精确服务</p>
                  </div>
                  <button className="button" disabled={scope !== 'admin' || busy} onClick={() => open(grant)}>
                    准备重启 {grant.service}
                  </button>
                </article>
              ))}
            </div>
          </>
        ) : (
          <Empty
            title={policy.loading ? '正在读取本机授权' : policy.value?.reason || '尚未启用远程服务操作'}
          />
        )}
        <ErrorNotice message={review ? '' : error} />
        {record && (
          <div role="status">
            <strong>{states[record.state] || '状态无效'}</strong>
            <p>
              {record.service} · {record.message}
            </p>
            <p className="small muted">
              登记：{timestamp(record.startedAt)}
              {record.finishedAt > 0 && <> · 记录终态：{timestamp(record.finishedAt)}</>}
            </p>
          </div>
        )}
        <label className="operation-field">
          原请求 ID
          <input
            aria-label="服务操作请求 ID"
            className="mono"
            value={requestId}
            disabled={busy}
            onChange={(event) => {
              setRequestId(event.target.value)
              setRecord(undefined)
            }}
          />
        </label>
        <button className="button" disabled={busy || !requestId} onClick={() => void query()}>
          只读查询操作结果
        </button>
        {busy && <p role="status">正在等待操作结果；已接受的操作不会因离开页面而撤销。请保留原请求 ID。</p>}
        {scope !== 'admin' && <p className="small muted">当前权限只能查看授权和已有操作结果。</p>}
      </div>
      {review && (
        <Modal title="确认重启服务" close={() => setReview(undefined)}>
          <div className="operation-body operation-review">
            <p>
              主机：<strong>{hostname}</strong>
            </p>
            <p>
              精确服务：<strong>{review.grant.service}</strong>
            </p>
            <p>重启会中断此服务。已接受的操作不能通过关闭弹窗撤回；结果未知时只查询原请求，不要再次发起。</p>
            <p className="small mono" style={{ overflowWrap: 'anywhere' }}>
              请求 ID：{review.requestId}
            </p>
            <label className="operation-field">
              操作原因
              <textarea
                aria-label="服务重启原因"
                value={reason}
                disabled={busy}
                onChange={(event) => setReason(event.target.value)}
              />
            </label>
            <label className="operation-confirm">
              <input
                type="checkbox"
                checked={confirmed}
                disabled={busy}
                onChange={(event) => setConfirmed(event.target.checked)}
              />
              我已核对主机和精确服务，确认本次重启会中断服务
            </label>
            <ErrorNotice message={error} />
            <button
              className="button primary"
              disabled={
                busy || !confirmed || !reason.trim() || Array.from(reason).length > 200 || scope !== 'admin'
              }
              onClick={() => void execute()}
            >
              {busy ? '等待结果…' : '确认执行本次重启'}
            </button>
          </div>
        </Modal>
      )}
    </Panel>
  )
}
