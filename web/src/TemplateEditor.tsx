import { useEffect, useMemo, useRef, useState } from 'react'
import './notifications.css'
import { APIError } from './api'
import { CardPreview } from './CardPreview'
import { FeishuTrial } from './FeishuTrial'
import { useApp, useResource } from './state'
import { Empty, ErrorNotice, Panel } from './ui'
import { colors, fields, initialRaw, states, variables } from './templates'
import type { PreviewResult, TemplateDefinition } from './templates'

const presets = new Set(['standard', 'compact', 'detailed', 'digest'])
const names: Record<string, string> = {
  id: '模板代号',
  name: '模板名称',
  extends: '基础预设',
  title: '标题',
  colors: '标题颜色',
  fields: '内置字段',
  extraFields: '附加字段',
  mention: '提醒谁',
  buttons: '按钮',
  note: '底部备注',
  raw: '卡片 JSON',
}

export function fieldErrors(error: APIError, source: string, definition: TemplateDefinition) {
  const result: Record<string, string> = {},
    lines = source.split('\n')
  for (const issue of error.issues) {
    for (let index = Math.min(issue.line - 1, lines.length - 1); index >= 0; index--) {
      const match = /^  "([^"]+)":/.exec(lines[index])
      if (match && names[match[1]]) {
        result[match[1]] = issue.message
        break
      }
    }
  }
  const expression = /at <([^>]+)>/.exec(error.message)?.[1]
  if (expression)
    for (const [key, value] of Object.entries(definition))
      if (names[key] && JSON.stringify(value)?.includes(expression)) result[key] = error.message
  for (const [pattern, key] of [
    [/模板 ID|预设.*覆盖/, 'id'],
    [/extends/, 'extends'],
    [/标题颜色/, 'colors'],
    [/内置字段/, 'fields'],
    [/mention|用户标识/, 'mention'],
    [/按钮/, 'buttons'],
    [/raw|JSON/, 'raw'],
  ] as [RegExp, string][])
    if (pattern.test(error.message)) result[key] = error.message
  return result
}

