import { useEffect, useRef, useState } from 'react'
import { APIError } from './api'
import { FeishuInspectionView, useFeishuReport } from './FeishuInspection'
import type { FeishuChat } from './FeishuInspection'
import { useApp, useResource } from './state'
import { Empty, ErrorNotice, Panel } from './ui'
import type { Notifier, Template } from './types'
import './notifications.css'

type App = { id: string; appId: string; domain: string }
type Rule = { id: string; name?: string; severity?: string }
export type Confirmation = { ok: boolean; message: string }

export function FeishuSetup() {
  const { api, data, scope, version, refresh } = useApp()
  const apps = data?.notifiers.feishuApps || []
  const [selected, setSelected] = useState(''),
    [created, setCreated] = useState<App>()
  const [newApp, setNewApp] = useState(false),
    [appId, setAppId] = useState(''),
    [secret, setSecret] = useState(''),
    [domain, setDomain] = useState('feishu')
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const [groups, setGroups] = useState<string[]>([])
  const action = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => action.current?.abort(), [])
  useEffect(() => {
    if (!selected && apps.length) setSelected(apps[0].id)
  }, [apps, selected])
  const current = apps.find((app) => app.id === selected) || (created?.id === selected ? created : undefined)
  const editable = scope === 'config' || scope === 'admin'
  const inspection = useFeishuReport(current?.id || '', editable && !newApp)
  const source = useResource(
    async (signal) => {
      const [rules, templates] = await Promise.all([
        api.request<{ rules?: Rule[] }>('/config/rules', { signal }),
        api.request<Template[]>('/templates', { signal }),
      ])
      return { rules: rules.rules || [], templates }
    },
    [api, version],
  )
  const chats = inspection.report?.steps.find((step) => step.step === 'chat')?.chats || []
  const ready = inspection.report?.steps.find((step) => step.step === 'chat')?.status === 'ok'
  const saveApp = async () => {
    const controller = new AbortController()
    action.current = controller
    setBusy(true)
    setError('')
    let id = 'default'
    for (let suffix = 2; apps.some((app) => app.id === id); suffix++) id = 'app-' + suffix
    try {
      const result = await api.request<Confirmation>('/feishu/apps', {
        method: 'POST',
        signal: controller.signal,
        body: JSON.stringify({ id, appId: appId.trim(), domain, secret }),
      })
      if (controller.signal.aborted) return
      if (!result.ok) {
        setError(result.message)
        return
      }
      setCreated({ id, appId: appId.trim(), domain })
      setSelected(id)
      setNewApp(false)
      setGroups([])
      refresh()
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '应用保存失败')
    } finally {
      if (!controller.signal.aborted) {
        setBusy(false)
        setSecret('')
      }
    }
  }
  return (
    <>
      <div className="breadcrumbs">
        <a href="#/notifications">通知</a>
        <span>/</span>
        <span>接入飞书</span>
      </div>
      <div className="page-heading">
        <div>
          <h1>接入飞书</h1>
          <p className="muted">完成五步接入；每个群确认收到测试卡片后才保存为通知渠道。</p>
        </div>
      </div>
      <Panel
        title="1. 填写应用凭据"
        extra={
          <span className="setup-status">
            {error ? '出错' : current && !newApp ? '已完成' : '需要你操作'}
          </span>
        }
      >
        <div className="form-body">
          {apps.length > 0 && (
            <label>
              使用已接入的应用
              <select
                aria-label="应用"
                value={newApp ? '__new' : selected}
                onChange={(event) => {
                  setNewApp(event.target.value === '__new')
                  setSelected(event.target.value === '__new' ? '' : event.target.value)
                  setGroups([])
                }}
              >
                {apps.map((app) => (
                  <option key={app.id} value={app.id}>
                    {app.id}
                  </option>
                ))}
                <option value="__new">接入另一个应用</option>
              </select>
            </label>
          )}
          {(!current || newApp) &&
            (scope === 'admin' ? (
              <form
                onSubmit={(event) => {
                  event.preventDefault()
                  void saveApp()
                }}
              >
                <ol className="numbered-guide">
                  <li>在飞书开发者后台创建企业自建应用。</li>
                  <li>在“凭证与基础信息”复制应用的 App ID 和 App Secret。</li>
                </ol>
                <a
                  className="button"
                  href={domain === 'lark' ? 'https://open.larksuite.com/app' : 'https://open.feishu.cn/app'}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  打开开发者后台 ↗
                </a>
                <label>
                  平台
                  <select value={domain} onChange={(event) => setDomain(event.target.value)} disabled={busy}>
                    <option value="feishu">飞书</option>
                    <option value="lark">Lark</option>
                  </select>
                </label>
                <label>
                  App ID
                  <input
                    required
                    value={appId}
                    disabled={busy}
                    onChange={(event) => setAppId(event.target.value)}
                    autoComplete="off"
                  />
                </label>
                <label>
                  App Secret
                  <input
                    required
                    type="password"
                    value={secret}
                    disabled={busy}
                    onChange={(event) => setSecret(event.target.value)}
                    autoComplete="new-password"
                  />
                </label>
                <p className="small muted">凭据加密保存在监控器电脑上；提交后清空此处的 Secret。</p>
                <button className="button primary" disabled={busy || !appId.trim() || !secret}>
                  {busy ? '正在验证…' : '验证并保存应用'}
                </button>
              </form>
            ) : (
              <div className="notice">
                <strong>需要管理员令牌</strong>
                <p>在监控器电脑上用管理员身份运行以下命令，再用生成的令牌更换页面访问权限：</p>
                <pre>
                  <code>lgmon token create --scope admin</code>
                </pre>
                <p>也可以直接在命令行完成接入：</p>
                <pre>
                  <code>lgmon feishu setup</code>
                </pre>
              </div>
            ))}
          {current && !newApp && <p className="muted">应用凭据已加密保存，接下来检查机器人、权限和群。</p>}
          {!editable && (
            <p className="notice">
              检查和配置已有应用需要可配置或管理员令牌。可通过页面右上角“访问权限”更换。
            </p>
          )}
          <ErrorNotice message={error} />
        </div>
      </Panel>
      {current && !newApp && (
        <FeishuInspectionView
          app={current}
          enabled={editable}
          inspection={inspection}
          renderChats={(available, newChats) => (
            <ul className="discovered-chats">
              {available.map((chat) => (
                <li key={chat.id} className={newChats.has(chat.id) ? 'new-chat' : ''}>
                  <label className="chat-choice">
                    <input
                      type="checkbox"
                      checked={groups.includes(chat.id)}
                      onChange={(event) =>
                        setGroups((values) =>
                          event.target.checked ? [...values, chat.id] : values.filter((id) => id !== chat.id),
                        )
                      }
                    />
                    <span>
                      <strong>{chat.name || '未命名的群'}</strong>
                      <small>{chat.memberCount == null ? '人数未知' : chat.memberCount + ' 人'}</small>
                    </span>
                  </label>
                  {newChats.has(chat.id) && <span className="soft-label">新发现</span>}
                </li>
              ))}
            </ul>
          )}
        />
      )}
      <Panel
        title="5. 发送测试并确认收件"
        extra={
          <span className="setup-status">
            {current &&
            groups.length > 0 &&
            groups.every((id) =>
              data?.notifiers.notifiers?.some(
                (channel) =>
                  channel.type === 'feishu_app' && channel.app === current.id && channel.chatId === id,
              ),
            )
              ? '已完成'
              : '需要你操作'}
          </span>
        }
      >
        <ErrorNotice message={source.error} />
        {current && ready && editable && source.value ? (
          <div className="form-body">
            {groups.length === 0 && (
              <Empty title="先在上一步勾选接收告警的群">可以选择多个群，各自设置模板和告警范围。</Empty>
            )}
            {chats
              .filter((chat) => groups.includes(chat.id))
              .map((chat) => (
                <GroupSetup
                  key={current.id + ':' + chat.id}
                  app={current.id}
                  chat={chat}
                  templates={source.value!.templates}
                  rules={source.value!.rules}
                  existing={data?.notifiers.notifiers?.find(
                    (channel) =>
                      channel.type === 'feishu_app' &&
                      channel.app === current.id &&
                      channel.chatId === chat.id,
                  )}
                />
              ))}
          </div>
        ) : (
          <Empty title="完成前面的应用、权限和选群步骤后继续" />
        )}
      </Panel>
    </>
  )
}

