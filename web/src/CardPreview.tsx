import type { ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'

type Value = Record<string, unknown>
const object = (value: unknown): Value =>
  value && typeof value === 'object' && !Array.isArray(value) ? (value as Value) : {}
const list = (value: unknown) => (Array.isArray(value) ? value : [])
const text = (value: unknown): string =>
  typeof value === 'string'
    ? value
    : typeof object(value).content === 'string'
      ? String(object(value).content)
      : ''

export function safeLink(value: unknown): string | undefined {
  if (typeof value !== 'string') return
  try {
    const url = new URL(value)
    if ((url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password)
      return url.href
  } catch {
    return
  }
}

function Markdown({ value }: { value: unknown }) {
  const source = text(value).replace(/<at\b[^>]*>.*?<\/at>/gis, (tag) => {
    const match = /\bid\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s>]+))/i.exec(tag)
    return (match?.[1] || match?.[2] || match?.[3]) === 'all' ? '@所有人' : '@指定成员'
  })
  return (
    <div className="card-markdown">
      <ReactMarkdown
        skipHtml
        components={{
          a: ({ children, href }) => (
            <span className="card-link" title={safeLink(href) ? href : '无效链接'}>
              {children}
            </span>
          ),
          img: ({ alt }) => <span className="card-unsupported">[图片：{alt || '暂无预览'}]</span>,
        }}
      >
        {source.replaceAll('<', '&lt;')}
      </ReactMarkdown>
    </div>
  )
}

function Element({ value, depth = 0 }: { value: unknown; depth?: number }): ReactNode {
  const element = object(value)
  const children = (items: unknown) =>
    list(items).map((item, index) => <Element value={item} depth={depth + 1} key={index} />)
  if (depth > 12) return <p className="small muted">预览层级过深，请在飞书中查看。</p>
  switch (element.tag) {
    case 'markdown':
    case 'lark_md':
      return <Markdown value={element.content ?? element.text} />
    case 'plain_text':
      return <div className="card-markdown">{text(element)}</div>
    case 'hr':
      return <hr />
    case 'column_set':
      return <div className="card-columns">{children(element.columns)}</div>
    case 'column':
      return <div className="card-column">{children(element.elements)}</div>
    case 'div':
      return (
        <div>
          {element.text != null && <Markdown value={element.text} />}
          <div className="card-columns">
            {list(element.fields).map((field, index) => (
              <div className="card-column" key={index}>
                <Markdown value={object(field).text} />
              </div>
            ))}
          </div>
          {element.extra != null && <Element value={element.extra} depth={depth + 1} />}
        </div>
      )
    case 'button':
      return (
        <button
          type="button"
          className={'card-button ' + (element.type === 'primary' ? 'card-button-primary' : '')}
          disabled
          title="预览中不执行按钮操作"
        >
          {text(element.text) || '按钮'}
        </button>
      )
    case 'action':
      return <div className="card-actions">{children(element.actions)}</div>
    case 'note':
      return <div className="card-note">{children(element.elements)}</div>
    case 'collapsible_panel':
      return (
        <details className="card-collapse" open={element.expanded === true}>
          <summary>{text(object(element.header).title) || '展开内容'}</summary>
          {children(element.elements)}
        </details>
      )
    default:
      return (
        <div className="card-unsupported">
          暂不预览此组件：{typeof element.tag === 'string' ? element.tag : '未知组件'}
        </div>
      )
  }
}

const colors = new Set([
  'blue',
  'wathet',
  'turquoise',
  'green',
  'yellow',
  'orange',
  'red',
  'carmine',
  'violet',
  'purple',
  'indigo',
  'grey',
])
export function CardPreview({ card, label }: { card: unknown; label: string }) {
  const value = object(card),
    header = object(value.header)
  const color = typeof header.template === 'string' && colors.has(header.template) ? header.template : 'blue'
  return (
    <div className="feishu-card" role="group" aria-label={label}>
      {value.header != null && <div className={'card-header card-color-' + color}>{text(header.title)}</div>}
      <div className="card-body">
        {list(object(value.body).elements ?? value.elements).map((item, index) => (
          <Element value={item} key={index} />
        ))}
      </div>
      <p className="card-disclaimer">以飞书实际显示为准；预览中的按钮不执行操作。</p>
    </div>
  )
}
