/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useRef, useState } from 'react'
import { Glyph } from './glyphs'
import { useGate } from './access'
import { critterKeys, critterSrc } from './critters'
import { Dialog } from './parts'
import { useApp } from './state'
import type { LANOutgoing, Profile } from './types'
import './avatar.css'

/*
 * People on the network are shown by the account each computer has: a nickname and an avatar.
 * Three sets are bundled (see avatars/LICENSE): lgmon's own little animals, Lorelei portraits and fiora's.
 * Only a key travels between computers, and each one draws its own copy. An uploaded picture travels as a
 * small data URL.
 */

const files = import.meta.glob('./avatars/*.{jpg,gif,svg}', {
  eager: true,
  query: '?url',
  import: 'default',
}) as Record<string, string>
const presets: Record<string, string> = Object.fromEntries(
  Object.entries(files).map(([path, url]) => [path.replace(/^.*\/|\.\w+$/g, ''), url]),
)
const numbered = (prefix: string) =>
  Object.keys(presets)
    .filter((key) => key.startsWith(prefix))
    .sort((a, b) => Number(a.slice(prefix.length)) - Number(b.slice(prefix.length)))
const stills = numbered('fiora-')

/* The sets as the picker shows them; every key here is one the server accepts. */
export const avatarSets: { id: string; label: string; keys: string[] }[] = [
  { id: 'critters', label: '小动物', keys: critterKeys },
  { id: 'lorelei', label: '插画', keys: numbered('lorelei-') },
  { id: 'anime', label: '动漫', keys: stills },
  { id: 'moving', label: '会动的', keys: numbered('huaji-') },
]

function hash(value: string) {
  let result = 2166136261
  for (let index = 0; index < value.length; index++) {
    result ^= value.charCodeAt(index)
    result = Math.imul(result, 16777619)
  }
  return result >>> 0
}

/* Another bundled avatar than the current one, for anything that can be given a fresh face. */
export function randomAvatar(current = '') {
  const choices = avatarSets.flatMap((set) => set.keys).filter((key) => key !== current)
  return choices[Math.floor(Math.random() * choices.length)] || current
}

/*
 * Picking an avatar: the choice up top, large; below it every bundled one by set, or a picture of one's own.
 * Nothing is saved until "用这个".
 */
