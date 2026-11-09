import { useState } from 'react'
import type { FormEvent } from 'react'
import { useApp } from './state'
import { copyTeamText, teamPath, useTeamResource } from './team'
import type { Joined, Member, Share, Team } from './team'
import { SharedTeamList } from './SharedTeams'
import { Empty, ErrorNotice, Panel } from './ui'
import './teams.css'

const messageOf = (err: unknown) => (err instanceof Error ? err.message : '团队操作失败，请重试')

export function Teams() {
  const { scope } = useApp()
  return (
    <div className="team-page">
      <div className="breadcrumbs">
        <a href="#/boards">面板</a>
        <span>/</span>
        <span>团队与分享</span>
      </div>
      <div className="page-heading">
        <div>
          <p className="eyebrow">TEAMS</p>
          <h1>团队与分享</h1>
          <p className="muted">负责人限定分享范围，成员仅能查看获授权的卡片。</p>
        </div>
        {scope === 'admin' && (
          <a className="button primary" href="#/teams/join">
            粘贴邀请加入
          </a>
        )}
      </div>
      {scope === 'admin' ? (
        <OwnerTeams />
      ) : (
        <p className="notice">
          负责人管理与加入团队需要管理员权限。点击右上角当前权限，更换本人保存的管理员令牌。
        </p>
      )}
      <h2>团队共享</h2>
      <SharedTeamList />
      {scope === 'admin' && <JoinedSettings />}
    </div>
  )
}

