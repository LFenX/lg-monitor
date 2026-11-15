import { useEffect, useMemo, useState } from 'react'
import { ServiceCards, ServiceList, Stats, groupItems } from './Board'
import { Glyph } from './glyphs'
import { canConfigure } from './kit'
import { useApp, useResource } from './state'
import { APIError } from './api'
import { dayText } from './status'
import type {
  LANDirectory,
  LANGrant,
  LANIncoming,
  LANOutgoing,
  LANPeer,
  LANRemoteBoard,
  LANState,
} from './types'

type Requests = { incoming: LANIncoming[]; outgoing: LANOutgoing[] }

function short(fingerprint: string) {
  return fingerprint.replace(/-/g, '').slice(0, 16).toUpperCase()
}

/* Ticks every few seconds while the page is open, so a decision on the other side shows up soon. */
function useTick(ms: number) {
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), ms)
    return () => clearInterval(timer)
  }, [ms])
  return tick
}

function Visibility({ state, refresh }: { state: LANState; refresh: () => void }) {
  const { api, scope } = useApp()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [name, setName] = useState(state.name),
    [editing, setEditing] = useState(false)
  const writable = canConfigure(scope)
  const save = (body: Record<string, unknown>) => {
    setBusy(true)
    setError('')
    api
      .request<LANState>('/lan', { method: 'PUT', body: JSON.stringify(body) })
      .then(() => {
        setEditing(false)
        refresh()
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
      .finally(() => setBusy(false))
  }
  return (
    <section className="lm-section" aria-label="内网可见" style={{ marginTop: 0 }}>
      <div className="lm-switch-row">
        <div>
          <h2 className="lm-h2">让内网看到我的面板</h2>
          <p className="lm-muted lm-small" style={{ margin: '6px 0 0' }}>
            打开后，同一局域网里装了 lgmon
            的人能看到你的面板名称和简介；要看具体状态，需要向你申请，你核对配对码后同意。
          </p>
        </div>
        <button
          className="lm-switch"
          role="switch"
          aria-checked={state.visible}
          aria-label="让内网看到我的面板"
          disabled={!writable || busy}
          title={writable ? undefined : '这个页面现在只能查看'}
          onClick={() => save({ visible: !state.visible })}
        >
          <i />
        </button>
      </div>
      {error && <p className="lm-error">{error}</p>}
      {state.visible && (
        <dl className="lm-kv" style={{ marginTop: 16 }}>
          <dt>别人看到的名字</dt>
          <dd>
            {editing ? (
              <form
                style={{ display: 'flex', gap: 8 }}
                onSubmit={(event) => {
                  event.preventDefault()
                  save({ name })
                }}
              >
                <input
                  className="lm-input"
                  style={{ height: 32 }}
                  maxLength={40}
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  aria-label="别人看到的名字"
                />
                <button className="lm-btn small primary" disabled={busy}>
                  保存
                </button>
              </form>
            ) : (
              <>
                {state.name}{' '}
                {writable && (
                  <button className="lm-btn ghost small" onClick={() => setEditing(true)}>
                    改名
                  </button>
                )}
              </>
            )}
          </dd>
          <dt>本机地址</dt>
          <dd>{state.addresses.length ? state.addresses.join('、') : '—'}</dd>
          <dt>指纹</dt>
          <dd style={{ fontFamily: 'var(--mono)' }}>{state.fingerprint}</dd>
        </dl>
      )}
      {state.visible && state.problem && (
        <p className="lm-reason" data-state="unknown">
          {state.problem}
        </p>
      )}
      {state.visible && (
        <p className="lm-muted lm-small" style={{ margin: '12px 0 0' }}>
          第一次打开时 Windows 可能会问是否允许 lgmon 访问网络，请选“允许”（专用网络）。lgmon
          不会自己修改防火墙。
        </p>
      )}
    </section>
  )
}

function Incoming({ items, refresh }: { items: LANIncoming[]; refresh: () => void }) {
  const { api, scope } = useApp()
  const [busy, setBusy] = useState(''),
    [error, setError] = useState('')
  const pending = items.filter((item) => item.status === 'pending')
  if (!pending.length) return null
  const decide = (id: string, action: 'approve' | 'deny') => {
    setBusy(id)
    setError('')
    api
      .request('/lan/requests/' + encodeURIComponent(id) + '/' + action, { method: 'POST' })
      .then(refresh)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '操作失败'))
      .finally(() => setBusy(''))
  }
  return (
    <section className="lm-section" aria-label="收到的申请">
      <header>
        <h2 className="lm-h2">收到的申请</h2>
        <small>先和对方确认他屏幕上的配对码与这里一致，再同意</small>
      </header>
      <ul className="lm-lan-list">
        {pending.map((item) => (
          <li key={item.id}>
            <div>
              <strong>{item.deviceName}</strong>
              <span className="lm-muted lm-small">
                {' '}
                想看「{item.boardName || item.boardId}」 · {item.hostname || item.address}
              </span>
            </div>
            <span className="lm-code-big" aria-label={'配对码 ' + item.code}>
              {item.code}
            </span>
            <div className="lm-actions">
              <button
                className="lm-btn small"
                disabled={!canConfigure(scope) || busy === item.id}
                onClick={() => decide(item.id, 'deny')}
              >
                拒绝
              </button>
              <button
                className="lm-btn small primary"
                disabled={!canConfigure(scope) || busy === item.id}
                onClick={() => decide(item.id, 'approve')}
              >
                配对码一致，同意
              </button>
            </div>
          </li>
        ))}
      </ul>
      {error && <p className="lm-error">{error}</p>}
    </section>
  )
}