export function AvatarPicker({
  value,
  seed,
  title = '选择头像',
  save,
  close,
}: {
  value?: string
  seed?: string
  title?: string
  save: (avatar: string) => Promise<unknown>
  close: () => void
}) {
  const [chosen, setChosen] = useState(value || ''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const file = useRef<HTMLInputElement>(null)
  const custom = chosen.startsWith('data:image/')
  const [set, setSet] = useState(
    () => avatarSets.find((item) => item.keys.includes(value || ''))?.id || avatarSets[0].id,
  )
  const shown = avatarSets.find((item) => item.id === set) || avatarSets[0]
  return (
    <Dialog
      title={title}
      close={close}
      footer={
        <>
          {error && <p className="lm-error">{error}</p>}
          <button className="lm-btn" onClick={close}>
            取消
          </button>
          <button
            className="lm-btn primary"
            disabled={busy || !chosen || chosen === value}
            onClick={() => {
              setBusy(true)
              setError('')
              save(chosen)
                .then(close)
                .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
                .finally(() => setBusy(false))
            }}
          >
            {busy ? '正在保存…' : '用这个'}
          </button>
        </>
      }
    >
      <div className="lm-avatar-picker">
        <div className="lm-avatar-picker-now">
          <Avatar value={chosen} seed={seed} size={72} label="所选" />
          <div className="lm-avatar-picker-now-t">
            <b>{custom ? '自己的图片' : chosen === value ? '现在的头像' : '换成这个'}</b>
            <span>选择后可以先预览</span>
            <div className="lm-avatar-picker-now-acts">
              <button
                type="button"
                className="lm-btn small"
                onClick={() => {
                  const next = randomAvatar(chosen)
                  setChosen(next)
                  setSet(avatarSets.find((item) => item.keys.includes(next))?.id || set)
                }}
              >
                <Glyph name="refresh" size={14} />
                随机一个
              </button>
              <button type="button" className="lm-btn small ghost" onClick={() => file.current?.click()}>
                <Glyph name="upload" size={14} />
                上传图片
              </button>
              <input
                ref={file}
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                hidden
                aria-label="头像图片"
                onChange={(event) => {
                  const picked = event.target.files?.[0]
                  event.target.value = ''
                  if (!picked) return
                  setError('')
                  shrinkAvatar(picked)
                    .then(setChosen)
                    .catch((err: unknown) => setError(err instanceof Error ? err.message : '这张图片用不了'))
                }}
              />
            </div>
          </div>
        </div>
        <div className="lm-avatar-picker-tabs" role="tablist" aria-label="头像分类">
          {avatarSets.map((item) => (
            <button
              key={item.id}
              role="tab"
              aria-selected={item.id === shown.id}
              onClick={() => setSet(item.id)}
            >
              {item.label}
              <small>{item.keys.length}</small>
            </button>
          ))}
        </div>
        <div className="lm-avatar-picker-grid" role="listbox" aria-label={shown.label + '头像'}>
          {shown.keys.map((key, index) => (
            <button
              key={key}
              role="option"
              aria-selected={key === chosen}
              aria-label={shown.label + ' ' + (index + 1)}
              onClick={() => setChosen(key)}
            >
              <Avatar value={key} size={56} />
              {key === chosen && (
                <span className="lm-avatar-picker-check" aria-hidden="true">
                  <Glyph name="check" size={12} />
                </span>
              )}
            </button>
          ))}
        </div>
      </div>
    </Dialog>
  )
}

export function avatarSrc(value: string | undefined, seed = '') {
  if (value?.startsWith('data:image/')) return value
  if (value && Object.hasOwn(presets, value)) return presets[value]
  const critter = value ? critterSrc(value) : ''
  if (critter) return critter
  // Discovery can show a short fingerprint before the directory returns the full one.
  const fingerprint = seed.replace(/[-:]/g, '')
  const initial = /^(?:[a-f0-9]{16}|[a-f0-9]{64})$/i.test(fingerprint)
    ? fingerprint.slice(0, 16).toUpperCase()
    : seed
  return presets[stills[hash(initial || 'lgmon') % stills.length]] || ''
}

export function Avatar({
  value,
  seed,
  size = 24,
  label,
}: {
  value?: string
  seed?: string
  size?: number
  label?: string
}) {
  const [broken, setBroken] = useState('')
  const src = avatarSrc(value, seed)
  if (!src || broken === src)
    return (
      <span
        className="lm-avatar fallback"
        style={{ width: size, height: size, fontSize: size * 0.46 }}
        aria-hidden
      >
        {(label || seed || '?').trim().slice(0, 1).toUpperCase()}
      </span>
    )
  return (
    <img
      className="lm-avatar"
      src={src}
      width={size}
      height={size}
      alt={label ? label + '的头像' : ''}
      aria-hidden={label ? undefined : true}
      draggable={false}
      onError={() => setBroken(src)}
    />
  )
}

const limit = 60 * 1024

function read(file: Blob) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(new Error('读取图片失败'))
    reader.readAsDataURL(file)
  })
}

/*
 * Shrinks a picture to a 160 px square, cut from its middle, so it fits the account and every board refresh.
 * A small GIF is kept as it is, which keeps it moving.
 */
export async function shrinkAvatar(file: File): Promise<string> {
  if (!/^image\/(png|jpeg|gif|webp)$/.test(file.type)) throw new Error('请选择 PNG、JPEG、GIF 或 WebP 图片')
  if (file.type === 'image/gif' && file.size <= 44 * 1024) return read(file)
  const url = URL.createObjectURL(file)
  try {
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const element = new Image()
      element.onload = () => resolve(element)
      element.onerror = () => reject(new Error('这张图片打不开，请换一张'))
      element.src = url
    })
    const side = Math.min(image.naturalWidth, image.naturalHeight)
    if (!side) throw new Error('这张图片打不开，请换一张')
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 160
    const context = canvas.getContext('2d')
    if (!context) throw new Error('这个窗口不能处理图片')
    context.drawImage(
      image,
      (image.naturalWidth - side) / 2,
      (image.naturalHeight - side) / 2,
      side,
      side,
      0,
      0,
      160,
      160,
    )
    for (const quality of [0.86, 0.7, 0.5]) {
      let result = canvas.toDataURL('image/webp', quality)
      if (!result.startsWith('data:image/webp')) result = canvas.toDataURL('image/jpeg', quality)
      if (result.length <= limit) return result
    }
    throw new Error('图片压缩后还是太大，请换一张简单一点的')
  } finally {
    URL.revokeObjectURL(url)
  }
}

