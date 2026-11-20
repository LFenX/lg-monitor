import { useEffect, useMemo, useState } from 'react'
import { useGate } from './access'
import { Avatar, AvatarPicker, randomAvatar } from './avatar'
import { Glyph } from './glyphs'
import { Dialog } from './parts'
import { useApp, useResource } from './state'
import type { DepartmentCard, DepartmentList, DepartmentPerson, DepartmentRequest } from './types'
import './departments.css'

/*
 * Departments on the LAN: anyone may create one (it lives on their computer) or ask to join one; a leader
 * approves. They nest, also under a department another computer hosts, and other people's boards can be arranged
 * by them. No board sharing is needed to see them.
 */

export type Placed = DepartmentCard & {
  host: string
  hostName: string
  local: boolean
  key: string
  children: Placed[]
}

/* Every department as a tree, keyed "<host>:<id>" the way parents name them. */
export function departmentTree(list?: DepartmentList): Placed[] {
  if (!list || !Array.isArray(list.hosts)) return []
  const all: Placed[] = []
  for (const host of list.hosts)
    for (const card of Array.isArray(host.departments) ? host.departments : [])
      all.push({
        ...card,
        host: host.id,
        hostName: host.name,
        local: !!host.local,
        key: host.id + ':' + card.id,
        children: [],
      })
  const byKey = new Map(all.map((item) => [item.key, item]))
  // Two computers can each point a department at the other's; a department in such a loop stands at the top
  // instead of disappearing with the whole loop.
  const loops = (item: Placed) => {
    const seen = new Set<string>()
    for (
      let at = item.parent ? byKey.get(item.parent) : undefined;
      at;
      at = at.parent ? byKey.get(at.parent) : undefined
    ) {
      if (at === item) return true
      if (seen.has(at.key)) return false
      seen.add(at.key)
    }
    return false
  }
  const roots: Placed[] = []
  for (const item of all) {
    const parent = item.parent && byKey.get(item.parent)
    if (parent && parent !== item && !loops(item)) parent.children.push(item)
    else roots.push(item)
  }
  return roots
}

export function flatten(nodes: Placed[], depth = 0): (Placed & { depth: number })[] {
  return nodes.flatMap((node) => [{ ...node, depth }, ...flatten(node.children, depth + 1)])
}

/* The departments, read again every minute; finding them on the LAN takes the server a moment. */
export function useDepartments(enabled = true) {
  const { api, pulse } = useApp()
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), 60_000)
    return () => clearInterval(timer)
  }, [])
  return useResource(
    (signal) =>
      enabled
        ? api.request<DepartmentList>('/departments', { signal }).catch(() => undefined)
        : Promise.resolve(undefined),
    [api, pulse, tick, enabled],
  )
}

/* Other people's boards, grouped by the department their owner is in; owners in none come last. */
export function byDepartment<T extends { ownerAccount?: string }>(items: T[], list?: DepartmentList) {
  const groups: { department?: Placed; items: T[] }[] = []
  for (const item of items) {
    const department = departmentOf(item.ownerAccount, list)
    const group = groups.find((entry) => entry.department?.key === department?.key)
    if (group) group.items.push(item)
    else groups.push({ department, items: [item] })
  }
  return groups.sort((a, b) => (a.department ? 0 : 1) - (b.department ? 0 : 1))
}

/* Which department a person (by account key) is in, the deepest one when there are several. */
export function departmentOf(account: string | undefined, list?: DepartmentList) {
  if (!account) return undefined
  const found = flatten(departmentTree(list)).filter((item) =>
    item.people.some((person) => person.key === account),
  )
  return found.sort((a, b) => b.depth - a.depth)[0]
}

function roleOf(card: DepartmentCard, me: string, pending: boolean) {
  const person = card.people.find((item) => item.key === me)
  if (person) return person.role
  return pending ? 'pending' : ''
}

const roleText: Record<string, string> = { leader: '负责人', member: '成员', pending: '等待负责人同意' }