function BoardAction({
  peer,
  board,
  outgoing,
  refresh,
}: {
  peer: LANPeer
  board: { id: string; name: string }
  outgoing: LANOutgoing[]
  refresh: () => void
}) {
  const { api, scope } = useApp()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const mine = outgoing.find(
    (item) =>
      short(item.fingerprint) === short(peer.fingerprint) &&
      item.boardId === board.id &&
      item.status !== 'denied' &&
      item.status !== 'expired',
  )
  if (mine?.status === 'approved')
    return (
      <a className="lm-btn small" href={'#/lan/remote/' + encodeURIComponent(mine.id)}>
        打开
        <Glyph name="chevron" size={14} />
      </a>
    )
  if (mine?.status === 'pending')
    return (
      <span className="lm-muted lm-small" aria-label={'等待对方同意，配对码 ' + mine.code}>
        等待对方同意 · 配对码 <strong className="lm-code-inline">{mine.code}</strong>
      </span>
    )
  return (
    <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
      {error && <span className="lm-error">{error}</span>}
      <button
        className="lm-btn small"
        disabled={!canConfigure(scope) || busy}
        onClick={() => {
          setBusy(true)
          setError('')
          api
            .request('/lan/requests', {
              method: 'POST',
              body: JSON.stringify({
                address: peer.address,
                boardId: board.id,
                fingerprint: peer.fingerprint,
              }),
            })
            .then(refresh)
            .catch((err: unknown) => setError(err instanceof Error ? err.message : '申请失败'))
            .finally(() => setBusy(false))
        }}
      >
        申请查看
      </button>
    </span>
  )
}

function Peer({ peer, outgoing, refresh }: { peer: LANPeer; outgoing: LANOutgoing[]; refresh: () => void }) {
  return (
    <article className="lm-peer" aria-label={peer.name || peer.address}>
      <header>
        <Glyph name="computer" />
        <strong>{peer.directory?.name || peer.name || peer.address}</strong>
        <span className="lm-muted lm-small">
          {peer.address.replace('https://', '')} · 指纹 {short(peer.fingerprint).slice(0, 8)}…
        </span>
      </header>
      {peer.problem && (
        <p className="lm-reason" data-state="unknown">
          {peer.problem}
        </p>
      )}
      {peer.directory && peer.directory.boards.length === 0 && (
        <p className="lm-muted lm-small">对方还没有面板。</p>
      )}
      {peer.directory && peer.directory.boards.length > 0 && (
        <ul className="lm-lan-list">
          {peer.directory.boards.map((board) => (
            <li key={board.id}>
              <div>
                <strong>{board.name}</strong>
                <span className="lm-muted lm-small">
                  {' '}
                  {board.services} 个服务{board.children ? '、' + board.children + ' 个子服务' : ''}
                  {board.description ? ' · ' + board.description : ''}
                </span>
              </div>
              <BoardAction peer={peer} board={board} outgoing={outgoing} refresh={refresh} />
            </li>
          ))}
        </ul>
      )}
    </article>
  )
}