function OwnerTeams() {
  const { api } = useApp()
  const teams = useTeamResource((signal) => api.request<Team[]>('/teams', { signal }), [api])
  const [selected, setSelected] = useState('')
  const [create, setCreate] = useState(false)
  const [name, setName] = useState('')
  const [owner, setOwner] = useState('负责人')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const current = selected || teams.value?.[0]?.id || ''
  const save = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      const value = await api.request<Team>('/teams', {
        method: 'POST',
        body: JSON.stringify({ name, ownerName: owner }),
      })
      setSelected(value.id)
      setCreate(false)
      setName('')
      teams.reload()
    } catch (err) {
      setError(messageOf(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Panel
        title="我负责的团队"
        extra={
          <button className="button small-button" onClick={() => setCreate(!create)}>
            新建团队
          </button>
        }
      >
        <div className="team-padding">
          <ErrorNotice message={teams.error || error} />
          {teams.value?.length ? (
            <label>
              选择团队
              <select aria-label="选择团队" value={current} onChange={(e) => setSelected(e.target.value)}>
                {teams.value.map((team) => (
                  <option key={team.id} value={team.id}>
                    {team.name} · {team.ownerName}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <p className="muted">本机尚无自建团队。成员身份保存在本机，读取的数据来自负责人。</p>
          )}
          {create && (
            <form className="team-form" onSubmit={(e) => void save(e)}>
              <label>
                团队名称
                <input required maxLength={128} value={name} onChange={(e) => setName(e.target.value)} />
              </label>
              <label>
                负责人显示名
                <input required maxLength={128} value={owner} onChange={(e) => setOwner(e.target.value)} />
              </label>
              <button className="button primary" disabled={busy}>
                建立团队
              </button>
            </form>
          )}
        </div>
      </Panel>
      {current && <OwnerTeam key={current} team={current} />}
    </>
  )
}

function OwnerTeam({ team }: { team: string }) {
  const { api } = useApp()
  const members = useTeamResource(
    (signal) => api.request<Member[]>('/teams/' + teamPath(team) + '/members', { signal }),
    [api, team],
  )
  const shares = useTeamResource(
    (signal) => api.request<Share[]>('/shares?team=' + teamPath(team), { signal }),
    [api, team],
  )
  const [inviteName, setInviteName] = useState('')
  const [ttl, setTTL] = useState('24h')
  const [invite, setInvite] = useState<{ invitation: string; fingerprint: string; expiresAt: number }>()
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState<Share>()
  const [newShare, setNewShare] = useState(false)
  const activeMembers = members.value?.filter((member) => !member.revokedAt) || []
  const action = async (path: string, method: string) => {
    setError('')
    setBusy(true)
    try {
      await api.request(path, { method })
      members.reload()
      shares.reload()
    } catch (err) {
      setError(messageOf(err))
    } finally {
      setBusy(false)
    }
  }
  const generate = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError('')
    setInvite(undefined)
    setNotice('')
    try {
      setInvite(
        await api.request('/teams/' + teamPath(team) + '/invites', {
          method: 'POST',
          body: JSON.stringify({ name: inviteName, ttl }),
        }),
      )
    } catch (err) {
      setError(messageOf(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <ErrorNotice message={error} />
      <Panel title="邀请成员设备">
        <div className="team-padding">
          <form className="team-form" onSubmit={(e) => void generate(e)}>
            <label>
              成员设备显示名
              <input
                required
                value={inviteName}
                maxLength={128}
                onChange={(e) => setInviteName(e.target.value)}
              />
            </label>
            <label>
              邀请有效期
              <select aria-label="邀请有效期" value={ttl} onChange={(e) => setTTL(e.target.value)}>
                <option value="1h">1 小时</option>
                <option value="24h">24 小时</option>
                <option value="72h">3 天</option>
              </select>
            </label>
            <button className="button primary" disabled={busy}>
              生成一次性邀请
            </button>
          </form>
          {invite && (
            <div className="team-form">
              <p>
                CA 指纹：<strong className="mono">{invite.fingerprint}</strong>
              </p>
              <p className="small muted">
                请通过可信渠道交给对方核对。邀请只返回一次，有效至{' '}
                {new Date(invite.expiresAt).toLocaleString()}。
              </p>
              <label>
                完整邀请文字
                <textarea aria-label="完整邀请文字" readOnly value={invite.invitation} />
              </label>
              <button
                className="button"
                onClick={() =>
                  void copyTeamText(invite.invitation)
                    .then(() => setNotice('邀请已复制，请通过可信渠道发送给指定设备的使用者。'))
                    .catch((err) => setNotice(messageOf(err)))
                }
              >
                复制邀请
              </button>
              {notice && <p role="status">{notice}</p>}
            </div>
          )}
        </div>
      </Panel>
      <Panel title="成员与访问记录">
        <div className="team-padding team-owner-list">
          <ErrorNotice message={members.error} />
          {members.value?.map((member) => (
            <article className="team-entry" key={member.id}>
              <strong>
                {member.name} {member.revokedAt ? '· 已移除' : ''}
              </strong>
              <span className="mono">{member.id}</span>
              <p className="small muted">
                {member.lastAccess ? '最近访问 ' + new Date(member.lastAccess).toLocaleString() : '尚未读取'}{' '}
                · {member.accessCount} 次
              </p>
              {!member.revokedAt && (
                <button
                  className="button small-button"
                  disabled={busy}
                  onClick={() =>
                    void action('/teams/' + teamPath(team) + '/members/' + teamPath(member.id), 'DELETE')
                  }
                >
                  移除 {member.name}
                </button>
              )}
            </article>
          ))}
          {members.value?.length === 0 && <Empty title="尚未有设备加入" />}
        </div>
      </Panel>
      <Panel
        title="面板分享"
        extra={
          <button
            className="button small-button"
            onClick={() => {
              setEditing(undefined)
              setNewShare(true)
            }}
          >
            新建分享
          </button>
        }
      >
        <div className="team-padding team-owner-list">
          <ErrorNotice message={shares.error} />
          {shares.value
            ?.filter((share) => !share.revokedAt)
            .map((share) => (
              <article className="team-entry" key={share.id}>
                <strong>{share.dashboardId}</strong>
                <span className="mono">{share.id}</span>
                <p className="small">
                  {share.panels?.length ? '卡片：' + share.panels.join('、') : '整页面板'} ·{' '}
                  {share.collectors ? '主机：' + share.collectors.join('、') : '全部主机'} ·{' '}
                  {share.targets ? '目标：' + share.targets.join('、') : '全部目标'} ·{' '}
                  {share.members ? '指定 ' + share.members.length + ' 个成员' : '团队全体成员'}
                </p>
                <div className="row-actions">
                  <button
                    className="button small-button"
                    onClick={() => {
                      setEditing(share)
                      setNewShare(false)
                    }}
                  >
                    修改分享
                  </button>
                  <button
                    className="button small-button"
                    disabled={busy}
                    onClick={() => void action('/shares/' + teamPath(share.id), 'DELETE')}
                  >
                    撤销分享
                  </button>
                </div>
              </article>
            ))}
          {shares.value?.every((share) => !!share.revokedAt) && <Empty title="尚无有效分享" />}
        </div>
      </Panel>
      {(editing || newShare) && (
        <ShareEditor
          key={editing?.id || team + ':new'}
          team={team}
          members={activeMembers}
          original={editing}
          onClose={() => {
            setEditing(undefined)
            setNewShare(false)
          }}
          onSave={() => {
            setEditing(undefined)
            setNewShare(false)
            shares.reload()
          }}
        />
      )}
    </>
  )
}

function Selection({
  title,
  all,
  setAll,
  selected,
  setSelected,
  values,
}: {
  title: string
  all: boolean
  setAll: (v: boolean) => void
  selected: string[]
  setSelected: (v: string[]) => void
  values: { id: string; name: string }[]
}) {
  return (
    <fieldset>
      <legend>{title}</legend>
      <label className="team-check">
        <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
        全部{title}
      </label>
      {!all && (
        <div className="team-choice-list">
          {values.map((item) => (
            <label key={item.id} className="team-check">
              <input
                type="checkbox"
                checked={selected.includes(item.id)}
                onChange={(e) =>
                  setSelected(
                    e.target.checked ? [...selected, item.id] : selected.filter((v) => v !== item.id),
                  )
                }
              />
              {item.name}
            </label>
          ))}
          {values.length === 0 && <p className="muted">暂无可选项；请先准备数据或成员。</p>}
        </div>
      )}
    </fieldset>
  )
}

function ShareEditor({
  team,
  members,
  original,
  onClose,
  onSave,
}: {
  team: string
  members: Member[]
  original?: Share
  onClose: () => void
  onSave: () => void
}) {
  const { api, data } = useApp()
  const pages = useTeamResource(
    (signal) =>
      api.request<{ id: string; title: string; html?: boolean; error: string | null }[]>('/dashboards', {
        signal,
      }),
    [api],
    0,
  )
  const [dashboard, setDashboard] = useState(original?.dashboardId || '')
  const definition = useTeamResource(
    (signal) =>
      dashboard
        ? api.request<{ panels: { id: string; title: string }[]; error: string | null }>(
            '/dashboards/' + teamPath(dashboard),
            { signal },
          )
        : Promise.resolve({ panels: [], error: null }),
    [api, dashboard],
    0,
  )
  const [allPanels, setAllPanels] = useState(!original?.panels?.length),
    [panels, setPanels] = useState(original?.panels || [])
  const [allCollectors, setAllCollectors] = useState(!original?.collectors),
    [collectors, setCollectors] = useState(original?.collectors || [])
  const [allTargets, setAllTargets] = useState(!original?.targets),
    [targets, setTargets] = useState(original?.targets || [])
  const [allMembers, setAllMembers] = useState(!original?.members),
    [selectedMembers, setSelectedMembers] = useState(original?.members || [])
  const [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  const save = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    if (
      (!allPanels && !panels.length) ||
      (!allCollectors && !collectors.length) ||
      (!allTargets && !targets.length) ||
      (!allMembers && !selectedMembers.length)
    ) {
      setError('指定范围须至少选择一项；解除限制请明确勾选“全部”。')
      return
    }
    setBusy(true)
    const input = {
      teamId: team,
      dashboardId: dashboard,
      ...(original ? { revision: original.revision } : {}),
      ...(!allPanels ? { panels } : {}),
      ...(!allCollectors ? { collectors } : {}),
      ...(!allTargets ? { targets } : {}),
      ...(!allMembers ? { members: selectedMembers } : {}),
    }
    try {
      await api.request('/shares' + (original ? '/' + teamPath(original.id) : ''), {
        method: original ? 'PUT' : 'POST',
        body: JSON.stringify(input),
      })
      onSave()
    } catch (err) {
      setError(messageOf(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Panel
      title={original ? '修改分享范围' : '新建面板分享'}
      extra={
        <button className="button small-button" onClick={onClose}>
          取消编辑
        </button>
      }
    >
      <form className="team-padding team-form" onSubmit={(e) => void save(e)}>
        <ErrorNotice message={error || pages.error || definition.error || definition.value?.error || ''} />
        <label>
          声明式面板
          <select
            aria-label="声明式面板"
            required
            value={dashboard}
            onChange={(e) => {
              setDashboard(e.target.value)
              setPanels([])
            }}
          >
            <option value="">请选择面板</option>
            {pages.value
              ?.filter((page) => !page.html)
              .map((page) => (
                <option value={page.id} key={page.id} disabled={!!page.error}>
                  {page.title}
                  {page.error ? '（配置有错误）' : ''}
                </option>
              ))}
          </select>
        </label>
        <Selection
          title="卡片"
          all={allPanels}
          setAll={setAllPanels}
          selected={panels}
          setSelected={setPanels}
          values={definition.value?.panels.map((p) => ({ id: p.id, name: p.title })) || []}
        />
        <Selection
          title="主机"
          all={allCollectors}
          setAll={setAllCollectors}
          selected={collectors}
          setSelected={setCollectors}
          values={data?.collectors.map((c) => ({ id: c.id, name: c.hostname + ' · ' + c.id })) || []}
        />
        <Selection
          title="目标"
          all={allTargets}
          setAll={setAllTargets}
          selected={targets}
          setSelected={setTargets}
          values={data?.definitions.map((t) => ({ id: t.id, name: t.name || t.id })) || []}
        />
        <Selection
          title="成员"
          all={allMembers}
          setAll={setAllMembers}
          selected={selectedMembers}
          setSelected={setSelectedMembers}
          values={members.map((m) => ({ id: m.id, name: m.name }))}
        />
        <p className="notice small">
          将分享{allPanels ? '整页面板' : panels.length + ' 张卡片'}，范围为
          {allCollectors ? '全部主机' : collectors.length + ' 台主机'}、
          {allTargets ? '全部目标' : targets.length + ' 个目标'}，对象为
          {allMembers ? '团队全体成员（包括后续加入的成员）' : selectedMembers.length + ' 个指定成员'}。
        </p>
        <button className="button primary" disabled={busy || !dashboard || !!definition.value?.error}>
          保存分享范围
        </button>
      </form>
    </Panel>
  )
}

export function TeamJoin() {
  const { api, scope } = useApp()
  const [invitation, setInvitation] = useState('')
  const [preview, setPreview] = useState<{ address: string; fingerprint: string; teamId: string }>()
  const [confirmed, setConfirmed] = useState(false)
  const [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  const check = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    setPreview(undefined)
    setConfirmed(false)
    setBusy(true)
    try {
      setPreview(
        await api.request('/team-joins/preview', { method: 'POST', body: JSON.stringify({ invitation }) }),
      )
    } catch (err) {
      setError(messageOf(err))
    } finally {
      setBusy(false)
    }
  }
  const join = async () => {
    if (!preview || !confirmed) return
    setError('')
    setBusy(true)
    try {
      await api.request('/team-joins', {
        method: 'POST',
        body: JSON.stringify({ invitation, fingerprint: preview.fingerprint }),
      })
      setInvitation('')
      window.location.hash = '#/teams'
    } catch (err) {
      setError(messageOf(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="team-page">
      <div className="breadcrumbs">
        <a href="#/boards">面板</a>
        <span>/</span>
        <span>加入团队</span>
      </div>
      <div className="page-heading">
        <div>
          <p className="eyebrow">JOIN A TEAM</p>
          <h1>粘贴邀请加入团队</h1>
          <p className="muted">核对负责人的 CA 指纹后，为本设备建立独立的只读成员身份。</p>
        </div>
      </div>
      <Panel title="团队邀请">
        <div className="team-padding">
          <ErrorNotice message={error} />
          {scope !== 'admin' ? (
            <p className="notice">加入团队需要管理员权限，请先通过右上角权限按钮连接本人保存的管理员令牌。</p>
          ) : (
            <>
              <form className="team-form" onSubmit={(e) => void check(e)}>
                <label>
                  负责人提供的完整邀请文字
                  <textarea
                    required
                    maxLength={16384}
                    value={invitation}
                    onChange={(e) => {
                      setInvitation(e.target.value)
                      setPreview(undefined)
                      setConfirmed(false)
                    }}
                  />
                </label>
                <button className="button" disabled={busy || !invitation.trim()}>
                  检查地址与指纹
                </button>
              </form>
              {preview && (
                <div className="team-form">
                  <p>
                    负责人地址：<span className="mono">{preview.address}</span>
                  </p>
                  <p>
                    CA 指纹：<strong className="mono">{preview.fingerprint}</strong>
                  </p>
                  <label className="team-check">
                    <input
                      type="checkbox"
                      checked={confirmed}
                      onChange={(e) => setConfirmed(e.target.checked)}
                    />
                    已通过可信渠道与负责人核对以上 16 位指纹
                  </label>
                  <button
                    className="button primary"
                    disabled={busy || !confirmed}
                    onClick={() => void join()}
                  >
                    确认指纹并加入
                  </button>
                </div>
              )}
            </>
          )}
        </div>
      </Panel>
    </div>
  )
}

function JoinedSettings() {
  const { api } = useApp()
  const source = useTeamResource((signal) => api.request<Joined[]>('/team-joins', { signal }), [api])
  return (
    <Panel title="本机加入记录">
      <div className="team-padding team-owner-list">
        <ErrorNotice message={source.error} />
        {source.value?.map((joined) => (
          <JoinedAddress key={joined.id} joined={joined} reload={source.reload} />
        ))}
        {source.value?.length === 0 && <p className="muted">尚无本机成员身份。</p>}
      </div>
    </Panel>
  )
}
function JoinedAddress({ joined, reload }: { joined: Joined; reload: () => void }) {
  const { api } = useApp()
  const [address, setAddress] = useState(joined.address),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  const action = async (method: string) => {
    setBusy(true)
    setError('')
    try {
      await api.request('/team-joins/' + teamPath(joined.id), {
        method,
        ...(method === 'PUT' ? { body: JSON.stringify({ address }) } : {}),
      })
      reload()
    } catch (err) {
      setError(messageOf(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <article className="team-entry">
      <strong>
        {joined.ownerName} · {joined.teamName}
      </strong>
      <span className="mono">本机身份 {joined.memberId}</span>
      <p className="small muted">
        更换地址会保留 CA 指纹和成员证书；离开只移除本机记录，负责人处移除成员才会吊销证书。
      </p>
      <ErrorNotice message={error} />
      <label>
        负责人 HTTPS 地址
        <input
          aria-label={'负责人 HTTPS 地址 ' + joined.id}
          value={address}
          onChange={(e) => setAddress(e.target.value)}
        />
      </label>
      <div className="row-actions">
        <button className="button small-button" disabled={busy} onClick={() => void action('PUT')}>
          保存新地址
        </button>
        <button className="button small-button" disabled={busy} onClick={() => void action('DELETE')}>
          离开此团队
        </button>
      </div>
    </article>
  )
}