function People({ card, act, leader, me }: { card: Placed; act: Act; leader: boolean; me: string }) {
  return (
    <ul className="lm-dept-people" aria-label={card.name + ' 的成员'}>
      {card.people.map((person: DepartmentPerson) => (
        <li key={person.key}>
          <Avatar value={person.avatar} seed={person.key} size={26} />
          <span>
            <b>{person.name}</b>
            {person.key === me && <small>（我）</small>}
          </span>
          <em data-role={person.role}>{roleText[person.role]}</em>
          {leader && person.key !== me && (
            <>
              <button
                className="lm-btn small ghost"
                onClick={() =>
                  act(card, 'role', { key: person.key, role: person.role === 'leader' ? 'member' : 'leader' })
                }
              >
                {person.role === 'leader' ? '改为成员' : '设为负责人'}
              </button>
              <button
                className="lm-btn small ghost danger"
                onClick={() => act(card, 'remove', { key: person.key })}
              >
                移出
              </button>
            </>
          )}
        </li>
      ))}
    </ul>
  )
}

function Requests({ card, act }: { card: Placed; act: Act }) {
  const [list, setList] = useState<DepartmentRequest[]>()
  const [tick, setTick] = useState(0)
  useEffect(() => {
    void act(card, 'requests', {})
      .then((value) => setList((value as { requests?: DepartmentRequest[] })?.requests || []))
      .catch(() => setList([]))
    // act is rebuilt on every render; the list is read again after each decision.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [card.key, tick])
  if (!list) return <p className="lm-muted lm-small">正在读取加入申请…</p>
  if (!list.length) return <p className="lm-muted lm-small">没有待处理的加入申请。</p>
  return (
    <ul className="lm-dept-requests" aria-label={card.name + ' 的加入申请'}>
      {list.map((request) => (
        <li key={request.id}>
          <Avatar value={request.avatar} seed={request.key} size={26} />
          <b>{request.name}</b>
          <span className="lm-muted lm-small">想加入</span>
          <button
            className="lm-btn small"
            onClick={() =>
              act(card, 'decide', { request: request.id, approve: false }).then(() => setTick((n) => n + 1))
            }
          >
            拒绝
          </button>
          <button
            className="lm-btn small primary"
            onClick={() =>
              act(card, 'decide', { request: request.id, approve: true }).then(() => setTick((n) => n + 1))
            }
          >
            同意
          </button>
        </li>
      ))}
    </ul>
  )
}

type Act = (card: Placed, action: string, body: Record<string, unknown>) => Promise<unknown>

function Department({
  card,
  depth,
  me,
  pending,
  act,
  addChild,
  editing,
}: {
  card: Placed
  depth: number
  me: string
  pending: boolean
  act: Act
  addChild: (parent: Placed) => void
  editing: (card: Placed) => void
}) {
  const [open, setOpen] = useState<'' | 'people' | 'requests'>('')
  const role = roleOf(card, me, pending)
  const leader = role === 'leader'
  const leaders = card.people.filter((person) => person.role === 'leader')
  return (
    <li className="lm-dept" style={{ ['--depth' as string]: depth }} data-role={role || undefined}>
      <div className="lm-dept-row">
        {depth > 0 && <i className="lm-dept-branch" aria-hidden="true" />}
        <Avatar value={card.avatar} seed={card.key} size={36} />
        <span className="lm-dept-text">
          <b>{card.name}</b>
          <small>
            {card.people.length} 人 · 负责人 {leaders.map((person) => person.name).join('、') || '—'}
            {card.local ? ' · 托管在这台电脑' : ' · 由 ' + card.hostName + ' 托管'}
          </small>
        </span>
        <span className="lm-dept-faces" aria-hidden="true">
          {card.people.slice(0, 5).map((person) => (
            <Avatar key={person.key} value={person.avatar} seed={person.key} size={22} />
          ))}
        </span>
        {role && (
          <em className="lm-dept-role" data-role={role}>
            {roleText[role]}
          </em>
        )}
        <span className="lm-actions">
          <button
            className="lm-btn small ghost"
            aria-expanded={open === 'people'}
            onClick={() => setOpen(open === 'people' ? '' : 'people')}
          >
            成员
          </button>
          {leader && (
            <button
              className="lm-btn small ghost"
              aria-expanded={open === 'requests'}
              onClick={() => setOpen(open === 'requests' ? '' : 'requests')}
            >
              加入申请
            </button>
          )}
          {leader && (
            <button
              className="lm-btn small ghost"
              onClick={() => editing(card)}
              title="改名称、头像和上级部门"
            >
              <Glyph name="edit" size={14} />
              编辑
            </button>
          )}
          {leader && (
            <button
              className="lm-btn small ghost"
              onClick={() => addChild(card)}
              title="在这个部门下面再建一个"
            >
              <Glyph name="plus" size={14} />
              下级
            </button>
          )}
          {!role && (
            <button className="lm-btn small primary" onClick={() => act(card, 'join', {})}>
              申请加入
            </button>
          )}
          {(role === 'member' || role === 'leader') && (
            <button className="lm-btn small ghost" onClick={() => act(card, 'leave', {})}>
              退出
            </button>
          )}
        </span>
      </div>
      {open === 'people' && <People card={card} act={act} leader={leader} me={me} />}
      {open === 'requests' && leader && <Requests card={card} act={act} />}
    </li>
  )
}

function CreateDepartment({
  parent,
  options,
  close,
  done,
}: {
  parent?: Placed
  options: (Placed & { depth: number })[]
  close: () => void
  done: () => void
}) {
  const { api } = useApp()
  const [name, setName] = useState(''),
    [under, setUnder] = useState(parent?.key || ''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  return (
    <Dialog
      title={parent ? '在「' + parent.name + '」下面建部门' : '新建部门'}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            取消
          </button>
          <button className="lm-btn primary" form="lm-new-dept" disabled={busy || !name.trim()}>
            {busy ? '正在创建…' : '创建'}
          </button>
        </>
      }
    >
      <form
        id="lm-new-dept"
        className="lm-dept-form"
        onSubmit={(event) => {
          event.preventDefault()
          setBusy(true)
          setError('')
          api
            .request('/departments', { method: 'POST', body: JSON.stringify({ name, parent: under }) })
            .then(() => {
              done()
              close()
            })
            .catch((err: unknown) => setError(err instanceof Error ? err.message : '创建失败'))
            .finally(() => setBusy(false))
        }}
      >
        <label>
          部门名称
          <input
            className="lm-input"
            value={name}
            maxLength={40}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label>
          上级部门
          <select className="lm-input" value={under} onChange={(event) => setUnder(event.target.value)}>
            <option value="">没有（顶层部门）</option>
            {options.map((item) => (
              <option key={item.key} value={item.key}>
                {'　'.repeat(item.depth) + item.name}
              </option>
            ))}
          </select>
        </label>
        <p className="lm-muted lm-small" style={{ margin: 0 }}>
          部门由这台电脑托管，你是负责人；同一内网的人不用打开“内网可见”也能看到并申请加入，由负责人同意。头像随机，之后可以改。
        </p>
      </form>
    </Dialog>
  )
}

/* A leader's edit: name, avatar and where the department hangs, never under itself or its own branch. */
function EditDepartment({
  card,
  options,
  act,
  close,
}: {
  card: Placed
  options: (Placed & { depth: number })[]
  act: Act
  close: () => void
}) {
  const [name, setName] = useState(card.name),
    [avatar, setAvatar] = useState(card.avatar || ''),
    [under, setUnder] = useState(card.parent || ''),
    [busy, setBusy] = useState(false),
    [picking, setPicking] = useState(false),
    [error, setError] = useState('')
  const branch = new Set(flatten([card]).map((item) => item.key))
  const places = options.filter((item) => !branch.has(item.key))
  // A parent this computer cannot see right now (its host is offline) stays selectable as it is.
  const unseen = !!card.parent && !options.some((item) => item.key === card.parent)
  if (picking) {
    return (
      <AvatarPicker
        title={'给「' + name + '」选头像'}
        value={avatar}
        seed={card.key}
        save={async (next) => setAvatar(next)}
        close={() => setPicking(false)}
      />
    )
  }
  return (
    <Dialog
      title={'编辑部门「' + card.name + '」'}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            取消
          </button>
          <button className="lm-btn primary" form="lm-edit-dept" disabled={busy || !name.trim()}>
            {busy ? '正在保存…' : '保存'}
          </button>
        </>
      }
    >
      <form
        id="lm-edit-dept"
        className="lm-dept-form"
        onSubmit={(event) => {
          event.preventDefault()
          setBusy(true)
          setError('')
          const body: Record<string, unknown> = { name: name.trim(), parent: under }
          if (avatar !== (card.avatar || '')) body.avatar = avatar
          act(card, 'edit', body)
            .then(close)
            .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
            .finally(() => setBusy(false))
        }}
      >
        <div className="lm-dept-face">
          <Avatar value={avatar} seed={card.key} size={56} label={name} />
          <div>
            <button type="button" className="lm-btn small" onClick={() => setAvatar(randomAvatar(avatar))}>
              <Glyph name="refresh" size={14} />
              随机换一个
            </button>
            <button type="button" className="lm-btn small ghost" onClick={() => setPicking(true)}>
              <Glyph name="sparkle" size={14} />
              选择头像
            </button>
          </div>
        </div>
        <label>
          部门名称
          <input
            className="lm-input"
            value={name}
            maxLength={40}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label>
          上级部门
          <select className="lm-input" value={under} onChange={(event) => setUnder(event.target.value)}>
            <option value="">没有（顶层部门）</option>
            {unseen && <option value={card.parent}>现在看不到的上级部门（保持不变）</option>}
            {places.map((item) => (
              <option key={item.key} value={item.key}>
                {'　'.repeat(item.depth) + item.name}
              </option>
            ))}
          </select>
        </label>
        <p className="lm-muted lm-small" style={{ margin: 0 }}>
          只能挂到别的部门下面，不能挂在它自己或它的下级部门下面。成员和负责人不受影响。
        </p>
      </form>
    </Dialog>
  )
}

export function DepartmentsSection() {
  const { api, refresh } = useApp()
  const gate = useGate()
  const departments = useDepartments()
  const [creating, setCreating] = useState<{ parent?: Placed } | null>(null),
    [editing, setEditing] = useState<Placed | null>(null),
    [error, setError] = useState('')
  const value = departments.value
  const tree = useMemo(() => departmentTree(value), [value])
  const flat = useMemo(() => flatten(tree), [tree])
  const me = value?.me || ''
  const mine = flat.filter((item) => item.people.some((person) => person.key === me))
  const act: Act = (card, action, body) =>
    new Promise((resolve, reject) =>
      gate('config', '部门操作', () => {
        setError('')
        api
          .request(
            '/departments/' +
              (card.local ? 'local' : encodeURIComponent(card.host)) +
              '/' +
              encodeURIComponent(card.id) +
              '/' +
              action,
            {
              method: 'POST',
              body: JSON.stringify(body),
            },
          )
          .then((result) => {
            if (action !== 'requests' && action !== 'status') {
              departments.reload()
              refresh()
            }
            resolve(result)
          })
          .catch((err: unknown) => {
            setError(err instanceof Error ? err.message : '操作失败')
            reject(err)
          })
      }),
    )
  return (
    <section className="lm-section" aria-label="部门">
      <header>
        <div>
          <h2 className="lm-h2">部门</h2>
          <small>
            {mine.length
              ? '我在：' + mine.map((item) => item.name).join('、')
              : '可以加入一个部门，也可以不加入'}
          </small>
        </div>
        <button className="lm-btn small" onClick={() => gate('config', '新建部门', () => setCreating({}))}>
          <Glyph name="plus" size={14} />
          新建部门
        </button>
      </header>
      {error && <p className="lm-error">{error}</p>}
      {!value ? (
        <p className="lm-muted lm-small">正在内网里找部门…</p>
      ) : flat.length === 0 ? (
        <p className="lm-muted lm-small" style={{ margin: 0 }}>
          内网里还没有部门。新建一个后，同一内网的人就能看到并申请加入。
        </p>
      ) : (
        <ul className="lm-depts">
          {flat.map((item) => (
            <Department
              key={item.key}
              card={item}
              depth={item.depth}
              me={me}
              pending={value.pending?.[item.key] === 'pending'}
              act={act}
              addChild={(parent) => setCreating({ parent })}
              editing={setEditing}
            />
          ))}
        </ul>
      )}
      {editing && <EditDepartment card={editing} options={flat} act={act} close={() => setEditing(null)} />}
      {creating && (
        <CreateDepartment
          parent={creating.parent}
          options={flat}
          close={() => setCreating(null)}
          done={() => {
            departments.reload()
            refresh()
          }}
        />
      )}
    </section>
  )
}
