import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Glyph } from './glyphs'
import type { GlyphName } from './glyphs'
import { canConfigure } from './kit'
import { ConnectSteps, Dialog, PromptBox, CopyButton, boardPrompt, childPrompt } from './parts'
import { useApp, useResource } from './state'
import type { ExploreItem, ExploreKind, ExploreResult } from './types'

type Kind = 'port' | 'url' | 'service' | 'process' | 'task'
export type ParentOption = { id: string; name: string; depth: number }

const kinds: { id: Kind; label: string; glyph: GlyphName; explore?: ExploreKind; help: string }[] = [
  {
    id: 'port',
    label: '端口',
    glyph: 'plug',
    explore: 'ports',
    help: '网站、接口、数据库这类在端口上提供服务的程序。端口连不上就算异常。',
  },
  {
    id: 'url',
    label: '网址',
    glyph: 'globe',
    help: '定期打开这个地址；打不开、返回错误或 HTTPS 证书快到期都算异常。',
  },
  {
    id: 'service',
    label: 'Windows 服务',
    glyph: 'service',
    explore: 'services',
    help: '服务停止就算异常，例如 cloudflared、PostgreSQL。',
  },
  {
    id: 'process',
    label: '进程',
    glyph: 'process',
    explore: 'processes',
    help: '这个程序不在运行就算异常。',
  },
  {
    id: 'task',
    label: '计划任务',
    glyph: 'task',
    explore: 'tasks',
    help: '上次运行失败、被禁用或任务不见了就算异常。',
  },
]

function describe(item: ExploreItem): [string, string] {
  switch (item.kind) {
    case 'port':
      return [
        ':' + item.port + (item.process ? '  ' + item.process : ''),
        (item.address || '') + (item.services?.length ? ' · 服务 ' + item.services.join('、') : ''),
      ]
    case 'service':
      return [
        item.displayName || item.name,
        item.name +
          ' · ' +
          (item.state === 'running' ? '运行中' : item.state === 'stopped' ? '已停止' : item.state || ''),
      ]
    case 'task':
      return [item.name, (item.task || '') + (item.result ? ' · ' + item.result : '')]
  }
  const memory = item.rss ? ' · ' + Math.round(item.rss / 1048576) + ' MB' : ''
  return [item.name, 'PID ' + item.pid + memory]
}

