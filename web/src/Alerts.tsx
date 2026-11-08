import { useCallback, useEffect, useRef, useState } from 'react'
import { hostLink, targetLink, timestamp, title } from './model'
import { useApp, useResource } from './state'
import { Badge, Empty, ErrorNotice, Icon, Modal, Panel, Range } from './ui'
import type { Alert, AlertEvents } from './types'

export function Alerts() {
  const { api, data, scope, version, now, refresh } = useApp()
  const [state, setState] = useState('firing'),
    [severity, setSeverity] = useState(''),
    [collector, setCollector] = useState(''),
    [search, setSearch] = useState(''),
    [hours, setHours] = useState(24)
  const [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [success, setSuccess] = useState(''),
    [silence, setSilence] = useState<Alert>()
  const [detail, setDetail] = useState<Alert>()
  const resource = useResource(
    (signal) =>
      api.request<Alert[]>(
        '/alerts?state=' +
          state +
          (state === 'firing' || state === 'pending' ? '' : '&since=' + (now() - hours * 3600000)),
        { signal },
      ),
    [api, state, hours, version],
    state + ':' + hours,
  )
  const editable = scope === 'config' || scope === 'admin'
  const alerts = (resource.value || []).filter(
    (alert) =>
      (!severity || alert.severity === severity) &&
      (!collector || alert.collectorId === collector) &&
      (!search ||
        [alert.summary, alert.targetId, title(data?.definitions || [], alert.targetId)]
          .join(' ')
          .toLowerCase()
          .includes(search.toLowerCase())),
  )
  const ack = async (alert: Alert) => {
    setBusy(alert.id)
    setError('')
    setSuccess('')
    try {
      await api.request('/alerts/' + encodeURIComponent(alert.id) + '/ack', { method: 'POST' })
      setSuccess('已确认告警；本次故障的重复提醒已停止。')
      refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : '确认失败，请重试')
    } finally {
      setBusy('')
    }
  }
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">ALERTS</p>
          <h1>告警</h1>
          <p className="muted">跟进故障，确认处理，或在维护期间静音。</p>
        </div>
        {(state === 'resolved' || state === 'all') && <Range hours={hours} change={setHours} />}
      </div>
      {!editable && (
        <div className="notice">
          <Icon name="key" />
          <span>当前为只读访问。确认告警、静音需要配置权限，可在右上角更换访问令牌。</span>
        </div>
      )}
      <ErrorNotice message={resource.error || error} />
      {success && (
        <div className="notice notice-success" role="status">
          <Icon name="check" />
          <span>{success}</span>
        </div>
      )}
      <div className="filters">
        <label>
          <span>状态</span>
          <select value={state} onChange={(event) => setState(event.target.value)}>
            <option value="firing">触发中</option>
            <option value="pending">待定</option>
            <option value="resolved">已恢复</option>
            <option value="all">全部</option>
          </select>
        </label>
        <label>
          <span>严重程度</span>
          <select value={severity} onChange={(event) => setSeverity(event.target.value)}>
            <option value="">全部级别</option>
            <option value="critical">严重</option>
            <option value="warning">警告</option>
            <option value="info">提示</option>
          </select>
        </label>
        <label>
          <span>主机</span>
          <select value={collector} onChange={(event) => setCollector(event.target.value)}>
            <option value="">全部主机</option>
            {data?.collectors.map((host) => (
              <option key={host.id} value={host.id}>
                {host.hostname || host.id}
              </option>
            ))}
          </select>
        </label>
        <label className="search">
          <span className="sr-only">搜索告警</span>
          <input
            type="search"
            value={search}
            placeholder="搜索目标或告警内容"
            onChange={(event) => setSearch(event.target.value)}
          />
        </label>
      </div>
      <Panel title="告警记录" extra={<span className="count">{alerts.length}</span>}>
        {alerts.length ? (
          <div className="table-scroll">
            <table className="alerts-table">
              <thead>
                <tr>
                  <th>告警</th>
                  <th>主机 / 目标</th>
                  <th>状态</th>
                  <th>时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {alerts.map((alert) => (
                  <tr key={alert.id}>
                    <td>
                      <Badge state={alert.severity} />
                      <p className="strong alert-summary">{alert.summary || '监控条件触发'}</p>
                      <span className="muted small">
                        {alert.ruleId}
                        {alert.flapping ? ' · 抖动已合并' : ''}
                      </span>
                    </td>
                    <td>
                      <a href={hostLink(alert.collectorId)}>
                        {data?.collectors.find((host) => host.id === alert.collectorId)?.hostname ||
                          alert.collectorId}
                      </a>
                      <p className="small">
                        <a className="muted" href={targetLink(alert.collectorId, alert.targetId)}>
                          {title(data?.definitions || [], alert.targetId)}
                        </a>
                      </p>
                    </td>
                    <td>
                      <Badge state={alert.state} />
                      {!!alert.acknowledgedAt && <p className="positive small">已确认</p>}
                    </td>
                    <td className="muted small">
                      {timestamp(alert.startedAt)}
                      {alert.resolvedAt > 0 && <p>恢复 {timestamp(alert.resolvedAt)}</p>}
                    </td>
                    <td>
                      <div className="row-actions">
                        <button className="button small-button" onClick={() => setDetail(alert)}>
                          事件详情
                        </button>
                        <button
                          className="button small-button"
                          disabled={
                            !editable ||
                            busy === alert.id ||
                            alert.state !== 'firing' ||
                            !!alert.acknowledgedAt
                          }
                          onClick={() => void ack(alert)}
                        >
                          {busy === alert.id ? '处理中' : alert.acknowledgedAt ? '已确认' : '确认'}
                        </button>
                        <button
                          className="button small-button"
                          disabled={!editable}
                          onClick={() => {
                            setError('')
                            setSilence(alert)
                          }}
                        >
                          静音
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty title={resource.loading ? '正在读取告警' : '没有符合条件的告警'}>
            可调整状态、严重程度或主机筛选。
          </Empty>
        )}
      </Panel>
      {detail && <AlertEventDetails key={detail.id} alert={detail} close={() => setDetail(undefined)} />}
      {silence && (
        <Silence
          alert={silence}
          close={() => setSilence(undefined)}
          complete={() => {
            setSilence(undefined)
            setSuccess('已设置静音。监控仍会记录状态，静音期间不发送匹配的通知。')
            refresh()
          }}
        />
      )}
    </>
  )
}

export function AlertEventDetails({ alert, close }: { alert: Alert; close: () => void }) {
  const { api } = useApp()
  const [page, setPage] = useState<AlertEvents>(),
    [busy, setBusy] = useState(true),
    [error, setError] = useState('')
  const pending = useRef<AbortController | undefined>(undefined)
  const load = useCallback(
    async (cursor?: string) => {
      pending.current?.abort()
      const controller = new AbortController()
      pending.current = controller
      setBusy(true)
      setError('')
      try {
        const result = await api.request<AlertEvents>(
          '/alerts/' +
            encodeURIComponent(alert.id) +
            '/events' +
            (cursor ? '?cursor=' + encodeURIComponent(cursor) : ''),
          { signal: controller.signal },
        )
        if (!controller.signal.aborted)
          setPage((previous) => ({
            ...result,
            events: cursor ? [...(previous?.events || []), ...result.events] : result.events,
          }))
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '读取事件失败，请重试')
      } finally {
        if (!controller.signal.aborted) setBusy(false)
      }
    },
    [api, alert.id],
  )
  useEffect(() => {
    void load()
    return () => pending.current?.abort()
  }, [load])
  return (
    <Modal title="告警事件详情" close={close}>
      <p className="strong">{alert.summary}</p>
      <p className="muted small">
        {alert.collectorId} / {alert.targetId}
      </p>
      <div className="row-actions">
        <button className="button small-button" disabled={busy} onClick={() => void load()}>
          刷新事件
        </button>
        <span className="muted small">{page?.events.length || 0} 条已加载</span>
      </div>
      <ErrorNotice message={error} />
      {page?.from !== null && page?.from !== undefined && (
        <p className="muted small">
          故障采样前后各 60 秒：{timestamp(page.from)} 至 {timestamp(page.to!)}
        </p>
      )}
      {!page && busy && <p role="status">正在读取事件</p>}
      {page && page.from === null && (
        <p className="muted">此告警没有进程或服务故障的原始采样时间，无法确定事件窗口。</p>
      )}
      {page && page.from !== null && !page.events.length && (
        <p className="muted">窗口内尚未收到归属此目标的事件。迟到事件到达后可刷新查看。</p>
      )}
      <div className="eventlog-list">
        {page?.events.map((event) => (
          <article className="eventlog-entry" key={event.ts + ':' + event.channel + ':' + event.recordId}>
            <p className="strong">
              {event.provider} · {event.eventId}
            </p>
            <p className="muted small">
              {timestamp(event.ts)} · {event.channel} · 记录 {event.recordId} · 级别 {event.level}
            </p>
            {event.subject && <p className="small">{event.subject}</p>}
            <p className="eventlog-message">{event.message || '事件没有可用消息文本'}</p>
          </article>
        ))}
      </div>
      {page?.nextCursor && (
        <button className="button" disabled={busy} onClick={() => void load(page.nextCursor!)}>
          {busy ? '读取中' : '加载更多事件'}
        </button>
      )}
    </Modal>
  )
}

function Silence({ alert, close, complete }: { alert: Alert; close: () => void; complete: () => void }) {
  const { api } = useApp(),
    [duration, setDuration] = useState('1h'),
    [reason, setReason] = useState(''),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  return (
    <Modal title="静音告警规则" close={close}>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          setBusy(true)
          setError('')
          void api
            .request('/silences', {
              method: 'POST',
              body: JSON.stringify({ rule: alert.ruleId, for: duration, reason }),
            })
            .then(complete)
            .catch((err: unknown) => setError(err instanceof Error ? err.message : '设置静音失败'))
            .finally(() => setBusy(false))
        }}
      >
        <p className="muted">
          规则 <strong>{alert.ruleId}</strong> 的所有匹配告警将在选定时间内停止通知，状态继续记录。
        </p>
        <ErrorNotice message={error} />
        <label className="field">
          静音时长
          <select value={duration} onChange={(event) => setDuration(event.target.value)}>
            <option value="15m">15 分钟</option>
            <option value="1h">1 小时</option>
            <option value="4h">4 小时</option>
            <option value="24h">24 小时</option>
          </select>
        </label>
        <label className="field">
          原因（选填）
          <input
            value={reason}
            maxLength={500}
            onChange={(event) => setReason(event.target.value)}
            placeholder="例如：计划内维护"
          />
        </label>
        <div className="modal-actions">
          <button className="button" type="button" onClick={close} disabled={busy}>
            取消
          </button>
          <button className="button primary" disabled={busy}>
            {busy ? '正在设置' : '设置静音'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