export function TemplateEditor({ id }: { id: string }) {
  const { api, scope, refresh } = useApp()
  const source = useResource(
    (signal) =>
      api.request<TemplateDefinition>('/templates/' + encodeURIComponent(id === 'new' ? 'standard' : id), {
        signal,
      }),
    [api, id],
  )
  const [definition, setDefinition] = useState<TemplateDefinition>()
  const [mode, setMode] = useState<'basic' | 'raw'>('basic'),
    [raw, setRaw] = useState(initialRaw)
  const [preview, setPreview] = useState<PreviewResult[]>(),
    [previewState, setPreviewState] = useState(0)
  const [error, setError] = useState(''),
    [issues, setIssues] = useState<Record<string, string>>({})
  const [pending, setPending] = useState(false),
    [busy, setBusy] = useState(false),
    [saved, setSaved] = useState('')
  const [trial, setTrial] = useState(false)
  const rawInput = useRef<HTMLTextAreaElement>(null)
  const action = useRef<AbortController | undefined>(undefined)
  const editable = scope === 'config' || scope === 'admin'
  useEffect(() => () => action.current?.abort(), [])
  useEffect(() => {
    if (!source.value) return
    const value = structuredClone(source.value)
    if (id === 'new' || presets.has(id)) {
      value.id = ''
      value.name = id === 'new' ? '' : (value.name || id) + '自定义'
    }
    setDefinition(value)
    setMode(value.raw == null ? 'basic' : 'raw')
    if (value.raw != null) setRaw(JSON.stringify(value.raw, null, 2))
  }, [source.value, id])
  const draft = useMemo(() => {
    if (!definition) return { text: '', value: undefined, error: '' }
    try {
      const value: TemplateDefinition =
        mode === 'raw'
          ? { id: definition.id || 'editor-preview', name: definition.name, raw: JSON.parse(raw) }
          : {
              ...definition,
              id: definition.id || 'editor-preview',
              raw: undefined,
              extends: definition.extends || 'standard',
            }
      if (
        mode === 'raw' &&
        (value.raw == null || Array.isArray(value.raw) || !['object', 'string'].includes(typeof value.raw))
      )
        throw new Error('卡片 JSON 必须是对象或 JSON 模板字符串')
      return { value, text: JSON.stringify(value, null, 2), error: '' }
    } catch (err) {
      return {
        value: undefined,
        text: '',
        error:
          err instanceof SyntaxError
            ? 'JSON 语法错误：' + err.message
            : err instanceof Error
              ? err.message
              : 'JSON 格式无效',
      }
    }
  }, [definition, mode, raw])
  useEffect(() => {
    const controller = new AbortController()
    setPreview(undefined)
    setError('')
    setIssues({})
    setSaved('')
    if (!draft.value) {
      setPending(false)
      return
    }
    setPending(true)
    const timer = setTimeout(async () => {
      try {
        const path = '/templates/' + encodeURIComponent(draft.value!.id) + '/preview'
        const firing = await api.request<PreviewResult>(path, {
          method: 'POST',
          body: draft.text,
          signal: controller.signal,
        })
        const resolved = await api.request<PreviewResult>(path, {
          method: 'POST',
          body: JSON.stringify({ template: draft.value, state: 'resolved' }),
          signal: controller.signal,
        })
        if (!controller.signal.aborted) setPreview([firing, resolved])
      } catch (err) {
        if (!controller.signal.aborted) {
          setError(err instanceof Error ? err.message : '预览校验失败')
          if (err instanceof APIError)
            setIssues(mode === 'raw' ? { raw: err.message } : fieldErrors(err, draft.text, draft.value!))
        }
      } finally {
        if (!controller.signal.aborted) setPending(false)
      }
    }, 350)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [api, draft.text, draft.error])
  const save = async () => {
    if (!definition || !draft.value || !editable) return
    if (!/^[a-z0-9-]+$/.test(definition.id) || presets.has(definition.id)) {
      setIssues({ id: '请填写由小写字母、数字或连字符组成的自定义模板代号' })
      return
    }
    const controller = new AbortController()
    action.current = controller
    setBusy(true)
    setSaved('')
    setError('')
    try {
      await api.request('/templates/' + encodeURIComponent(definition.id) + '/preview', {
        method: 'POST',
        body: draft.text,
        signal: controller.signal,
      })
      await api.request('/templates/' + encodeURIComponent(definition.id), {
        method: 'PUT',
        body: draft.text,
        signal: controller.signal,
      })
      if (!controller.signal.aborted) {
        setSaved('模板已校验并保存。')
        refresh()
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        setError(err instanceof Error ? err.message : '模板保存失败')
        if (err instanceof APIError)
          setIssues(mode === 'raw' ? { raw: err.message } : fieldErrors(err, draft.text, draft.value))
      }
    } finally {
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  const insert = (variable: string) => {
    const input = rawInput.current
    const start = input?.selectionStart ?? raw.length,
      end = input?.selectionEnd ?? raw.length
    const token = '{{' + variable + '}}'
    setRaw(raw.slice(0, start) + token + raw.slice(end))
    requestAnimationFrame(() => {
      input?.focus()
      input?.setSelectionRange(start + token.length, start + token.length)
    })
  }
  if (source.error) return <ErrorNotice message={source.error} />
  if (!definition) return <Empty title="正在读取模板" />
  const change = (value: TemplateDefinition) => setDefinition(value)
  return (
    <>
      <div className="breadcrumbs">
        <a href="#/notifications">通知</a>
        <span>/</span>
        <span>模板编辑</span>
      </div>
      <div className="page-heading">
        <div>
          <h1>{presets.has(id) || id === 'new' ? '自定义卡片模板' : '编辑卡片模板'}</h1>
          <p className="muted">先预览触发和恢复效果，再保存或试发。</p>
        </div>
        <a className="button" href="#/notifications">
          返回通知
        </a>
      </div>
      {!editable && (
        <div className="notice">当前为只读访问，可编辑草稿并预览；保存和试发需要可配置权限。</div>
      )}
      <ErrorNotice message={error} />
      {saved && (
        <div className="notice notice-success" role="status">
          {saved}
        </div>
      )}
      <div className="template-editor">
        <div className="editor-form">
          <Panel title="模板设置">
            <div className="form-body">
              <label className="form-field">
                模板名称
                <input
                  value={definition.name || ''}
                  onChange={(event) => change({ ...definition, name: event.target.value })}
                />
              </label>
              <label className="form-field">
                模板代号
                <input
                  value={definition.id}
                  aria-label="模板代号"
                  placeholder="例如 oncall"
                  aria-invalid={!!issues.id}
                  onChange={(event) => change({ ...definition, id: event.target.value })}
                />
                <FieldError value={issues.id} />
                <small className="muted">只用小写字母、数字和连字符；预设模板另存为自定义模板。</small>
              </label>
              <div className="mode-tabs" role="group" aria-label="编辑模式">
                <button className={mode === 'basic' ? 'active' : ''} onClick={() => setMode('basic')}>
                  基础模式
                </button>
                <button className={mode === 'raw' ? 'active' : ''} onClick={() => setMode('raw')}>
                  高级模式
                </button>
              </div>
              <p className="small muted">两种模式分别保留本页草稿，只保存当前模式的内容。</p>
              {mode === 'basic' ? (
                <BasicEditor value={definition} change={change} issues={issues} />
              ) : (
                <>
                  <label className="form-field">
                    卡片 JSON
                    <textarea
                      className="code-editor"
                      aria-label="卡片 JSON"
                      ref={rawInput}
                      spellCheck={false}
                      value={raw}
                      aria-invalid={!!draft.error || !!issues.raw}
                      onChange={(event) => setRaw(event.target.value)}
                      rows={22}
                    />
                    <FieldError value={draft.error || issues.raw} />
                  </label>
                  <a href="https://open.feishu.cn/cardkit" target="_blank" rel="noopener noreferrer">
                    去飞书卡片搭建工具设计 ↗
                  </a>
                  <p className="small muted">
                    可粘贴完整卡片对象，或包含 firing、resolved 的两个状态对象。这里只编辑 raw 内容。
                  </p>
                </>
              )}
              <details className="variable-help">
                <summary>模板变量与用法</summary>
                <p className="small">
                  文字变量使用双花括号；时间已格式化。列表可用 join，range 仅支持
                  Alerts、Processes、Target.Tags，不能嵌套。
                </p>
                <div>
                  {variables.map(([value, label]) => (
                    <div key={value}>
                      <span>{label}</span>
                      {mode === 'raw' ? (
                        <button className="variable-token" onClick={() => insert(value)}>
                          {'{{' + value + '}}'}
                        </button>
                      ) : (
                        <code>{'{{' + value + '}}'}</code>
                      )}
                    </div>
                  ))}
                </div>
              </details>
              <div className="editor-actions">
                <button
                  className="button primary"
                  disabled={!editable || busy || pending || !!draft.error || !draft.value}
                  onClick={() => void save()}
                >
                  {busy ? '正在校验并保存…' : '校验并保存模板'}
                </button>
                <button
                  className="button"
                  disabled={!editable || busy || pending || !!draft.error || !preview || !draft.value}
                  onClick={() => setTrial(true)}
                >
                  发送到测试群
                </button>
              </div>
            </div>
          </Panel>
        </div>
        <div className="editor-preview">
          <Panel
            title="实时预览"
            extra={
              <div className="mode-tabs" role="group" aria-label="预览状态">
                <button className={!previewState ? 'active' : ''} onClick={() => setPreviewState(0)}>
                  触发
                </button>
                <button className={previewState ? 'active' : ''} onClick={() => setPreviewState(1)}>
                  恢复
                </button>
              </div>
            }
          >
            <div className="form-body">
              {pending ? (
                <p role="status" className="muted">
                  正在后端校验预览…
                </p>
              ) : preview ? (
                <>
                  {preview[previewState].warning && (
                    <div className="notice">{preview[previewState].warning}</div>
                  )}
                  <CardPreview card={preview[previewState].card} label="当前模板预览" />
                </>
              ) : (
                <Empty title={draft.error || error ? '修正字段后显示预览' : '等待模板内容'} />
              )}
            </div>
          </Panel>
        </div>
      </div>
      {trial && draft.value && (
        <FeishuTrial
          draft={draft.value}
          state={previewState ? 'resolved' : 'firing'}
          close={() => setTrial(false)}
          invalid={(err) => {
            setError(err.message)
            setIssues(mode === 'raw' ? { raw: err.message } : fieldErrors(err, draft.text, draft.value!))
          }}
        />
      )}
    </>
  )
}

function FieldError({ value }: { value?: string }) {
  return value ? <span className="field-error">{value}</span> : null
}

function BasicEditor({
  value,
  change,
  issues,
}: {
  value: TemplateDefinition
  change: (value: TemplateDefinition) => void
  issues: Record<string, string>
}) {
  const { api } = useApp()
  const [mentionText, setMentionText] = useState<Record<string, string>>(() => {
    const result: Record<string, string> = {}
    for (const [state, mention] of Object.entries(value.mention || {}))
      if (Array.isArray(mention)) result[state] = mention.join(', ')
    return result
  })
  const base = useResource(
    (signal) =>
      api.request<TemplateDefinition>('/templates/' + encodeURIComponent(value.extends || 'standard'), {
        signal,
      }),
    [api, value.extends],
  )
  const selected = value.fields ?? base.value?.fields ?? []
  const update = (key: keyof TemplateDefinition, next: unknown) => change({ ...value, [key]: next })
  const order = (index: number, offset: number) => {
    const next = [...selected],
      target = index + offset
    ;[next[index], next[target]] = [next[target], next[index]]
    update('fields', next)
  }
  return (
    <>
      <label className="form-field">
        基础预设
        <select
          aria-label="基础预设"
          value={value.extends || 'standard'}
          onChange={(event) => update('extends', event.target.value)}
        >
          <option value="standard">标准</option>
          <option value="compact">简洁</option>
          <option value="detailed">排查</option>
          <option value="digest">汇总</option>
        </select>
        <FieldError value={issues.extends} />
      </label>
      <ErrorNotice message={base.error} />
      <label className="form-field">
        标题
        <input
          aria-label="标题"
          value={value.title ?? ''}
          placeholder={base.value?.title || '沿用预设标题'}
          aria-invalid={!!issues.title}
          onChange={(event) => update('title', event.target.value)}
        />
        <FieldError value={issues.title} />
      </label>
      <fieldset
        className="form-section"
        aria-invalid={!!issues.colors}
        disabled={value.extends === 'compact'}
      >
        <legend>标题颜色</legend>
        {value.extends === 'compact' && <p className="small muted">简洁卡片没有标题栏，不使用标题颜色。</p>}
        <div className="color-fields">
          {states.map(([state, label]) => (
            <label key={state}>
              {label}
              <select
                value={value.colors?.[state] || base.value?.colors?.[state] || 'blue'}
                onChange={(event) => update('colors', { ...value.colors, [state]: event.target.value })}
              >
                {colors.map(([color, name]) => (
                  <option key={color} value={color}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
        <FieldError value={issues.colors} />
      </fieldset>
      <fieldset
        className="form-section"
        aria-invalid={!!issues.fields}
        disabled={value.extends === 'compact' || value.extends === 'digest'}
      >
        <legend>内置字段与顺序</legend>
        <p className="small muted">标准和排查卡片显示这些字段；简洁和汇总沿用各自布局。</p>
        <div className="field-choices">
          {fields.map(([key, label]) => (
            <label key={key}>
              <input
                type="checkbox"
                checked={selected.includes(key)}
                onChange={(event) =>
                  update(
                    'fields',
                    event.target.checked ? [...selected, key] : selected.filter((field) => field !== key),
                  )
                }
              />
              {label}
            </label>
          ))}
        </div>
        <ol className="ordered-fields">
          {selected.map((field, index) => (
            <li key={field}>
              <span>{fields.find(([key]) => key === field)?.[1] || field}</span>
              <button
                className="button"
                disabled={!index}
                aria-label={'上移' + (fields.find(([key]) => key === field)?.[1] || field)}
                onClick={() => order(index, -1)}
              >
                ↑
              </button>
              <button
                className="button"
                disabled={index === selected.length - 1}
                aria-label={'下移' + (fields.find(([key]) => key === field)?.[1] || field)}
                onClick={() => order(index, 1)}
              >
                ↓
              </button>
            </li>
          ))}
        </ol>
        <FieldError value={issues.fields} />
      </fieldset>
      <fieldset className="form-section" aria-invalid={!!issues.extraFields}>
        <legend>附加字段</legend>
        {(value.extraFields || []).map((field, index) => (
          <div className="editable-row" key={index}>
            <label>
              字段名称
              <input
                value={field.label}
                onChange={(event) =>
                  update(
                    'extraFields',
                    value.extraFields!.map((item, i) =>
                      i === index ? { ...item, label: event.target.value } : item,
                    ),
                  )
                }
              />
            </label>
            <label>
              字段内容
              <input
                value={field.value}
                onChange={(event) =>
                  update(
                    'extraFields',
                    value.extraFields!.map((item, i) =>
                      i === index ? { ...item, value: event.target.value } : item,
                    ),
                  )
                }
              />
            </label>
            <button
              className="button"
              aria-label={'移除附加字段' + (index + 1)}
              onClick={() =>
                update(
                  'extraFields',
                  value.extraFields!.filter((_, i) => i !== index),
                )
              }
            >
              移除
            </button>
          </div>
        ))}
        <button
          className="button"
          onClick={() => update('extraFields', [...(value.extraFields || []), { label: '', value: '' }])}
        >
          添加附加字段
        </button>
        <FieldError value={issues.extraFields} />
      </fieldset>
      <fieldset className="form-section" aria-invalid={!!issues.mention}>
        <legend>提醒谁</legend>
        {states.map(([state, label]) => {
          const mention = value.mention?.[state],
            kind = mention === 'all' ? 'all' : Array.isArray(mention) ? 'users' : 'none'
          return (
            <div className="mention-row" key={state}>
              <label>
                {label}时
                <select
                  value={kind}
                  onChange={(event) => {
                    const next = { ...value.mention }
                    if (event.target.value === 'none') delete next[state]
                    else
                      next[state] =
                        event.target.value === 'all'
                          ? 'all'
                          : (mentionText[state] || '').split(/[,，\s]+/).filter(Boolean)
                    update('mention', next)
                  }}
                >
                  <option value="none">不提醒成员</option>
                  <option value="all">所有人</option>
                  <option value="users">指定成员</option>
                </select>
              </label>
              {kind === 'users' && (
                <label>
                  成员标识
                  <input
                    value={mentionText[state] || ''}
                    placeholder="多个成员以逗号分隔"
                    onChange={(event) => {
                      setMentionText((current) => ({ ...current, [state]: event.target.value }))
                      update('mention', {
                        ...value.mention,
                        [state]: event.target.value.split(/[,，\s]+/).filter(Boolean),
                      })
                    }}
                  />
                </label>
              )}
            </div>
          )
        })}
        <FieldError value={issues.mention} />
      </fieldset>
      <fieldset className="form-section" aria-invalid={!!issues.buttons}>
        <legend>按钮</legend>
        {(value.buttons || []).map((button, index) => (
          <div className="editable-row" key={index}>
            <label>
              按钮文字
              <input
                value={button.text}
                onChange={(event) =>
                  update(
                    'buttons',
                    value.buttons!.map((item, i) =>
                      i === index ? { ...item, text: event.target.value } : item,
                    ),
                  )
                }
              />
            </label>
            <label>
              跳转地址
              <input
                value={button.url}
                placeholder="https://"
                onChange={(event) =>
                  update(
                    'buttons',
                    value.buttons!.map((item, i) =>
                      i === index ? { ...item, url: event.target.value } : item,
                    ),
                  )
                }
              />
            </label>
            <button
              className="button"
              aria-label={'移除按钮' + (index + 1)}
              onClick={() =>
                update(
                  'buttons',
                  value.buttons!.filter((_, i) => i !== index),
                )
              }
            >
              移除
            </button>
          </div>
        ))}
        <button
          className="button"
          onClick={() => update('buttons', [...(value.buttons || []), { text: '', url: '' }])}
        >
          添加按钮
        </button>
        <FieldError value={issues.buttons} />
      </fieldset>
      <label className="form-field">
        底部备注
        <textarea
          aria-label="底部备注"
          rows={3}
          value={value.note || ''}
          aria-invalid={!!issues.note}
          onChange={(event) => update('note', event.target.value)}
        />
        <FieldError value={issues.note} />
      </label>
    </>
  )
}
