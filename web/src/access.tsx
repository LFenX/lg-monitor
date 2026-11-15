import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { API } from './api'
import { Glyph } from './glyphs'
import { CopyButton, Dialog, quote } from './parts'
import { useApp } from './state'
import type { Scope } from './types'

/*
 * Permissions only come up when something is actually blocked. A page asks with askAccess(need, what);
 * the shell shows one dialog that says why this page can only look, and the concrete way out for this
 * install. Nothing in the shell advertises tokens otherwise.
 */

export type Need = 'config' | 'admin'
export type AccessRequest = { need: Need; what: string }

const EVENT = 'lgmon-access'

export function askAccess(need: Need, what = '') {
  window.dispatchEvent(new CustomEvent<AccessRequest>(EVENT, { detail: { need, what } }))
}

export function onAccessRequest(listener: (request: AccessRequest) => void) {
  const handle = (event: Event) => listener((event as CustomEvent<AccessRequest>).detail)
  window.addEventListener(EVENT, handle)
  return () => window.removeEventListener(EVENT, handle)
}

export function allows(scope: Scope, need: Need) {
  return need === 'admin' ? scope === 'admin' : scope === 'config' || scope === 'admin'
}

/* Runs an action when this page may, otherwise explains what would let it. */
export function useGate() {
  const { scope } = useApp()
  return (need: Need, what: string, action: () => void) => {
    if (allows(scope, need)) action()
    else askAccess(need, what)
  }
}

const scopeText: Record<string, string> = {
  read: '只能查看',
  config: '可以查看和修改',
  admin: '可以修改一切',
}
export function scopeName(scope: Scope) {
  return scopeText[scope] || '只能查看'
}

export function TokenForm({
  complete,
  submit = '使用这个令牌',
}: {
  complete: (token: string, scope: Scope) => void
  submit?: string
}) {
  const [value, setValue] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  return (
    <form
      className="lm-token"
      onSubmit={(event) => {
        event.preventDefault()
        const token = value.trim()
        if (!token) return
        setBusy(true)
        setError('')
        const candidate = new API(token)
        const controller = new AbortController()
        pending.current?.abort()
        pending.current = controller
        void candidate
          .request('/collectors', { signal: controller.signal })
          .then(() => {
            if (!controller.signal.aborted) complete(token, candidate.scope)
          })
          .catch((err: unknown) => {
            if (!controller.signal.aborted)
              setError(err instanceof Error ? err.message : '连接失败，请检查地址和令牌')
          })
          .finally(() => {
            if (!controller.signal.aborted) setBusy(false)
          })
      }}
    >
      <input
        className="lm-input"
        type="password"
        aria-label="访问令牌"
        autoComplete="off"
        spellCheck={false}
        required
        disabled={busy}
        maxLength={512}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder="粘贴令牌"
      />
      <button className="lm-btn primary" disabled={busy}>
        {busy ? '正在核对…' : submit}
      </button>
      {error && <p className="lm-error">{error}</p>}
    </form>
  )
}

export function tokenCommand(install: { executable: string; home: string } | undefined, need: Need) {
  const program = install?.executable ? '& ' + quote(install.executable) : 'lgmon'
  return (
    program +
    ' token create --scope ' +
    need +
    ' --name 网页' +
    (install?.home ? ' --home ' + quote(install.home) : '')
  )
}

/*
 * Older pages open native modal dialogs, which sit in the browser's top layer above any z-index. The access
 * dialog can be asked for from inside one, so it joins the top layer too and lands on top of it.
 */
function TopLayer({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (typeof dialog.showModal === 'function') dialog.showModal()
    else dialog.setAttribute('open', '')
    return () => {
      if (typeof dialog.close === 'function') dialog.close()
    }
  }, [])
  return (
    <dialog ref={ref} className="lm-top-layer" onCancel={(event) => event.preventDefault()}>
      {children}
    </dialog>
  )
}

export function AccessDialog({
  request,
  local,
  close,
  login,
}: {
  request: AccessRequest
  local: boolean
  close: () => void
  login: (token: string, scope: Scope) => void
}) {
  const { guide } = useApp()
  const install = guide?.install
  const { need, what } = request
  const service = install ? !install.personal : false
  const settingsFile = install?.home ? install.home + '\\config\\settings.yaml' : 'settings.yaml'
  const why =
    need === 'admin'
      ? '修改设置和保存密钥只接受管理员令牌，在本机打开的网页也一样。'
      : !local
        ? '你是从别的设备打开的这个网页，用的令牌只能查看。'
        : service
          ? 'lgmon 以 Windows 服务运行，在本机打开的网页默认只能查看，免得这台电脑上的其他程序借网页改动监控。'
          : 'settings.yaml 里把本机网页设成了只能查看（localAccess: read）。'
  return (
    <TopLayer>
      <Dialog title={need === 'admin' ? '需要管理员权限' : '需要修改权限'} close={close}>
        <p style={{ margin: 0 }}>
          {what ? <strong>{what}</strong> : '这个操作'}需要{need === 'admin' ? '管理员' : '修改'}
          权限，这个网页现在只能查看。{why}
        </p>
        <ol className="lm-access-ways">
          {need === 'config' && local && (
            <li>
              <strong>让本机网页可以修改</strong>
              <span>
                在 <code>{settingsFile}</code> 里{service ? '加一行' : '改成'}{' '}
                <code>localAccess: config</code>
                ，保存后刷新这个网页，不用重启。
              </span>
            </li>
          )}
          <li>
            <strong>{need === 'config' && local ? '或者用一个令牌' : '用一个令牌'}</strong>
            <span>
              在这台电脑的 PowerShell{service ? '（以管理员身份打开）' : ''}
              里运行下面这行，把输出的令牌粘贴进来。令牌只保存在这个标签页，关掉就失效。
            </span>
            <div className="lm-code">
              <code>{tokenCommand(install, need)}</code>
              <CopyButton text={tokenCommand(install, need)} />
            </div>
            <TokenForm complete={login} />
          </li>
        </ol>
        <p className="lm-muted lm-small" style={{ margin: 0 }}>
          <Glyph name="agent" size={14} /> 不想改权限也可以：让接入的 Agent 来做，它用自己的令牌。
        </p>
      </Dialog>
    </TopLayer>
  )
}

/* A one-line note on read-only pages that still explains itself and offers the way out. */
export function ReadOnlyNote({ need = 'config', what }: { need?: Need; what: string }) {
  const { scope } = useApp()
  if (allows(scope, need)) return null
  return (
    <div className="lm-note" data-tone="info" role="note">
      <Glyph name="lock" size={16} />
      <span>
        这个网页现在只能查看，{what}需要{need === 'admin' ? '管理员' : '修改'}权限。
      </span>
      <button className="lm-btn small" onClick={() => askAccess(need, what)}>
        怎么获得
      </button>
    </div>
  )
}
