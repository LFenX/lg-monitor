import { useEffect, useState } from 'react'
import './notifications.css'
import { CardPreview } from './CardPreview'
import { useApp, useResource } from './state'
import { Empty, ErrorNotice, Panel } from './ui'
import type { Template } from './types'
import type { PreviewResult } from './templates'
import { ruleTemplatePatch } from './templates'

export function TemplateLibrary() {
  const { api, version } = useApp()
  const [revision, setRevision] = useState(0)
  const resource = useResource(
    (signal) => api.request<Template[]>('/templates', { signal }),
    [api, version, revision],
  )
  return (
    <Panel
      title="卡片模板"
      className="template-library"
      extra={
        <div className="inline">
          <button className="button" onClick={() => setRevision((value) => value + 1)}>
            刷新模板
          </button>
          <a className="button primary" href="#/notifications/templates/new">
            新建模板
          </a>
        </div>
      }
    >
      <ErrorNotice message={resource.error} />
      {resource.value?.length ? (
        <div className="preview-library">
          {resource.value.map((entry) => (
            <TemplateCard entry={entry} revision={revision} key={entry.id} />
          ))}
        </div>
      ) : (
        <Empty title={resource.loading ? '正在读取模板' : '尚无卡片模板'} />
      )}
    </Panel>
  )
}

function TemplateCard({ entry, revision }: { entry: Template; revision: number }) {
  const { api } = useApp()
  const [preview, setPreview] = useState<PreviewResult[]>()
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    setError('')
    Promise.all(
      ['firing', 'resolved'].map((state) =>
        api.request<PreviewResult>('/templates/' + encodeURIComponent(entry.id) + '/preview', {
          method: 'POST',
          body: JSON.stringify({ state }),
          signal: controller.signal,
        }),
      ),
    )
      .then((value) => {
        if (!controller.signal.aborted) setPreview(value)
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '预览读取失败')
      })
    return () => controller.abort()
  }, [api, entry.id, revision])
  return (
    <article className="template-card">
      <div className="template-card-heading">
        <div>
          <h3>{entry.name || entry.id}</h3>
          <span className="soft-label small">{entry.preset ? '预设模板' : '自定义模板'}</span>
        </div>
        <a className="button" href={'#/notifications/templates/' + encodeURIComponent(entry.id)}>
          {entry.preset ? '基于此模板自定义' : '编辑模板'}
        </a>
      </div>
      <ErrorNotice message={error} />
      {preview ? (
        <div className="preview-pair">
          {preview.map((value, index) => (
            <div key={index}>
              <p className="preview-state">{index ? '恢复' : '触发'}</p>
              {value.warning && <p className="notice small">{value.warning}</p>}
              <CardPreview
                card={value.card}
                label={(entry.name || entry.id) + ' · ' + (index ? '恢复预览' : '触发预览')}
              />
            </div>
          ))}
        </div>
      ) : (
        !error && (
          <p className="muted preview-loading" role="status">
            正在读取触发和恢复预览
          </p>
        )
      )}
    </article>
  )
}

