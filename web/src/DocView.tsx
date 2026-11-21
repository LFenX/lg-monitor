import { Children, cloneElement, isValidElement, useMemo } from 'react'
import type { ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { DependencyMap, neighbourhood } from './DependencyMap'
import { Glyph } from './glyphs'
import { useCopy } from './kit'
import { RoleIcon, roleOf } from './roles'
import type { Monitor } from './types'
import { useMonitorHref } from './shared'
import './doc.css'

/*
 * A service's document, laid out like product documentation: one calm column of prose, a contents list on the
 * right, code blocks with a copy button and callouts for notes and warnings. [[service-id]] becomes a live link
 * to that service and a ```deps block draws the dependency map in place.
 */

const servicePrefix = '#/monitors/'

/* [[id]] and [[id|text]] outside code become links the renderer turns into service chips. */
export function linkServices(markdown: string) {
  return markdown
    .split(/(```[\s\S]*?```|`[^`\n]*`)/)
    .map((part, index) =>
      index % 2
        ? part
        : part.replace(/\[\[([a-z0-9-]+)(?:\|([^\]\n]+))?\]\]/g, (_, id: string, text?: string) => {
            return '[' + (text || id).replace(/[[\]]/g, '') + '](' + servicePrefix + id + ')'
          }),
    )
    .join('')
}

/* Headings for the contents list, skipping anything inside code fences. */
export function headings(markdown: string) {
  const result: { level: number; text: string; id: string }[] = []
  let fenced = false
  for (const line of markdown.split('\n')) {
    if (/^\s*```/.test(line)) fenced = !fenced
    const match = !fenced && /^(##|###)\s+(.+?)\s*#*\s*$/.exec(line)
    if (match) {
      const text = match[2].replace(/[*_`[\]]/g, '')
      result.push({ level: match[1].length, text, id: slug(text, result.length) })
    }
  }
  return result
}

function slug(text: string, index: number) {
  return (
    'doc-' +
    index +
    '-' +
    text
      .toLowerCase()
      .replace(/[^\p{L}\p{N}]+/gu, '-')
      .slice(0, 40)
  )
}

function plain(children: ReactNode): string {
  return Children.toArray(children)
    .map((child) =>
      typeof child === 'string' || typeof child === 'number'
        ? String(child)
        : isValidElement<{ children?: ReactNode }>(child)
          ? plain(child.props.children)
          : '',
    )
    .join('')
}

const gfmCallouts: Record<
  string,
  { tone: string; glyph: 'bell' | 'sparkle' | 'bolt' | 'check'; label: string }
> = {
  NOTE: { tone: 'note', glyph: 'check', label: '说明' },
  TIP: { tone: 'tip', glyph: 'sparkle', label: '提示' },
  IMPORTANT: { tone: 'danger', glyph: 'bolt', label: '重要' },
  WARNING: { tone: 'warn', glyph: 'bell', label: '注意' },
  CAUTION: { tone: 'danger', glyph: 'bolt', label: '警告' },
}

/* Strip a leading [!TAG] marker from the first text node of a GitHub-style alert blockquote. */
function stripAlertMarker(children: ReactNode, marker: string): ReactNode {
  let stripped = false
  const walk = (node: ReactNode): ReactNode =>
    Children.map(node, (child) => {
      if (stripped) return child
      if (typeof child === 'string') {
        const idx = child.indexOf(marker)
        if (idx >= 0) {
          stripped = true
          return (child.slice(0, idx) + child.slice(idx + marker.length)).replace(/^\s+/, '')
        }
        return child
      }
      if (isValidElement<{ children?: ReactNode }>(child) && child.props.children !== undefined) {
        return cloneElement(child, undefined, walk(child.props.children))
      }
      return child
    })
  return walk(children)
}

const callouts: Record<string, { tone: string; glyph: 'bell' | 'sparkle' | 'bolt' | 'check' }> = {
  注意: { tone: 'warn', glyph: 'bell' },
  警告: { tone: 'danger', glyph: 'bolt' },
  重要: { tone: 'danger', glyph: 'bolt' },
  提示: { tone: 'tip', glyph: 'sparkle' },
  说明: { tone: 'note', glyph: 'check' },
  Note: { tone: 'note', glyph: 'check' },
  Tip: { tone: 'tip', glyph: 'sparkle' },
  Warning: { tone: 'warn', glyph: 'bell' },
}

function CodeBlock({ language, code }: { language: string; code: string }) {
  const { copied, copy } = useCopy()
  return (
    <div className="doc-code">
      <div className="doc-code-bar">
        <span>{language || '文本'}</span>
        <button type="button" onClick={() => copy(code)} aria-label="复制代码">
          <Glyph name={copied ? 'check' : 'copy'} size={14} />
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  )
}

function ServiceChip({ monitor, id, children }: { monitor?: Monitor; id: string; children: ReactNode }) {
  const href = useMonitorHref()
  if (!monitor)
    return (
      <span className="doc-service" data-missing="" title={'没有找到服务 ' + id}>
        {children}
      </span>
    )
  const state = monitor.state
  return (
    <a className="doc-service" href={href(id)} data-state={state} title={monitor.description || monitor.name}>
      <RoleIcon role={roleOf(monitor)} size={18} title="" />
      {children}
      <i aria-label={state === 'up' ? '正常' : state === 'down' ? '异常' : '未知'} />
    </a>
  )
}

export default function DocView({
  markdown,
  monitors,
  focus,
}: {
  markdown: string
  monitors: Monitor[]
  focus?: string
}) {
  const byID = useMemo(() => new Map(monitors.map((monitor) => [monitor.id, monitor])), [monitors])
  const source = useMemo(() => linkServices(markdown), [markdown])
  const toc = useMemo(() => headings(markdown), [markdown])
  const scrollTo = (id: string) =>
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  const titled =
    (Tag: 'h2' | 'h3') =>
    ({ children }: { children?: ReactNode }) => {
      const text = plain(children).replace(/[*_`[\]]/g, '')
      const id = toc.find((entry) => entry.level === (Tag === 'h2' ? 2 : 3) && entry.text === text)?.id
      return (
        <Tag id={id}>
          {children}
          {id && (
            <button
              type="button"
              className="doc-anchor"
              onClick={() => scrollTo(id)}
              aria-label={'定位到「' + plain(children) + '」'}
            >
              #
            </button>
          )}
        </Tag>
      )
    }
  return (
    <div className="doc" data-toc={toc.length > 1 || undefined}>
      <article className="doc-body">
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          skipHtml
          components={{
            h1: ({ children }) => <h2 className="doc-h1">{children}</h2>,
            h2: titled('h2'),
            h3: titled('h3'),
            a: ({ href, children }) => {
              if (typeof href === 'string' && href.startsWith(servicePrefix)) {
                const id = decodeURIComponent(href.slice(servicePrefix.length))
                return (
                  <ServiceChip id={id} monitor={byID.get(id)}>
                    {/* [[id]] without its own text reads as the service's name. */}
                    {plain(children) === id && byID.get(id) ? byID.get(id)!.name : children}
                  </ServiceChip>
                )
              }
              const safe = typeof href === 'string' && /^(https?:\/\/|#\/)/.test(href)
              return safe ? (
                <a href={href} target={href!.startsWith('#/') ? undefined : '_blank'} rel="noreferrer">
                  {children}
                </a>
              ) : (
                <span>{children}</span>
              )
            },
            pre: ({ children }) => <>{children}</>,
            code: ({ className, children }) => {
              const language = /language-([\w-]+)/.exec(className || '')?.[1]
              const text = plain(children)
              const block = language !== undefined || text.includes('\n')
              if (!block) return <code>{children}</code>
              if (language === 'deps') {
                const named = text.split(/[\s,，、]+/).filter((id) => byID.has(id))
                const ids =
                  named.length > 1 ? new Set(named) : neighbourhood(monitors, named[0] || focus || '')
                if (ids.size === 0) return <p className="doc-muted">（依赖图：没有找到要画的服务）</p>
                return (
                  <div className="doc-map">
                    <DependencyMap
                      monitors={monitors}
                      ids={ids}
                      focus={named.length > 1 ? undefined : named[0] || focus}
                    />
                  </div>
                )
              }
              return <CodeBlock language={language || ''} code={text.replace(/\n$/, '')} />
            },
            blockquote: ({ children }) => {
              const first = plain(children).trim()
              const gfm = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\](?:\s|$)/i.exec(first)
              if (gfm) {
                const key = gfm[1].toUpperCase()
                const meta = gfmCallouts[key]
                return (
                  <aside className="doc-callout" data-tone={meta.tone}>
                    <Glyph name={meta.glyph} size={16} />
                    <div>
                      <p className="doc-callout-title">{meta.label}</p>
                      {stripAlertMarker(children, gfm[0].trimEnd())}
                    </div>
                  </aside>
                )
              }
              const word = Object.keys(callouts).find((key) => first.startsWith(key))
              const meta = word ? callouts[word] : { tone: 'quote', glyph: undefined }
              return (
                <aside className="doc-callout" data-tone={meta.tone}>
                  {meta.glyph && <Glyph name={meta.glyph} size={16} />}
                  <div>{children}</div>
                </aside>
              )
            },
            table: ({ children }) => (
              <div className="doc-table">
                <table>{children}</table>
              </div>
            ),
            img: () => null,
          }}
        >
          {source}
        </ReactMarkdown>
      </article>
      {toc.length > 1 && (
        <nav className="doc-toc" aria-label="本页内容">
          <p>本页内容</p>
          {toc.map((entry) => (
            <button type="button" key={entry.id} data-level={entry.level} onClick={() => scrollTo(entry.id)}>
              {entry.text}
            </button>
          ))}
        </nav>
      )}
    </div>
  )
}