function Nearby({ outgoing, refresh }: { outgoing: LANOutgoing[]; refresh: () => void }) {
  const { api } = useApp()
  const [peers, setPeers] = useState<LANPeer[]>(),
    [searching, setSearching] = useState(false),
    [error, setError] = useState('')
  const [address, setAddress] = useState(''),
    [manual, setManual] = useState<LANPeer>(),
    [looking, setLooking] = useState(false)
  const search = () => {
    setSearching(true)
    setError('')
    api
      .request<LANPeer[]>('/lan/discover', { method: 'POST' })
      .then(setPeers)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '搜索失败'))
      .finally(() => setSearching(false))
  }
  useEffect(search, [api])
  return (
    <section className="lm-section" aria-label="附近的电脑">
      <header>
        <h2 className="lm-h2">附近的电脑</h2>
        <button className="lm-btn small" onClick={search} disabled={searching}>
          <Glyph name="refresh" size={14} />
          {searching ? '正在搜索…' : '再搜一次'}
        </button>
      </header>
      {error && <p className="lm-error">{error}</p>}
      {peers && peers.length === 0 && !searching && (
        <p className="lm-muted lm-small" style={{ margin: 0 }}>
          没有找到打开了内网可见的 lgmon。对方需要在自己的“内网”页打开开关；不在同一个局域网（例如用
          Tailscale）时，在下面输入对方地址。
        </p>
      )}
      <div style={{ display: 'grid', gap: 12 }}>
        {peers?.map((peer) => (
          <Peer key={peer.fingerprint + peer.address} peer={peer} outgoing={outgoing} refresh={refresh} />
        ))}
        {manual && <Peer peer={manual} outgoing={outgoing} refresh={refresh} />}
      </div>
      <form
        className="lm-lan-manual"
        onSubmit={(event) => {
          event.preventDefault()
          setLooking(true)
          setError('')
          api
            .request<LANDirectory>('/lan/directory?' + new URLSearchParams({ address }).toString())
            .then((directory) => {
              const value = address.includes('://')
                ? address
                : 'https://' + (address.includes(':') ? address : address + ':7445')
              setManual({
                name: directory.name,
                address: value.replace(/\/$/, ''),
                fingerprint: directory.fingerprint,
                directory,
              })
            })
            .catch((err: unknown) => setError(err instanceof Error ? err.message : '连接失败'))
            .finally(() => setLooking(false))
        }}
      >
        <input
          className="lm-input"
          placeholder="搜不到？输入对方地址，例如 192.168.1.20 或 100.64.0.5"
          value={address}
          onChange={(event) => setAddress(event.target.value)}
          aria-label="对方地址"
          required
        />
        <button className="lm-btn" disabled={looking}>
          {looking ? '正在连接…' : '查看'}
        </button>
      </form>
    </section>
  )
}

function Grants({ grants, refresh }: { grants: LANGrant[]; refresh: () => void }) {
  const { api, scope } = useApp()
  const [error, setError] = useState('')
  return (
    <section className="lm-section" aria-label="谁能看我的面板">
      <header>
        <h2 className="lm-h2">谁能看我的面板</h2>
        <small>撤销后对方下一次刷新就看不到了</small>
      </header>
      {grants.length === 0 ? (
        <p className="lm-muted lm-small" style={{ margin: 0 }}>
          还没有人。
        </p>
      ) : (
        <ul className="lm-lan-list">
          {grants.map((grant) => (
            <li key={grant.id}>
              <div>
                <strong>{grant.deviceName}</strong>
                <span className="lm-muted lm-small">
                  {' '}
                  可以看「{grant.boardName || grant.boardId}」 · {dayText(grant.createdAt)}同意
                </span>
              </div>
              <button
                className="lm-btn small danger"
                disabled={!canConfigure(scope)}
                onClick={() =>
                  api
                    .request('/lan/grants/' + encodeURIComponent(grant.id), { method: 'DELETE' })
                    .then(refresh)
                    .catch((err: unknown) => setError(err instanceof Error ? err.message : '撤销失败'))
                }
              >
                撤销
              </button>
            </li>
          ))}
        </ul>
      )}
      {error && <p className="lm-error">{error}</p>}
    </section>
  )
}

const statusText: Record<string, string> = {
  pending: '等待对方同意',
  approved: '已同意',
  denied: '对方拒绝了',
  expired: '已过期',
  revoked: '对方已撤销',
}

