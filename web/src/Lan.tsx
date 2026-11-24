/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { ago } from './kit'
import { LANAccessSettings } from './LANAccess'
import { PageLoading } from './loading'
import { SharedScope } from './shared'
import type { Shared } from './shared'
import { BoardMap, ServiceCards, ServiceList, Stats, groupItems } from './Board'
import { Avatar, ProfileCard } from './avatar'
import { DepartmentsSection } from './departments'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { useGate } from './access'
import { useApp, useResource } from './state'
import { usePrefetchRemote, useRemoteBoard, useTick } from './remote-cache'
import type { RemoteCopy } from './remote-cache'
export { usePrefetchRemote, useRemoteBoard }
import { dayText } from './status'
import { CopyButton } from './parts'

const ServicePage = lazy(() => import('./Service').then((value) => ({ default: value.ServicePage })))
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

function Visibility({ state, refresh }: { state: LANState; refresh: () => void }) {
  const { api } = useApp()
  const { t } = useI18n()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const gate = useGate()
  const save = (body: Record<string, unknown>) => {
    setBusy(true)
    setError('')
    api
      .request<LANState>('/lan', { method: 'PUT', body: JSON.stringify(body) })
      .then(refresh)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
      .finally(() => setBusy(false))
  }
  return (
    <section className="lm-section" aria-label={t('lanVisibleTitle')} style={{ marginTop: 0 }}>
      <div className="lm-switch-row">
        <div>
          <h2 className="lm-h2">{t('lanVisibleTitle')}</h2>
          <p className="lm-muted lm-small" style={{ margin: '6px 0 0' }}>
            {t('lanVisibleHint')}
          </p>
        </div>
        <button
          className="lm-switch"
          role="switch"
          aria-checked={state.visible}
          aria-label={t('lanVisibleTitle')}
          disabled={busy}
          onClick={() => gate('config', t('lanVisibleTitle'), () => save({ visible: !state.visible }))}
        >
          <i />
        </button>
      </div>
      {error && <p className="lm-error">{error}</p>}
      {state.visible && (
        <dl className="lm-kv" style={{ marginTop: 16 }}>
          <dt>{t('lanLocalAddresses')}</dt>
          <dd>{state.addresses.length ? state.addresses.join('、') : '—'}</dd>
          <dt>{t('lanFingerprint')}</dt>
          <dd style={{ fontFamily: 'var(--mono)' }}>{state.fingerprint}</dd>
        </dl>
      )}
      {state.visible && state.problem && (
        <p className="lm-reason" data-state="unknown">
          {state.problem}
        </p>
      )}
      {state.visible && state.firewall === 'blocked' && state.firewallFix && (
        <div className="lm-fix" aria-label="放行防火墙的命令">
          <pre>{state.firewallFix}</pre>
          <CopyButton text={state.firewallFix} label={t('lanCopyCommand')} />
        </div>
      )}
      {state.visible && (
        <p className="lm-muted lm-small" style={{ margin: '12px 0 0' }}>
          {state.firewall === 'allowed'
            ? '已在 Windows 防火墙放行成员端口 7445/TCP 和发现端口 7446/UDP，只接受内网和 Tailscale 地址；关闭内网可见时自动删除这条规则。'
            : '打开内网可见时，lgmon 会在 Windows 防火墙为内网和 Tailscale 地址放行 7445/TCP 与 7446/UDP，关闭时删除；不会改动其他规则。'}
        </p>
      )}
    </section>
  )
}