export function AddService({
  board,
  parents,
  parent = '',
  close,
}: {
  board?: { id: string; name: string }
  parents: ParentOption[]
  parent?: string
  close: () => void
}) {
  const { api, scope, guide, refresh } = useApp()
  const [kind, setKind] = useState<Kind>('port')
  const [query, setQuery] = useState(''),
    [search, setSearch] = useState('')
  const [picked, setPicked] = useState<ExploreItem>()
  const [url, setUrl] = useState('')
  const [name, setName] = useState(''),
    [named, setNamed] = useState(false)
  const [under, setUnder] = useState(parent)
  const [computer, setComputer] = useState('')
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const spec = kinds.find((item) => item.id === kind)!
  const computers = guide?.collectors || []
  useEffect(() => {
    const timer = setTimeout(() => setSearch(query.trim()), 250)
    return () => clearTimeout(timer)
  }, [query])
  const list = useResource(
    (signal) => {
      if (!spec.explore) return Promise.resolve(undefined)
      const params = new URLSearchParams({ kind: spec.explore, limit: '80' })
      if (search) params.set('q', search)
      if (computer) params.set('collector', computer)
      return api.request<ExploreResult>('/explore?' + params.toString(), { signal })
    },
    [kind, search, computer],
    kind + '|' + search + '|' + computer,
  )
  const choose = (next: Kind) => {
    setKind(next)
    setPicked(undefined)
    setQuery('')
    setSearch('')
    if (!named) setName('')
  }
  const pick = (item: ExploreItem) => {
    setPicked(item)
    if (!named) setName(item.suggest?.name || item.displayName || item.name)
  }
  if (!canConfigure(scope)) {
    const target = parents.find((item) => item.id === parent)
    const prompt = target ? childPrompt(target) : boardPrompt(board || { id: 'main', name: '我的服务' })
    return (
      <Dialog title="添加服务" close={close}>
        <p className="lm-muted" style={{ margin: 0 }}>
          这个页面现在只能查看。把下面这段话交给已接入的
          Agent，它会替你加好；或者在设置里输入一次可配置令牌，之后就能在这里直接点选添加。
        </p>
        <PromptBox text={prompt} />
        <div className="lm-prompt-bar">
          <span className="lm-muted lm-small">把【】里的内容换成你要监控的服务。</span>
          <CopyButton text={prompt} primary label="复制这段话" />
        </div>
        <ConnectSteps compact />
      </Dialog>
    )
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const body: Record<string, unknown> = { name: name.trim() }
    if (board) body.board = board.id
    if (under) body.parent = under
    if (computer) body.collector = computer
    if (kind === 'url') body.url = url.trim()
    else if (picked?.suggest) Object.assign(body, { ...picked.suggest, name: name.trim() })
    else return setError('先在列表里选一个要监控的对象')
    if (!name.trim()) return setError('给它起个名字，例如“博客网站”')
    setBusy(true)
    setError('')
    api
      .request('/monitors', { method: 'POST', body: JSON.stringify(body) })
      .then(() => {
        refresh()
        close()
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '添加失败'))
      .finally(() => setBusy(false))
  }
  const items = list.value?.items || []
  return (
    <Dialog
      title={under ? '添加子服务' : '添加服务'}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close} type="button">
            取消
          </button>
          <button className="lm-btn primary" form="lm-add-service" disabled={busy}>
            {busy ? '正在添加…' : '添加'}
          </button>
        </>
      }
    >
      <form id="lm-add-service" onSubmit={submit} style={{ display: 'grid', gap: 16 }}>
        <div className="lm-kinds" role="group" aria-label="要监控什么">
          {kinds.map((item) => (
            <button
              type="button"
              key={item.id}
              className="lm-kind"
              aria-pressed={kind === item.id}
              onClick={() => choose(item.id)}
            >
              <Glyph name={item.glyph} size={20} />
              {item.label}
            </button>
          ))}
        </div>
        <p className="lm-muted lm-small" style={{ margin: '-4px 0 0' }}>
          {spec.help}
        </p>
        {computers.length > 1 && spec.explore && (
          <label className="lm-field">
            哪台电脑
            <select
              className="lm-input"
              value={computer}
              onChange={(event) => setComputer(event.target.value)}
            >
              <option value="">自动（只有一台在线时）</option>
              {computers.map((host) => (
                <option key={host.id} value={host.id}>
                  {host.hostname || host.id}
                  {host.online ? '' : '（离线）'}
                </option>
              ))}
            </select>
          </label>
        )}
        {kind === 'url' ? (
          <label className="lm-field">
            网址
            <input
              className="lm-input"
              type="url"
              required
              placeholder="https://example.com/health"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
            />
          </label>
        ) : (
          <div style={{ display: 'grid', gap: 8 }}>
            <input
              className="lm-input"
              type="search"
              placeholder={
                '搜索' +
                spec.label +
                '，例如 ' +
                (kind === 'port'
                  ? '3000、node'
                  : kind === 'service'
                    ? 'cloudflared'
                    : kind === 'task'
                      ? 'backup'
                      : 'nginx')
              }
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              aria-label={'搜索' + spec.label}
            />
            <div className="lm-pick" role="radiogroup" aria-label={spec.label + '列表'}>
              {list.error ? (
                <p className="lm-pick-empty">{list.error}</p>
              ) : !list.value && list.loading ? (
                <p className="lm-pick-empty">正在读取这台电脑上的{spec.label}…</p>
              ) : items.length === 0 ? (
                <p className="lm-pick-empty">{list.value?.notes?.[0] || '没有找到，换个关键词试试'}</p>
              ) : (
                items.map((item) => {
                  const [title, detail] = describe(item)
                  const taken = !!item.monitoredBy
                  const key =
                    item.kind +
                    ':' +
                    (item.task || item.name) +
                    ':' +
                    (item.port || '') +
                    ':' +
                    (item.pid || '')
                  return (
                    <label key={key} aria-disabled={taken || undefined}>
                      <input
                        type="radio"
                        name="pick"
                        disabled={taken}
                        checked={picked === item}
                        onChange={() => pick(item)}
                      />
                      <span className="name">
                        <strong>{title}</strong>
                        <small>{detail}</small>
                      </span>
                      <span className="tag">{taken ? '已监控' : ''}</span>
                    </label>
                  )
                })
              )}
            </div>
            {list.value?.notes?.length && items.length > 0 ? (
              <p className="lm-muted lm-small" style={{ margin: 0 }}>
                {list.value.notes.join(' ')}
              </p>
            ) : null}
          </div>
        )}
        <div className="lm-grid-2">
          <label className="lm-field">
            名字
            <input
              className="lm-input"
              required
              maxLength={80}
              placeholder="例如：博客网站"
              value={name}
              onChange={(event) => {
                setName(event.target.value)
                setNamed(true)
              }}
            />
          </label>
          <label className="lm-field">
            挂在哪个服务下面
            <select className="lm-input" value={under} onChange={(event) => setUnder(event.target.value)}>
              <option value="">不挂，作为主服务</option>
              {parents
                .filter((item) => item.depth < 2)
                .map((item) => (
                  <option key={item.id} value={item.id}>
                    {'　'.repeat(item.depth) + item.name}
                  </option>
                ))}
            </select>
          </label>
        </div>
      </form>
    </Dialog>
  )
}
