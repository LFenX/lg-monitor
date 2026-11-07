import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { safeLink } from './CardPreview'
import { useApp } from './state'
import { Empty, ErrorNotice, Panel } from './ui'
import './notifications.css'

export type FeishuChat = { id: string; name: string; memberCount: number | null }
type Permission = { name: string; description: string; required: boolean; granted: boolean }
export type FeishuStep = {
  step: string
  status: string
  message: string
  url?: string
  permissions?: Permission[]
  chats?: FeishuChat[]
}
export type FeishuReport = { steps: FeishuStep[]; botName?: string }

export function officialFeishuURL(value?: string) {
  const link = safeLink(value)
  if (!link) return
  const url = new URL(link)
  if (url.protocol === 'https:' && ['open.feishu.cn', 'open.larksuite.com'].includes(url.hostname))
    return link
}

export function useFeishuReport(app: string, enabled: boolean) {
  const { api } = useApp()
  const [report, setReport] = useState<FeishuReport>(),
    [error, setError] = useState(''),
    [loading, setLoading] = useState(false)
  const [revision, setRevision] = useState(0),
    [newChats, setNewChats] = useState(new Set<string>())
  const known = useRef<{ app: string; ids?: Set<string> }>({ app: '' })
  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    if (known.current.app !== app) {
      known.current = { app }
      setNewChats(new Set())
      setReport(undefined)
    }
    if (!app || !enabled) {
      setLoading(false)
      return
    }
    const load = async () => {
      setLoading(true)
      try {
        const result = await api.request<FeishuReport>('/feishu/check?app=' + encodeURIComponent(app), {
          signal: controller.signal,
        })
        if (controller.signal.aborted) return
        const chatStep = result.steps.find((step) => step.step === 'chat')
        if (chatStep?.status === 'ok' || chatStep?.status === 'action_required') {
          const ids = new Set((chatStep.chats || []).map((chat) => chat.id)),
            previous = known.current.ids
          if (previous)
            setNewChats(
              (current) =>
                new Set(
                  [...current].filter((id) => ids.has(id)).concat([...ids].filter((id) => !previous.has(id))),
                ),
            )
          known.current.ids = ids
        }
        setReport(result)
        setError('')
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '飞书状态读取失败')
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false)
          timer = setTimeout(() => void load(), 5000)
        }
      }
    }
    void load()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [api, app, enabled, revision])
  return { report, error, loading, newChats, refresh: () => setRevision((value) => value + 1) }
}

function StepStatus({ step }: { step?: FeishuStep }) {
  const label =
    step?.status === 'ok'
      ? '已完成'
      : step?.status === 'error'
        ? '出错'
        : step?.status === 'action_required'
          ? '需要你操作'
          : '检查中'
  return <span className={'setup-status setup-status-' + (step?.status || 'loading')}>{label}</span>
}

export function FeishuInspection({
  app,
  enabled,
}: {
  app: { id: string; domain: string }
  enabled: boolean
}) {
  const inspection = useFeishuReport(app.id, enabled)
  return <FeishuInspectionView app={app} enabled={enabled} inspection={inspection} />
}