function Sent({ outgoing, refresh }: { outgoing: LANOutgoing[]; refresh: () => void }) {
  const { api, scope } = useApp()
  if (!outgoing.length) return null
  return (
    <section className="lm-section" aria-label="我发出的申请">
      <header>
        <h2 className="lm-h2">我发出的申请</h2>
      </header>
      <ul className="lm-lan-list">
        {outgoing.map((item) => (
          <li key={item.id}>
            <div>
              <strong>{item.boardName}</strong>
              <span className="lm-muted lm-small">
                {' '}
                {item.ownerName} · {statusText[item.status]}
                {item.status === 'pending' ? ' · 配对码 ' + item.code : ''}
              </span>
            </div>
            <span className="lm-actions">
              {item.status === 'approved' && (
                <a className="lm-btn small" href={'#/lan/remote/' + encodeURIComponent(item.id)}>
                  打开
                </a>
              )}
              <button
                className="lm-btn small ghost"
                disabled={!canConfigure(scope)}
                onClick={() =>
                  api
                    .request('/lan/requests/' + encodeURIComponent(item.id), { method: 'DELETE' })
                    .then(refresh)
                }
              >
                删除记录
              </button>
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

export function LanPage() {
  const { api, lan, pulse, refresh: refreshAll } = useApp()
  const tick = useTick(4000)
  const requests = useResource((signal) => api.request<Requests>('/lan/requests', { signal }), [pulse, tick])
  const grants = useResource((signal) => api.request<LANGrant[]>('/lan/grants', { signal }), [pulse, tick])
  const refresh = () => {
    requests.reload()
    grants.reload()
    refreshAll()
  }
  return (
    <div className="lm-page">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">内网</h1>
          <p className="lm-lead">同一个局域网里装了 lgmon 的人，可以申请看你的面板；你也可以申请看别人的。</p>
        </div>
      </header>
      {lan ? (
        <Visibility key={lan.name} state={lan} refresh={refresh} />
      ) : (
        <p className="lm-muted">正在读取内网状态…</p>
      )}
      <Incoming items={requests.value?.incoming || []} refresh={refresh} />
      <Nearby outgoing={requests.value?.outgoing || []} refresh={refresh} />
      <Grants grants={grants.value || []} refresh={refresh} />
      <Sent outgoing={requests.value?.outgoing || []} refresh={refresh} />
    </div>
  )
}

export function RemoteBoardPage({ id }: { id: string }) {
  const { api, pulse } = useApp()
  const tick = useTick(5000)
  const resource = useResource(
    (signal) =>
      api
        .request<LANRemoteBoard>('/lan/remote/' + encodeURIComponent(id), { signal })
        .then((board) => ({ board, gone: '' }))
        .catch((err: unknown) => {
          // A refusal is final: drop the last copy instead of showing it under an error.
          if (err instanceof APIError && (err.status === 403 || err.status === 404))
            return { board: undefined, gone: err.message }
          throw err
        }),
    [pulse, tick],
    id,
  )
  const [layout, setLayout] = useState<'list' | 'cards'>('list')
  const remote = resource.value?.board
  const gone = resource.value?.gone
  const groups = useMemo(() => groupItems(remote?.board.items || []), [remote])
  if (!remote)
    return (
      <div className="lm-page">
        {gone || resource.error ? (
          <div className="lm-note" data-tone="down">
            {gone || resource.error}
            <a className="lm-btn small" href="#/lan">
              回到内网
            </a>
          </div>
        ) : (
          <p className="lm-muted">正在向对方读取面板…</p>
        )}
      </div>
    )
  return (
    <div className="lm-page">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">{remote.board.name}</h1>
          <p className="lm-lead">
            来自 {remote.ownerName} · 只读
            {remote.board.description ? ' · ' + remote.board.description : ''}
          </p>
        </div>
        <div className="lm-actions">
          <div className="lm-seg" role="group" aria-label="显示方式">
            <button aria-pressed={layout === 'list'} onClick={() => setLayout('list')}>
              <Glyph name="list" size={15} />
              列表
            </button>
            <button aria-pressed={layout === 'cards'} onClick={() => setLayout('cards')}>
              <Glyph name="cards" size={15} />
              卡片
            </button>
          </div>
        </div>
      </header>
      {resource.error && (
        <div className="lm-note" data-tone="down">
          {resource.error}（下面是上一次读到的状态）
        </div>
      )}
      <Stats board={remote.board} />
      {layout === 'cards' ? (
        <ServiceCards groups={groups} remote />
      ) : (
        <ServiceList groups={groups} forceOpen={false} remote />
      )}
    </div>
  )
}