function Incoming({ items, refresh }: { items: LANIncoming[]; refresh: () => void }) {
  const { api } = useApp()
  const { t } = useI18n()
  const gate = useGate()
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
    <section className="lm-section" aria-label={t('lanIncomingTitle')}>
      <header>
        <h2 className="lm-h2">{t('lanIncomingTitle')}</h2>
        <small>{t('lanIncomingHint')}</small>
      </header>
      <ul className="lm-lan-list">
        {pending.map((item) => (
          <li key={item.id}>
            <span className="lm-person">
              <Avatar value={item.avatar} seed={item.deviceName} size={36} />
              <span>
                <strong>{item.deviceName}</strong>
                <small>
                  「{item.boardName || item.boardId}」 · {item.address}
                </small>
              </span>
            </span>
            <span className="lm-code-big" aria-label={'配对码 ' + item.code}>
              {item.code}
            </span>
            <div className="lm-actions">
              <button
                className="lm-btn small"
                disabled={busy === item.id}
                onClick={() => gate('config', t('lanReject'), () => decide(item.id, 'deny'))}
              >
                {t('lanReject')}
              </button>
              <button
                className="lm-btn small primary"
                disabled={busy === item.id}
                onClick={() => gate('config', t('lanApprove'), () => decide(item.id, 'approve'))}
              >
                {t('lanApprove')}
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
  const { api } = useApp()
  const gate = useGate()
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
        disabled={busy}
        onClick={() =>
          gate('config', '申请查看', () => {
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
          })
        }
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
        <span className="lm-person">
          <Avatar
            value={peer.directory?.avatar}
            seed={peer.directory?.fingerprint || peer.fingerprint}
            size={36}
          />
          <span>
            <strong>{peer.directory?.name || peer.name || peer.address}</strong>
            <small>
              {peer.address.replace('https://', '')} · 指纹 {short(peer.fingerprint).slice(0, 8)}…
            </small>
          </span>
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

// peerHost mirrors the server's lan.peers normalising, so a looked-up address can be shown as remembered.
function peerHost(value: string) {
  let host = value
    .trim()
    .replace(/^[a-z]+:\/\//i, '')
    .replace(/\/+$/, '')
  if (host.startsWith('[')) host = host.slice(1, host.indexOf(']'))
  else if (host.split(':').length === 2) host = host.split(':')[0]
  return host.toLowerCase().replace(/\.$/, '')
}

function Nearby({ outgoing, refresh }: { outgoing: LANOutgoing[]; refresh: () => void }) {
  const { api, lan } = useApp()
  const gate = useGate()
  const peersSaved = lan?.peers || []
  const savePeers = (next: string[], label: string) =>
    gate('config', label, () => {
      setError('')
      api
        .request<LANState>('/lan', { method: 'PUT', body: JSON.stringify({ peers: next }) })
        .then(() => {
          refresh()
          search()
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
    })
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
          没有找到打开了内网可见的 lgmon。对方需要在自己的“内网”页打开开关。发现靠广播，只能到达同一网段；
          对方在办公网的其他网段或用 Tailscale
          时，在下面输入对方地址，查看后点「记住」，以后每次搜索和部门都会直接问它。
        </p>
      )}
      <div style={{ display: 'grid', gap: 12 }}>
        {peers?.map((peer) => (
          <Peer key={peer.fingerprint + peer.address} peer={peer} outgoing={outgoing} refresh={refresh} />
        ))}
        {manual && <Peer peer={manual} outgoing={outgoing} refresh={refresh} />}
      </div>
      {manual && !peersSaved.includes(peerHost(manual.address)) && (
        <p className="lm-lan-remember">
          <span>广播找不到它？记住这个地址，以后搜索附近的电脑和部门时会直接问它。</span>
          <button
            type="button"
            className="lm-btn small"
            onClick={() => savePeers([...peersSaved, peerHost(manual.address)], '记住跨网段地址')}
          >
            记住 {peerHost(manual.address)}
          </button>
        </p>
      )}
      {peersSaved.length > 0 && (
        <div className="lm-lan-peers" aria-label="记住的跨网段地址">
          <small>记住的跨网段地址</small>
          {peersSaved.map((host) => (
            <span key={host} className="lm-lan-peer">
              {host}
              <button
                type="button"
                aria-label={'不再记住 ' + host}
                title="不再记住"
                onClick={() =>
                  savePeers(
                    peersSaved.filter((item) => item !== host),
                    '移除跨网段地址',
                  )
                }
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
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
  const { api } = useApp()
  const gate = useGate()
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
              <span className="lm-person">
                <Avatar value={grant.avatar} seed={grant.fingerprint || grant.deviceName} size={32} />
                <span>
                  <strong>{grant.deviceName}</strong>
                  <small>
                    可以看「{grant.boardName || grant.boardId}」 · {dayText(grant.createdAt)}同意
                  </small>
                </span>
              </span>
              <button
                className="lm-btn small danger"
                onClick={() =>
                  gate('config', '撤销授权', () =>
                    api
                      .request('/lan/grants/' + encodeURIComponent(grant.id), { method: 'DELETE' })
                      .then(refresh)
                      .catch((err: unknown) => setError(err instanceof Error ? err.message : '撤销失败')),
                  )
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
  const { api } = useApp()
  const gate = useGate()
  if (!outgoing.length) return null
  return (
    <section className="lm-section" aria-label="我发出的申请">
      <header>
        <h2 className="lm-h2">我发出的申请</h2>
      </header>
      <ul className="lm-lan-list">
        {outgoing.map((item) => (
          <li key={item.id}>
            <span className="lm-person">
              <Avatar value={item.ownerAvatar} seed={item.fingerprint} size={32} />
              <span>
                <strong>{item.boardName}</strong>
                <small>
                  {item.ownerName} · {statusText[item.status]}
                  {item.status === 'pending' ? ' · 配对码 ' + item.code : ''}
                </small>
              </span>
            </span>
            <span className="lm-actions">
              {item.status === 'approved' && (
                <a className="lm-btn small" href={'#/lan/remote/' + encodeURIComponent(item.id)}>
                  打开
                </a>
              )}
              <button
                className="lm-btn small ghost"
                onClick={() =>
                  gate('config', '删除记录', () =>
                    api
                      .request('/lan/requests/' + encodeURIComponent(item.id), { method: 'DELETE' })
                      .then(refresh),
                  )
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

function WebAccess({ state, refresh }: { state: LANState; refresh: () => void }) {
  const { api, boardList, monitors, monitorsLoaded, cloudStatus, cloudDevices } = useApp()
  const { lang, t } = useI18n()
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const gate = useGate()
  const [savedScope, setSavedScope] = useState<Pick<
    LANState,
    'webBoards' | 'webServices' | 'webDeviceBoards'
  > | null>(null)
  const currentScope = JSON.stringify([state.webBoards, state.webServices, state.webDeviceBoards])
  useEffect(() => setSavedScope(null), [currentScope])
  const effective = savedScope || state
  const selected = effective.webBoards || []
  const restricted = selected.length > 0
  const save = (body: Record<string, unknown>) => {
    setBusy(true)
    setError('')
    api
      .request<LANState>('/lan', { method: 'PUT', body: JSON.stringify(body) })
      .then((result) => {
        setSavedScope(result)
        refresh()
      })
      .catch((err: unknown) => {
        setSavedScope(null)
        setError(err instanceof Error ? err.message : '保存失败')
      })
      .finally(() => setBusy(false))
  }
  const serviceScopes = effective.webServices || {}
  const saveServices = (board: string, ids?: string[]) => {
    gate('config', t('lanWebScopeLabel'), () => {
      const webServices = { ...serviceScopes }
      if (ids === undefined) delete webServices[board]
      else webServices[board] = ids
      setSavedScope({ webBoards: selected, webServices, webDeviceBoards: effective.webDeviceBoards })
      save({ webServices: { [board]: ids ?? null } })
    })
  }
  const otherDevices = cloudStatus?.loggedIn ? cloudDevices.filter((dev) => !dev.isCurrent) : []
  const deviceBoards = effective.webDeviceBoards || {}
  const toggleDeviceBoard = (device: string, board: string) => {
    const current = deviceBoards[device] || []
    const next = current.includes(board) ? current.filter((id) => id !== board) : [...current, board]
    gate('config', t('lanWebDevicesTitle'), () => {
      setSavedScope({
        webBoards: selected,
        webServices: serviceScopes,
        webDeviceBoards: { ...deviceBoards, [device]: next },
      })
      save({ webDeviceBoards: { [device]: next } })
    })
  }
  const toggleBoard = (id: string) => {
    const next = selected.includes(id) ? selected.filter((item) => item !== id) : [...selected, id]
    if (!next.length) {
      setError(t('lanWebScopePickOne'))
      return
    }
    gate('config', t('lanWebScopeLabel'), () => save({ webBoards: next }))
  }
  return (
    <section className="lm-section" aria-label={t('lanWebTitle')}>
      <div className="lm-switch-row">
        <div>
          <h2 className="lm-h2">{t('lanWebTitle')}</h2>
          <p className="lm-muted lm-small" style={{ margin: '6px 0 0' }}>
            {t('lanWebHint')}
          </p>
        </div>
        <button
          className="lm-switch"
          role="switch"
          aria-checked={!!state.web}
          aria-label={t('lanWebTitle')}
          disabled={busy}
          onClick={() => gate('config', t('lanWebTitle'), () => save({ web: !state.web }))}
        >
          <i />
        </button>
      </div>
      {error && <p className="lm-error">{error}</p>}
      {state.web ? (
        <div style={{ marginTop: 14, display: 'grid', gap: 14 }}>
          {state.webAddresses && state.webAddresses.length > 0 && (
            <div style={{ display: 'grid', gap: 6 }}>
              <span className="lm-muted lm-small">{t('lanWebAddresses')}</span>
              {state.webAddresses.map((address) => (
                <div key={address} className="lm-code">
                  <code>{address}</code>
                  <CopyButton text={address} />
                </div>
              ))}
            </div>
          )}
          {state.firewall === 'blocked' && state.firewallFix ? (
            <div style={{ display: 'grid', gap: 8 }}>
              <p className="lm-reason" data-state="unknown" style={{ margin: 0 }}>
                {t('lanWebFirewallBlocked')}
              </p>
              <div className="lm-fix" aria-label={t('lanCopyCommand')}>
                <pre>{state.firewallFix}</pre>
                <CopyButton text={state.firewallFix} label={t('lanCopyCommand')} />
              </div>
            </div>
          ) : state.firewall === 'allowed' ? (
            <p className="lm-muted lm-small" style={{ margin: 0 }}>
              {t('lanWebFirewallAllowed')}
            </p>
          ) : null}
          {boardList.length > 0 && (
            <div className="lm-web-scope">
              <div className="lm-web-scope-head">
                <span className="lm-web-scope-title">{t('lanWebScopeLabel')}</span>
                <div className="lm-seg" role="group" aria-label={t('lanWebScopeLabel')}>
                  <button
                    type="button"
                    aria-pressed={!restricted}
                    disabled={busy}
                    onClick={() => {
                      if (!restricted) return
                      gate('config', t('lanWebScopeLabel'), () => save({ webBoards: [] }))
                    }}
                  >
                    {t('lanWebScopeAll')}
                  </button>
                  <button
                    type="button"
                    aria-pressed={restricted}
                    disabled={busy}
                    onClick={() => {
                      if (restricted) return
                      const first = boardList[0]?.id
                      if (!first) return
                      gate('config', t('lanWebScopeLabel'), () => save({ webBoards: [first] }))
                    }}
                  >
                    {t('lanWebScopeSelected')}
                  </button>
                </div>
              </div>
              {restricted && (
                <div className="lm-web-scope-boards">
                  {boardList.map((board) => {
                    const checked = selected.includes(board.id)
                    return (
                      <button
                        key={board.id}
                        type="button"
                        className="lm-web-scope-chip"
                        aria-pressed={checked}
                        disabled={busy}
                        onClick={() => toggleBoard(board.id)}
                      >
                        <Glyph name={checked ? 'check' : 'board'} size={13} />
                        <span>{board.name}</span>
                        <small>{board.summary.services}</small>
                      </button>
                    )
                  })}
                </div>
              )}
              {boardList
                .filter((board) => !restricted || selected.includes(board.id))
                .map((board) => {
                  const included = new Set(board.monitors)
                  const byID = new Map(monitors.map((m) => [m.id, m]))
                  const members = monitors.filter((m) => {
                    let id: string | undefined = m.id
                    const seen = new Set<string>()
                    while (id && !seen.has(id)) {
                      if (included.has(id)) return true
                      seen.add(id)
                      id = byID.get(id)?.parent
                    }
                    return false
                  })
                  const chosen = serviceScopes[board.id]
                  return (
                    <details className="lm-service-scope" key={board.id}>
                      <summary>
                        <strong>{board.name}</strong>
                        <small>
                          {chosen === undefined
                            ? lang === 'en'
                              ? 'All services'
                              : '全部服务'
                            : (lang === 'en' ? 'Selected services' : '已选服务') +
                              ' · ' +
                              members.filter((m) => chosen.includes(m.id)).length}
                        </small>
                      </summary>
                      <label>
                        <input
                          type="checkbox"
                          disabled={busy || !monitorsLoaded}
                          checked={chosen === undefined}
                          onChange={() => saveServices(board.id, chosen === undefined ? [] : undefined)}
                        />
                        {lang === 'en'
                          ? 'Share all current and future services'
                          : '开放此面板所有服务（含以后新增）'}
                      </label>
                      {members.map((m) => (
                        <label key={m.id} style={{ paddingInlineStart: Math.min(m.depth || 0, 5) * 14 }}>
                          <input
                            type="checkbox"
                            disabled={busy || !monitorsLoaded}
                            checked={chosen === undefined || chosen.includes(m.id)}
                            onChange={() => {
                              const ids = chosen ?? members.map((item) => item.id)
                              saveServices(
                                board.id,
                                ids.includes(m.id) ? ids.filter((id) => id !== m.id) : [...ids, m.id],
                              )
                            }}
                          />
                          {m.name}
                        </label>
                      ))}
                    </details>
                  )
                })}
              <p className="lm-muted lm-small" style={{ margin: 0 }}>
                {t('lanWebScopeHint')}
              </p>
            </div>
          )}
          {otherDevices.length > 0 && (
            <div className="lm-web-scope" role="group" aria-label={t('lanWebDevicesTitle')}>
              <div className="lm-web-scope-head">
                <span className="lm-web-scope-title">{t('lanWebDevicesTitle')}</span>
              </div>
              {otherDevices.map((dev) => {
                const chosen = deviceBoards[dev.id] || []
                return (
                  <div
                    key={dev.id}
                    className="lm-web-device"
                    role="group"
                    aria-label={t('lanWebDevicesLabel', { name: dev.name })}
                  >
                    <div className="lm-web-device-head">
                      <Avatar value={dev.avatar} seed={dev.id} size={20} />
                      <strong>{dev.name}</strong>
                      {!dev.online && <small>{t('lanWebDevicesOffline')}</small>}
                    </div>
                    {dev.boards.length > 0 ? (
                      <div className="lm-web-scope-boards">
                        {dev.boards.map((board) => {
                          const checked = chosen.includes(board.id)
                          return (
                            <button
                              key={board.id}
                              type="button"
                              className="lm-web-scope-chip"
                              aria-pressed={checked}
                              disabled={busy}
                              onClick={() => toggleDeviceBoard(dev.id, board.id)}
                            >
                              <Glyph name={checked ? 'check' : 'board'} size={13} />
                              <span>{board.name}</span>
                              <small>{board.summary.services}</small>
                            </button>
                          )
                        })}
                      </div>
                    ) : (
                      <p className="lm-muted lm-small" style={{ margin: 0 }}>
                        {t('lanWebDevicesNone')}
                      </p>
                    )}
                  </div>
                )
              })}
              <p className="lm-muted lm-small" style={{ margin: 0 }}>
                {t('lanWebDevicesHint')}
              </p>
            </div>
          )}
        </div>
      ) : (
        <p className="lm-muted lm-small" style={{ margin: '10px 0 0' }}>
          {t('lanWebOffHint')}
        </p>
      )}
    </section>
  )
}

export function LanPage() {
  const { api, lan, pulse, refresh: refreshAll } = useApp()
  const { t } = useI18n()
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
          <h1 className="lm-title">{t('lanTitle')}</h1>
          <p className="lm-lead">{t('lanLead')}</p>
        </div>
      </header>
      {lan && <ProfileCard />}
      {lan ? (
        <>
          <Visibility state={lan} refresh={refresh} />
          <WebAccess state={lan} refresh={refresh} />
          <LANAccessSettings tick={tick} />
        </>
      ) : (
        <p className="lm-muted">正在读取内网状态…</p>
      )}
      <DepartmentsSection />
      <Incoming items={requests.value?.incoming || []} refresh={refresh} />
      <Nearby outgoing={requests.value?.outgoing || []} refresh={refresh} />
      <Grants grants={grants.value || []} refresh={refresh} />
      <Sent outgoing={requests.value?.outgoing || []} refresh={refresh} />
    </div>
  )
}

function sharedOf(remote: LANRemoteBoard): Shared {
  return {
    id: remote.id,
    boardName: remote.board.name,
    ownerName: remote.ownerName,
    ownerAvatar: remote.ownerAvatar,
    monitors: remote.board.items,
  }
}

function RemoteMissing({ copy, label }: { copy: RemoteCopy; label: string }) {
  if (!copy.gone && !copy.error) return <PageLoading label={label} />
  return (
    <div className="lm-note" data-tone="down">
      {copy.gone || copy.error}
      <a className="lm-btn small" href="#/lan">
        回到内网
      </a>
    </div>
  )
}

/* How fresh the copy is; only a copy that has stopped updating for a while says so, and calmly. */
function Freshness({ copy }: { copy: RemoteCopy }) {
  const { now } = useApp()
  useTick(5000)
  const at = copy.board?.fetchedAt || 0
  if (!at) return null
  const stale = !!copy.error && now() - at > 60_000
  return (
    <span className="lm-fresh" data-stale={stale || undefined} title={stale ? copy.error : undefined}>
      <i aria-hidden="true" />
      {stale ? '对方暂时连不上，显示的是 ' + ago(at, now()) + '的状态' : ago(at, now()) + '更新'}
    </span>
  )
}

export function RemoteBoardPage({ id }: { id: string }) {
  const copy = useRemoteBoard(id)
  const [layout, setLayout] = useState<'list' | 'cards' | 'map'>('list')
  const remote = copy.board
  const groups = useMemo(() => groupItems(remote?.board.items || []), [remote])
  const shared = useMemo(() => remote && sharedOf(remote), [remote])
  if (!remote || !shared)
    return (
      <div className="lm-page">
        <RemoteMissing copy={copy} label="正在向对方读取面板…" />
      </div>
    )
  return (
    <SharedScope value={shared}>
      <div className="lm-page">
        <header className="lm-head">
          <div>
            <h1 className="lm-title">{remote.board.name}</h1>
            <p className="lm-lead lm-remote-owner">
              <Avatar value={remote.ownerAvatar} seed={remote.ownerName} size={22} />
              <span>
                来自 {remote.ownerName} · 只读
                {remote.board.description ? ' · ' + remote.board.description : ''}
              </span>
              <Freshness copy={copy} />
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
              <button aria-pressed={layout === 'map'} onClick={() => setLayout('map')}>
                <Glyph name="graph" size={15} />
                关系
              </button>
            </div>
          </div>
        </header>
        <Stats board={remote.board} />
        {layout === 'map' ? (
          <BoardMap board={remote.board} remote />
        ) : layout === 'cards' ? (
          <ServiceCards groups={groups} board={remote.board} />
        ) : (
          <ServiceList groups={groups} forceOpen={false} />
        )}
      </div>
    </SharedScope>
  )
}

/* One service of someone else's board, the way its owner sees it, read-only. */
export function RemoteServicePage({ id, monitor }: { id: string; monitor: string }) {
  const copy = useRemoteBoard(id)
  const shared = useMemo(() => copy.board && sharedOf(copy.board), [copy.board])
  if (!shared)
    return (
      <div className="lm-page">
        <RemoteMissing copy={copy} label="正在向对方读取服务…" />
      </div>
    )
  return (
    <SharedScope value={shared}>
      <Suspense
        fallback={
          <div className="lm-page">
            <PageLoading label="正在打开服务…" />
          </div>
        }
      >
        <ServicePage key={monitor} id={monitor} />
      </Suspense>
    </SharedScope>
  )
}