export function RuleTemplates() {
  const { api, scope, version, refresh } = useApp()
  const [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [success, setSuccess] = useState('')
  const resource = useResource(
    async (signal) => {
      const [rules, templates] = await Promise.all([
        api.request<{ rules?: Record<string, unknown>[] }>('/config/rules', { signal }),
        api.request<Template[]>('/templates', { signal }),
      ])
      return { rules: rules.rules || [], templates }
    },
    [api, version],
  )
  const editable = scope === 'config' || scope === 'admin'
  const save = async (id: string, template: string) => {
    setBusy(id)
    setError('')
    setSuccess('')
    try {
      const current = await api.request<{ rules?: Record<string, unknown>[] }>('/config/rules')
      await api.request('/config/rules', {
        method: 'PUT',
        body: JSON.stringify({
          rules: ruleTemplatePatch(current.rules || [], id, template),
        }),
      })
      setSuccess('已更新该规则的卡片模板。')
      refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : '规则模板保存失败')
    } finally {
      setBusy('')
    }
  }
  return (
    <Panel
      title="规则的卡片模板"
      extra={!editable && <span className="small muted">只读；更换访问令牌后可配置</span>}
    >
      <ErrorNotice message={error || resource.error} />
      {success && (
        <p className="notice notice-success" role="status">
          {success}
        </p>
      )}
      {resource.value?.rules.length ? (
        <div className="rule-template-list">
          {resource.value.rules.map((rule) => (
            <label key={String(rule.id)}>
              <span>
                <strong>{String(rule.name || rule.id)}</strong>
                <span className="small muted">{String(rule.id)}</span>
              </span>
              <select
                aria-label={String(rule.name || rule.id) + '的卡片模板'}
                value={String(rule.template || '')}
                disabled={!editable || !!busy}
                onChange={(event) => void save(String(rule.id), event.target.value)}
              >
                <option value="">跟随群默认模板</option>
                {resource.value!.templates.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name || item.id}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
      ) : (
        <Empty title={resource.loading ? '正在读取规则' : '尚无告警规则'} />
      )}
    </Panel>
  )
}

export function RuleNotifications() {
  const { api, data, scope, version, refresh } = useApp()
  const editable = scope === 'config' || scope === 'admin'
  const source = useResource(
    (signal) => api.request<{ rules?: Record<string, unknown>[] }>('/config/rules', { signal }),
    [api, version],
  )
  const [selected, setSelected] = useState<Record<string, string[]>>({}),
    [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [success, setSuccess] = useState('')
  const channels = data?.notifiers.notifiers || []
  const save = async (id: string) => {
    setBusy(id)
    setError('')
    setSuccess('')
    try {
      const current = await api.request<{ rules?: Record<string, unknown>[] }>('/config/rules')
      if (!current.rules?.some((rule) => rule.id === id)) throw new Error('规则已不存在，请刷新后重试')
      await api.request('/config/rules', {
        method: 'PUT',
        body: JSON.stringify({
          ...current,
          rules: current.rules.map((rule) => (rule.id === id ? { ...rule, notify: selected[id] } : rule)),
        }),
      })
      setSuccess('规则的通知渠道已更新。')
      refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : '规则通知保存失败')
    } finally {
      setBusy('')
    }
  }
  return (
    <>
      <div className="breadcrumbs">
        <a href="#/notifications">通知</a>
        <span>/</span>
        <span>规则通知设置</span>
      </div>
      <div className="page-heading">
        <div>
          <h1>规则通知设置</h1>
          <p className="muted">选择每条规则的通知渠道；不修改规则的触发条件。</p>
        </div>
      </div>
      <Panel title="通知渠道">
        <div className="form-body">
          <ErrorNotice message={error || source.error} />
          {success && (
            <p role="status" className="notice notice-success">
              {success}
            </p>
          )}
          {!editable && <p className="notice">当前为只读访问；保存需要可配置权限。</p>}
          {source.value?.rules?.length ? (
            source.value.rules.map((rule) => {
              const id = String(rule.id),
                recipients = selected[id] || (Array.isArray(rule.notify) ? (rule.notify as string[]) : [])
              return (
                <section className="group-setup" key={id}>
                  <h3>{String(rule.name || rule.id)}</h3>
                  {channels.map((channel) => (
                    <label className="chat-choice" key={channel.id}>
                      <input
                        type="checkbox"
                        checked={recipients.includes(channel.id)}
                        disabled={!editable || !!busy}
                        onChange={(event) =>
                          setSelected((values) => ({
                            ...values,
                            [id]: event.target.checked
                              ? [...recipients, channel.id]
                              : recipients.filter((value) => value !== channel.id),
                          }))
                        }
                      />
                      {channel.chatName || channel.id}
                    </label>
                  ))}
                  <p className="small muted">渠道的告警范围必须包含这条规则的级别，否则保存会提示错误。</p>
                  <button
                    className="button primary"
                    disabled={!editable || !!busy || !selected[id]}
                    onClick={() => void save(id)}
                  >
                    {busy === id ? '正在保存…' : '保存通知渠道'}
                  </button>
                </section>
              )
            })
          ) : (
            <Empty title={source.loading ? '正在读取规则' : '尚无告警规则'} />
          )}
        </div>
      </Panel>
      <RuleTemplates />
    </>
  )
}
