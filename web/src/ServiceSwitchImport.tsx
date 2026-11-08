import { Fragment, useEffect, useRef, useState } from 'react'
import { useApp } from './state'
import { ErrorNotice, Modal } from './ui'
import type { Collector, Target } from './types'

type ImportItem = {
  ref: string
  kind: string
  name: string
  autostart: string
  action: 'create' | 'update' | 'unchanged' | 'covered' | 'missing' | 'skipped'
  targetId: string
  coveredBy: string | null
  reason: string | null
  target: Target | null
}
type ImportPreview = {
  collector: { id: string; hostname: string }
  sha256: string
  readAt: number
  targetsVersion: string
  items: ImportItem[]
}
type ImportResult = { created: string[]; updated: string[]; removed: string[]; targetsVersion: string }
const actions = {
  create: '新建',
  update: '更新',
  unchanged: '未变化',
  covered: '已有目标覆盖',
  missing: '源中已缺失',
  skipped: '跳过',
}

function waitForRead(delay: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) return reject(new DOMException('已取消', 'AbortError'))
    const abort = () => {
      clearTimeout(timer)
      reject(new DOMException('已取消', 'AbortError'))
    }
    const timer = setTimeout(
      () => {
        signal.removeEventListener('abort', abort)
        resolve()
      },
      Math.max(0, delay),
    )
    signal.addEventListener('abort', abort, { once: true })
  })
}

export function ServiceSwitchImport({ host }: { host: Collector }) {
  const { api, scope, refresh, now } = useApp()
  const [open, setOpen] = useState(false)
  const [preview, setPreview] = useState<ImportPreview>()
  const [selected, setSelected] = useState<string[]>([])
  const [publicProbes, setPublicProbes] = useState(true)
  const [removeMissing, setRemoveMissing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<ImportResult>()
  const controller = useRef<AbortController | undefined>(undefined)
  const nextRead = useRef(0)
  const canSave = scope === 'config' || scope === 'admin'
  useEffect(() => () => controller.current?.abort(), [])

  const request = async (save: boolean, publicFlag = publicProbes) => {
    controller.current?.abort()
    const work = new AbortController()
    controller.current = work
    setBusy(true)
    setError('')
    setResult(undefined)
    try {
      await waitForRead(nextRead.current - now(), work.signal)
      nextRead.current = now() + 2000
      const path = '/collectors/' + encodeURIComponent(host.id) + '/service-switch'
      if (save && preview) {
        const saved = await api.request<ImportResult>(path + '/import', {
          method: 'POST',
          signal: work.signal,
          body: JSON.stringify({
            sha256: preview.sha256,
            targetsVersion: preview.targetsVersion,
            refs: selected,
            removeMissing,
            publicProbes: publicFlag,
          }),
        })
        if (!work.signal.aborted) {
          setResult(saved)
          setPreview(undefined)
          refresh()
        }
      } else {
        setPreview(undefined)
        const value = await api.request<ImportPreview>(path + '?publicProbes=' + publicFlag, {
          signal: work.signal,
        })
        if (!work.signal.aborted) {
          setPreview(value)
          setSelected(
            value.items
              .filter((item) => item.action === 'create' || item.action === 'update')
              .map((item) => item.ref),
          )
          setRemoveMissing(false)
        }
      }
    } catch (err) {
      if (!work.signal.aborted) setError(err instanceof Error ? err.message : '服务开关导入失败')
    } finally {
      if (!work.signal.aborted) {
        nextRead.current = now() + 2000
        setBusy(false)
      }
    }
  }
  const close = () => {
    controller.current?.abort()
    setOpen(false)
    setBusy(false)
  }
  return (
    <>
      <button
        className="text-link"
        onClick={() => {
          setOpen(true)
          void request(false)
        }}
      >
        从服务开关导入
      </button>
      {open && (
        <Modal title={'从服务开关导入 · ' + (host.hostname || host.id)} close={close}>
          <div className="service-switch-import">
            <p className="muted">
              读取采集器本机已设置的 services.json。确认后仅保存监控目标，告警规则需另行添加。
            </p>
            <label className="inline">
              <input
                type="checkbox"
                checked={publicProbes}
                disabled={busy}
                onChange={(event) => {
                  const value = event.target.checked
                  setPublicProbes(value)
                  setPreview(undefined)
                  void request(false, value)
                }}
              />
              生成公网域名探测
            </label>
            <ErrorNotice message={error} />
            {busy && <p role="status">正在读取并核对配置…</p>}
            {result && (
              <p className="notice notice-success" role="status">
                已创建 {result.created.length} 个、更新 {result.updated.length} 个、删除{' '}
                {result.removed.length} 个目标。请按需要添加告警规则。
              </p>
            )}
            {preview && (
              <>
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>选择</th>
                        <th>目标</th>
                        <th>动作</th>
                        <th>说明 / 完整目标</th>
                      </tr>
                    </thead>
                    <tbody>
                      {preview.items.map((item, index) => (
                        <Fragment key={item.ref + ':' + index}>
                          <tr>
                            <td>
                              {(item.action === 'create' || item.action === 'update') && (
                                <input
                                  type="checkbox"
                                  aria-label={'导入 ' + item.name}
                                  disabled={busy}
                                  checked={selected.includes(item.ref)}
                                  onChange={(event) =>
                                    setSelected((old) =>
                                      event.target.checked
                                        ? [...old, item.ref]
                                        : old.filter((ref) => ref !== item.ref),
                                    )
                                  }
                                />
                              )}
                            </td>
                            <td>
                              <strong>{item.name}</strong>
                              <div className="mono small">{item.targetId || item.ref}</div>
                              {item.autostart === 'off' && <p>开机不自动启动</p>}
                            </td>
                            <td>{actions[item.action]}</td>
                            <td>{item.reason || (item.coveredBy ? '已有目标：' + item.coveredBy : '')}</td>
                          </tr>
                          {item.target && (
                            <tr className="service-switch-detail">
                              <td colSpan={4}>
                                <details>
                                  <summary>查看将保存的完整目标</summary>
                                  <pre>{JSON.stringify(item.target, null, 2)}</pre>
                                </details>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      ))}
                    </tbody>
                  </table>
                </div>
                {preview.items.some((item) => item.action === 'missing') && (
                  <label className="inline">
                    <input
                      type="checkbox"
                      checked={removeMissing}
                      disabled={busy}
                      onChange={(event) => setRemoveMissing(event.target.checked)}
                    />
                    一并删除所有“源中已缺失”的导入目标（默认保留）
                  </label>
                )}
                {!preview.items.length && <p>源中没有可导入的项目。</p>}
              </>
            )}
            {!canSave && <p className="muted">当前为只读权限；保存需要 config 或 admin 令牌。</p>}
            <div className="actions">
              <button disabled={busy} onClick={() => void request(false)}>
                重新预览
              </button>
              <button
                className="primary"
                disabled={!canSave || busy || !preview || (!selected.length && !removeMissing)}
                onClick={() => void request(true)}
              >
                确认并保存所选目标
              </button>
            </div>
          </div>
        </Modal>
      )}
    </>
  )
}