/* This computer's account: click the avatar for another one, upload a picture, or rename. */
export function ProfileCard() {
  const { api, lan, refresh, showProfile } = useApp()
  const gate = useGate()
  const [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [editing, setEditing] = useState(false),
    [picking, setPicking] = useState(false),
    [name, setName] = useState('')
  const nickname = lan?.name || ''
  const put = (body: Record<string, unknown>) =>
    api.request<Profile>('/profile', { method: 'PUT', body: JSON.stringify(body) }).then((saved) => {
      if (saved?.avatar !== undefined) showProfile(saved)
      refresh()
    })
  const save = (what: string, body: Record<string, unknown>) => {
    setBusy(what)
    setError('')
    return put(body)
      .then(() => setEditing(false))
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
      .finally(() => setBusy(''))
  }
  const shuffle = () => gate('config', '换头像', () => void save('shuffle', { shuffle: true }))
  const pick = () => gate('config', '换头像', () => setPicking(true))
  return (
    <section className="lm-profile" aria-label="我的账户">
      <button
        className="lm-profile-face"
        onClick={pick}
        disabled={!!busy}
        title="选一个头像"
        aria-label="我的头像，点一下选择"
        data-busy={busy === 'shuffle' || undefined}
      >
        <Avatar value={lan?.avatar} seed={lan?.fingerprint} size={64} />
        <span className="lm-profile-dice" aria-hidden>
          <Glyph name="edit" size={13} />
        </span>
      </button>
      {picking && (
        <AvatarPicker
          value={lan?.avatar}
          seed={lan?.fingerprint}
          save={(avatar) => put({ avatar })}
          close={() => setPicking(false)}
        />
      )}
      <div className="lm-profile-text">
        {editing ? (
          <form
            className="lm-profile-rename"
            onSubmit={(event) => {
              event.preventDefault()
              void save('name', { nickname: name })
            }}
          >
            <input
              className="lm-input"
              maxLength={24}
              value={name}
              autoFocus
              onChange={(event) => setName(event.target.value)}
              aria-label="昵称"
            />
            <button className="lm-btn small primary" disabled={!!busy || !name.trim()}>
              保存
            </button>
            <button type="button" className="lm-btn small ghost" onClick={() => setEditing(false)}>
              取消
            </button>
          </form>
        ) : (
          <strong className="lm-profile-name">{nickname || '…'}</strong>
        )}
        <p className="lm-muted lm-small">
          内网里的其他人看到这个昵称和头像，不会看到电脑名。账户跟着这台电脑。
        </p>
        <div className="lm-profile-actions">
          {!editing && (
            <button
              className="lm-btn small"
              onClick={() =>
                gate('config', '改昵称', () => {
                  setName(nickname)
                  setEditing(true)
                })
              }
            >
              改昵称
            </button>
          )}
          <button className="lm-btn small" disabled={!!busy} onClick={pick}>
            <Glyph name="sparkle" size={14} />
            选择头像
          </button>
          <button className="lm-btn small ghost" disabled={!!busy} onClick={shuffle}>
            随机换一个
          </button>
        </div>
        {error && <p className="lm-error">{error}</p>}
      </div>
    </section>
  )
}

/* Other people's boards, one group per person (their computer's fingerprint), in the order first seen. */
export function byOwner(items: LANOutgoing[]): [string, LANOutgoing[]][] {
  const groups = new Map<string, LANOutgoing[]>()
  for (const item of items) groups.set(item.fingerprint, [...(groups.get(item.fingerprint) || []), item])
  return [...groups]
}
