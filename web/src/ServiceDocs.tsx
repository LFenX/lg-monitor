/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { Suspense, lazy, useState } from 'react'
import { useGate } from './access'
import { AgentCard } from './AgentCard'
import { DependencyMap, neighbourhood } from './DependencyMap'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { Dialog, docPrompt, useAgentSetup } from './parts'
import { RelationLine, RoleIcon, relations, roleOf } from './roles'
import { useMonitorHref, useMonitors, useShared } from './shared'
import { useApp, useResource } from './state'
import type { Dependency, Monitor, MonitorDetail } from './types'

const DocView = lazy(() => import('./DocView'))

/* What this service uses and what uses it, as a small map and two plain lists. */
export function ServiceRelations({ detail }: { detail: MonitorDetail }) {
  const monitors = useMonitors()
  const href = useMonitorHref()
  const monitor = detail.monitor
  const depends = monitor.depends || []
  const dependents = monitor.dependents || []
  if (!depends.length && !dependents.length) return null
  const all = monitors.some((item) => item.id === monitor.id)
    ? monitors.map((item) => (item.id === monitor.id ? monitor : item))
    : [monitor, ...monitors]
  const byID = new Map(all.map((item) => [item.id, item]))
  const list = (title: string, items: Dependency[], outward: boolean) =>
    items.length > 0 && (
      <div className="lm-deps-list">
        <h3>{title}</h3>
        <ul>
          {items.map((item) => {
            const other = byID.get(item.id)
            return (
              <li key={item.id}>
                <span className="relation" data-relation={item.relation}>
                  <RelationLine relation={item.relation} optional={item.optional} />
                  {(relations[item.relation] || relations.uses)[outward ? 'verb' : 'name']}
                  {item.optional && <em className="optional">可选</em>}
                </span>
                <a href={href(item.id)}>
                  {other && <RoleIcon role={roleOf(other)} size={20} title="" />}
                  {other?.name || item.id}
                </a>
                {item.note && <small>{item.note}</small>}
              </li>
            )
          })}
        </ul>
      </div>
    )
  return (
    <section className="lm-section" aria-label="依赖关系">
      <header>
        <h2 className="lm-h2">依赖关系</h2>
        <small>它用到了谁、谁用到了它；只用来看，不影响告警</small>
      </header>
      <DependencyMap
        monitors={all}
        ids={neighbourhood(all, monitor.id)}
        focus={monitor.id}
        label={monitor.name + ' 的上下游'}
      />
      <div className="lm-deps">
        {list('它用到', depends, true)}
        {list('用到它的服务', dependents, false)}
      </div>
    </section>
  )
}