function GroupSetup({
  app,
  chat,
  templates,
  rules,
  existing,
}: {
  app: string
  chat: FeishuChat
  templates: Template[]
  rules: Rule[]
  existing?: Notifier
}) {
  const { api, refresh } = useApp()
  const initialPolicy = existing?.filter
    ? existing.filter.tags?.length || existing.filter.collectors?.length
      ? 'keep'
      : existing.filter.severity?.length === 1 && existing.filter.severity[0] === 'critical'
        ? 'critical'
        : existing.filter.severity?.length === 2 &&
            ['critical', 'warning'].every((s) => existing.filter!.severity!.includes(s))
          ? 'warning'
          : existing.filter.severity?.length
            ? 'keep'
            : 'all'
    : 'all'
  const [template, setTemplate] = useState(existing?.template || 'standard'),
    [policy, setPolicy] = useState(initialPolicy)
  const [selected, setSelected] = useState(rules.map((rule) => rule.id))
  const [receipt, setReceipt] = useState(false),
    [missed, setMissed] = useState(false),
    [done, setDone] = useState(false),
    [partial, setPartial] = useState(false)
  const [busy, setBusy] = useState(''),
    [message, setMessage] = useState(''),
    [error, setError] = useState(''),
    [cooldown, setCooldown] = useState(0)
  const action = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => action.current?.abort(), [])
  useEffect(() => {
    if (!cooldown) return
    const timer = setTimeout(() => setCooldown((value) => value - 1), 1000)
    return () => clearTimeout(timer)
  }, [cooldown])
  const incompatible = rules.filter(
    (rule) =>
      selected.includes(rule.id) &&
      (policy === 'critical'
        ? rule.severity !== 'critical'
        : policy === 'warning'
          ? !['critical', 'warning'].includes(rule.severity || '')
          : false),
  )
  const send = async () => {
    const controller = new AbortController()
    action.current = controller
    setBusy('send')
    setReceipt(false)
    setMissed(false)
    setMessage('')
    setError('')
    try {
      const result = await api.request<Confirmation>('/feishu/test', {
        method: 'POST',
        signal: controller.signal,
        body: JSON.stringify({ app, chatId: chat.id, template, state: 'firing' }),
      })
      if (controller.signal.aborted) return
      setMessage(result.message)
      setReceipt(result.ok)
      setCooldown(10)
    } catch (err) {
      if (!controller.signal.aborted) {
        setError(err instanceof Error ? err.message : '测试发送失败')
        if (err instanceof APIError && err.status === 429) setCooldown(10)
      }
    } finally {
      if (!controller.signal.aborted) setBusy('')
    }
  }
  const save = async () => {
    if (!receipt) return
    const controller = new AbortController()
    action.current = controller
    setBusy('save')
    setError('')
    const filter =
      policy === 'keep'
        ? undefined
        : policy === 'critical'
          ? { severity: ['critical'] }
          : policy === 'warning'
            ? { severity: ['critical', 'warning'] }
            : existing?.filter
              ? {}
              : undefined
    try {
      const result = await api.request<Confirmation>('/feishu/notifiers', {
        method: 'POST',
        signal: controller.signal,
        body: JSON.stringify({ app, chatId: chat.id, template, filter, rules: selected }),
      })
      if (controller.signal.aborted) return
      setMessage(result.message)
      setPartial(!result.ok)
      setDone(true)
      setReceipt(false)
      refresh()
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '群保存失败')
    } finally {
      if (!controller.signal.aborted) setBusy('')
    }
  }
  return (
    <section className="group-setup" aria-label={chat.name + '的通知设置'}>
      <h3>{chat.name}</h3>
      {error && <span className="setup-status setup-status-error">出错</span>}
      {done ? (
        <>
          <p className={partial ? 'notice' : 'notice notice-success'} role="status">
            {message}
          </p>
          <a className="button" href={partial ? '#/notifications/rules' : '#/notifications'}>
            {partial ? '去规则通知设置中添加' : '查看通知渠道'}
          </a>
          <button
            className="button"
            onClick={() => {
              setDone(false)
              setMessage('')
            }}
          >
            继续修改
          </button>
        </>
      ) : (
        <>
          <label>
            默认卡片模板
            <select
              value={template}
              disabled={!!busy}
              onChange={(event) => {
                setTemplate(event.target.value)
                setReceipt(false)
                setMissed(false)
                setMessage('')
              }}
            >
              {templates.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name || item.id}
                </option>
              ))}
            </select>
          </label>
          <label>
            告警范围
            <select value={policy} disabled={!!busy} onChange={(event) => setPolicy(event.target.value)}>
              {initialPolicy === 'keep' && <option value="keep">保留已有筛选条件</option>}
              <option value="all">全部告警</option>
              <option value="critical">只要严重告警</option>
              <option value="warning">严重和警告</option>
            </select>
          </label>
          <fieldset className="form-section">
            <legend>把这个群加入哪些规则</legend>
            {rules.length ? (
              rules.map((rule) => (
                <label className="chat-choice" key={rule.id}>
                  <input
                    type="checkbox"
                    checked={selected.includes(rule.id)}
                    disabled={!!busy}
                    onChange={(event) =>
                      setSelected((ids) =>
                        event.target.checked ? [...ids, rule.id] : ids.filter((id) => id !== rule.id),
                      )
                    }
                  />
                  {rule.name || rule.id}
                </label>
              ))
            ) : (
              <p className="small muted">尚无规则。保存后可在规则通知设置里添加。</p>
            )}
          </fieldset>
          {!!incompatible.length && (
            <p className="notice">
              所选规则的级别不在告警范围内，请取消勾选：
              {incompatible.map((rule) => rule.name || rule.id).join('、')}。
            </p>
          )}
          <ErrorNotice message={error} />
          {message && (
            <p className="notice" role="status">
              {message}
            </p>
          )}
          <button className="button primary" disabled={!!busy || cooldown > 0} onClick={() => void send()}>
            {busy === 'send' ? '正在发送…' : cooldown ? cooldown + ' 秒后可重发' : '发送测试卡片'}
          </button>
          {receipt && (
            <div className="receipt-confirmation">
              <p>
                <strong>群里收到了吗？</strong>
              </p>
              <button
                className="button primary"
                disabled={!!busy || !!incompatible.length}
                onClick={() => void save()}
              >
                {busy === 'save' ? '正在保存…' : '收到了，保存'}
              </button>
              <button
                className="button"
                disabled={!!busy}
                onClick={() => {
                  setReceipt(false)
                  setMissed(true)
                }}
              >
                没收到
              </button>
            </div>
          )}
          {missed && (
            <div className="notice">
              <p>尚未保存这个群。请依次检查：</p>
              <ol className="numbered-guide">
                <li>选中的群是否正确，是否已把机器人加入这个群。</li>
                <li>开发者后台是否启用了机器人能力。</li>
                <li>应用是否已发布，权限是否已生效。</li>
              </ol>
              <p>倒计时结束后可重新发送。</p>
            </div>
          )}
        </>
      )}
    </section>
  )
}
