import { useRef, useState } from 'react'
import { Glyph } from './glyphs'
import { useGate } from './access'
import { useApp } from './state'
import type { LANOutgoing, Profile } from './types'
import './avatar.css'

/*
 * People on the network are shown by the account each computer has: a nickname and an avatar.
 * The bundled avatars come from fiora (MIT, see avatars/LICENSE); only their key travels between
 * computers, and each one draws its own copy. An uploaded picture travels as a small data URL.
 */

const files = import.meta.glob('./avatars/*.{jpg,gif}', {
  eager: true,
  query: '?url',
  import: 'default',
}) as Record<string, string>
const presets: Record<string, string> = Object.fromEntries(
  Object.entries(files).map(([path, url]) => [path.replace(/^.*\/|\.\w+$/g, ''), url]),
)
const stills = Object.keys(presets)
  .filter((key) => key.startsWith('fiora-'))
  .sort((a, b) => Number(a.slice(6)) - Number(b.slice(6)))

function hash(value: string) {
  let result = 2166136261
  for (let index = 0; index < value.length; index++) {
    result ^= value.charCodeAt(index)
    result = Math.imul(result, 16777619)
  }
  return result >>> 0
}

/* Where to draw an avatar from; someone on an older version has none, so their seed picks a still one. */
/* Another bundled avatar than the current one, for anything that can be given a fresh face. */
export function randomAvatar(current = '') {
  const choices = Object.keys(presets).filter((key) => key !== current)
  return choices[Math.floor(Math.random() * choices.length)] || current
}

export function avatarSrc(value: string | undefined, seed = '') {
  if (value?.startsWith('data:image/')) return value
  if (value && presets[value]) return presets[value]
  return presets[stills[hash(seed || 'lgmon') % stills.length]] || ''
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
  const [broken, setBroken] = useState(false)
  const src = avatarSrc(value, seed)
  if (broken || !src)
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
      onError={() => setBroken(true)}
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
  const input = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(''),
    [error, setError] = useState(''),
    [editing, setEditing] = useState(false),
    [name, setName] = useState('')
  const nickname = lan?.name || ''
  const save = (what: string, body: Record<string, unknown>) => {
    setBusy(what)
    setError('')
    return api
      .request<Profile>('/profile', { method: 'PUT', body: JSON.stringify(body) })
      .then((saved) => {
        setEditing(false)
        if (saved?.avatar !== undefined) showProfile(saved)
        refresh()
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
      .finally(() => setBusy(''))
  }
  const shuffle = () => gate('config', '换头像', () => void save('shuffle', { shuffle: true }))
  return (
    <section className="lm-profile" aria-label="我的账户">
      <button
        className="lm-profile-face"
        onClick={shuffle}
        disabled={!!busy}
        title="点一下，随机换一个头像"
        aria-label="随机换一个头像"
        data-busy={busy === 'shuffle' || undefined}
      >
        <Avatar value={lan?.avatar} seed={lan?.fingerprint} size={64} />
        <span className="lm-profile-dice" aria-hidden>
          <Glyph name="refresh" size={13} />
        </span>
      </button>
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
          <button
            className="lm-btn small"
            disabled={!!busy}
            onClick={() => gate('config', '上传头像', () => input.current?.click())}
          >
            <Glyph name="upload" size={14} />
            {busy === 'upload' ? '正在上传…' : '上传头像'}
          </button>
          <button className="lm-btn small ghost" disabled={!!busy} onClick={shuffle}>
            随机换一个
          </button>
          <input
            ref={input}
            type="file"
            accept="image/png,image/jpeg,image/gif,image/webp"
            hidden
            onChange={(event) => {
              const file = event.target.files?.[0]
              event.target.value = ''
              if (!file) return
              setBusy('upload')
              setError('')
              shrinkAvatar(file)
                .then((avatar) => save('upload', { avatar }))
                .catch((err: unknown) => {
                  setError(err instanceof Error ? err.message : '上传失败')
                  setBusy('')
                })
            }}
          />
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