function EditDoc({
  monitor,
  text,
  close,
}: {
  monitor: Monitor
  text: string
  close: (saved: boolean) => void
}) {
  const { api, monitors, refresh } = useApp()
  const [draft, setDraft] = useState(text),
    [preview, setPreview] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const save = (markdown: string) => {
    setBusy(true)
    setError('')
    api
      .request('/monitors/' + encodeURIComponent(monitor.id) + '/doc', {
        method: 'PUT',
        body: JSON.stringify({ markdown }),
      })
      .then(() => {
        refresh()
        close(true)
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
      .finally(() => setBusy(false))
  }
  return (
    <Dialog
      title={'「' + monitor.name + '」的文档'}
      full
      close={() => close(false)}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          {text && (
            <button className="lm-btn danger" disabled={busy} onClick={() => save('')}>
              删除文档
            </button>
          )}
          <button className="lm-btn" onClick={() => close(false)}>
            取消
          </button>
          <button className="lm-btn primary" disabled={busy} onClick={() => save(draft)}>
            {busy ? '正在保存…' : '保存'}
          </button>
        </>
      }
    >
      {/* Side by side on a wide window: write on the left, the finished page on the right as you type. */}
      <div className="lm-seg lm-doc-tabs" role="group" aria-label="编辑或预览">
        <button aria-pressed={!preview} onClick={() => setPreview(false)}>
          <Glyph name="edit" size={15} />
          编辑
        </button>
        <button aria-pressed={preview} onClick={() => setPreview(true)}>
          <Glyph name="eye" size={15} />
          预览
        </button>
      </div>
      <div className="lm-doc-split" data-preview={preview || undefined}>
        <textarea
          className="lm-input lm-doc-editor"
          aria-label="文档（Markdown）"
          value={draft}
          spellCheck={false}
          onChange={(event) => setDraft(event.target.value)}
          placeholder={
            '# ' +
            monitor.name +
            '\n\n一段话说清它是做什么的。\n\n## 依赖\n\n用到 [[服务ID]]。\n\n```deps\n```\n\n## 排障\n\n> **注意** 重启前先确认……'
          }
        />
        <div className="lm-doc-preview" aria-label="预览">
          <Suspense fallback={<p className="lm-muted">正在准备预览…</p>}>
            <DocView markdown={draft} monitors={monitors} focus={monitor.id} />
          </Suspense>
        </div>
      </div>
      <p className="lm-small lm-muted" style={{ margin: 0 }}>
        Markdown：## 分节会出现在右侧目录；[[服务ID]] 显示成服务链接；语言写 deps 的代码块画依赖图；“&gt;
        **注意**”开头是提示框。不要写密钥。
      </p>
    </Dialog>
  )
}

/* The service's document, when it has one; otherwise a quiet way to start one. */
export function ServiceDoc({ monitor }: { monitor: Monitor }) {
  const { api, pulse, scope } = useApp()
  const monitors = useMonitors()
  const shared = useShared()
  const { lang, t } = useI18n()
  const gate = useGate()
  const setup = useAgentSetup('all')
  const [editing, setEditing] = useState(false)
  const [version, setVersion] = useState(0)
  const resource = useResource(
    (signal) =>
      monitor.hasDoc
        ? api.request<{ markdown: string }>(
            (shared ? '/lan/remote/' + encodeURIComponent(shared.id) : '') +
              '/monitors/' +
              encodeURIComponent(monitor.id) +
              '/doc',
            { signal },
          )
        : Promise.resolve({ markdown: '' }),
    [pulse, monitor.hasDoc, version],
    monitor.id,
  )
  const text = resource.value?.markdown || ''
  const edit = () => gate('config', '编辑文档', () => setEditing(true))
  return (
    <section className="lm-section lm-doc-section" aria-label="文档">
      <header className="lm-doc-head">
        <div>
          <h2 className="lm-h2">文档</h2>
          {!text && <small>部署在哪、怎么启动、依赖谁、出问题先查什么，都可以写在这里</small>}
        </div>
        {!shared && (text || scope !== 'read') && (
          <button className="lm-btn small" onClick={edit}>
            <Glyph name={text ? 'edit' : 'plus'} size={15} />
            {text ? '编辑' : '写文档'}
          </button>
        )}
      </header>
      {text ? (
        <Suspense fallback={<p className="lm-muted">{t('serviceDocOpening')}</p>}>
          <DocView markdown={text} monitors={monitors} focus={monitor.id} />
        </Suspense>
      ) : resource.error ? (
        <p className="lm-note" data-tone="down">
          {resource.error}
        </p>
      ) : (
        !monitor.hasDoc &&
        (shared ? (
          <p className="lm-muted lm-small" style={{ margin: 0 }}>
            {t('serviceDocEmptyRemote', { owner: shared.ownerName })}
          </p>
        ) : (
          <AgentCard
            quiet
            status={false}
            title={t('serviceDocAgentTitle')}
            lead={t('serviceDocAgentLead')}
            text={docPrompt(monitor, setup, lang)}
          />
        ))
      )}
      {editing && (
        <EditDoc
          monitor={monitor}
          text={text}
          close={(saved) => {
            setEditing(false)
            if (saved) setVersion((value) => value + 1)
          }}
        />
      )}
    </section>
  )
}