export function FeishuInspectionView({
  app,
  enabled,
  inspection,
  renderChats,
}: {
  app: { id: string; domain: string }
  enabled: boolean
  inspection: ReturnType<typeof useFeishuReport>
  renderChats?: (chats: FeishuChat[], newChats: Set<string>) => ReactNode
}) {
  const { report, error, loading, newChats, refresh } = inspection
  const step = (name: string) => report?.steps.find((item) => item.step === name)
  const bot = step('bot'),
    permissions = step('permissions'),
    chat = step('chat')
  const credentialsFailed = report && step('credentials')?.status !== 'ok'
  const developerURL = app.domain === 'lark' ? 'https://open.larksuite.com/app' : 'https://open.feishu.cn/app'
  const applyURL = officialFeishuURL(permissions?.url)
  const chats = chat?.chats || []
  return (
    <div className="setup-inspection">
      <ErrorNotice message={error} />
      <ErrorNotice
        message={credentialsFailed ? '应用凭证检查未通过，请先检查应用凭据；后续步骤尚未完成。' : ''}
      />
      <div className="inspection-toolbar">
        <span className="small muted">
          {enabled ? '每 5 秒自动检查；离开本页停止。' : '更换为可配置或管理员访问令牌后检查。'}
        </span>
        <button className="button" disabled={!enabled || loading} onClick={refresh}>
          {loading ? '正在检查…' : '重新检查'}
        </button>
      </div>
      {enabled && !credentialsFailed && (
        <Panel title="2. 检查机器人能力" extra={<StepStatus step={bot} />}>
          <div className="form-body">
            {bot?.status === 'ok' ? (
              <p>
                机器人已启用：<strong>{report?.botName || '应用机器人'}</strong>
              </p>
            ) : (
              <>
                <ol className="numbered-guide">
                  <li>打开开发者后台，进入自己的应用。</li>
                  <li>在“应用能力 → 添加应用能力”中选择“机器人”并启用。</li>
                </ol>
                <a
                  className="button primary"
                  href={officialFeishuURL(bot?.url) || developerURL}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  开启机器人 ↗
                </a>
              </>
            )}
          </div>
        </Panel>
      )}
      {enabled && !credentialsFailed && (
        <Panel title="3. 开通权限" extra={<StepStatus step={permissions} />}>
          <div className="form-body">
            <ul className="permission-list">
              {(permissions?.permissions || [])
                .filter((item) => item.required)
                .map((permission) => (
                  <li key={permission.name}>
                    <span>
                      {permission.name === 'im:message:send_as_bot'
                        ? '发送告警卡片'
                        : permission.name === 'im:chat:read'
                          ? '读取机器人所在的群'
                          : '飞书接口要求的附加权限'}
                      <small>
                        {permission.name === 'im:chat:read'
                          ? '自动发现告警群，无需手动查找群的信息。'
                          : permission.description}
                      </small>
                    </span>
                    <span className={permission.granted ? 'positive' : 'muted'}>
                      {permission.granted ? '已开通' : '未开通'}
                    </span>
                  </li>
                ))}
            </ul>
            {permissions?.status !== 'ok' && (
              <>
                <ol className="numbered-guide">
                  <li>打开权限页面，开通列出的应用身份权限。</li>
                  <li>在“版本管理与发布”创建并发布新版本；企业要求审批时等待管理员审批。</li>
                  <li>返回本页，开通结果会自动刷新。</li>
                </ol>
                <a
                  className="button primary"
                  href={applyURL || developerURL}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {applyURL ? '一键开通权限 ↗' : '打开开发者后台 ↗'}
                </a>
              </>
            )}
          </div>
        </Panel>
      )}
      {enabled && !credentialsFailed && (
        <Panel title="4. 发现告警群" extra={<StepStatus step={chat} />}>
          <div className="form-body">
            <ol className="numbered-guide">
              <li>在飞书里打开接收告警的群。</li>
              <li>进入“设置 → 群机器人 → 添加机器人”。</li>
              <li>搜索“{report?.botName || '应用机器人'}”并添加。</li>
            </ol>
            <p className="small muted">搜不到时，检查应用已发布、本人在应用可用范围内、机器人能力已启用。</p>
            {chats.length && renderChats ? (
              renderChats(chats, newChats)
            ) : chats.length ? (
              <ul className="discovered-chats">
                {chats.map((item) => (
                  <li key={item.id} className={newChats.has(item.id) ? 'new-chat' : ''}>
                    <span>
                      <strong>{item.name || '未命名的群'}</strong>
                      <small>{item.memberCount == null ? '人数未知' : item.memberCount + ' 人'}</small>
                    </span>
                    {newChats.has(item.id) && <span className="soft-label">新发现</span>}
                  </li>
                ))}
              </ul>
            ) : (
              <Empty title={chat?.status === 'error' ? '群列表读取失败' : '尚未发现机器人所在的群'}>
                加入后会自动出现在这里。
              </Empty>
            )}
          </div>
        </Panel>
      )}
      {report && (
        <details className="inspection-details">
          <summary>技术详情</summary>
          <pre>{JSON.stringify(report, null, 2)}</pre>
        </details>
      )}
    </div>
  )
}
