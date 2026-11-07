import { useEffect, useRef } from 'react'
import type { ReactNode } from 'react'
import { targetState } from './model'

export type IconName =
  | 'overview'
  | 'server'
  | 'alert'
  | 'board'
  | 'bell'
  | 'sun'
  | 'moon'
  | 'refresh'
  | 'arrow'
  | 'check'
  | 'key'
  | 'close'
  | 'clock'
  | 'pulse'
const paths: Record<IconName, ReactNode> = {
  overview: (
    <>
      <rect x="3" y="3" width="7" height="7" rx="1.5" />
      <rect x="14" y="3" width="7" height="7" rx="1.5" />
      <rect x="3" y="14" width="7" height="7" rx="1.5" />
      <rect x="14" y="14" width="7" height="7" rx="1.5" />
    </>
  ),
  server: (
    <>
      <rect x="3" y="3" width="18" height="8" rx="2" />
      <rect x="3" y="13" width="18" height="8" rx="2" />
      <path d="M7 7h.01M7 17h.01M11 7h6M11 17h6" />
    </>
  ),
  alert: (
    <>
      <path d="m10.3 4-8 14a2 2 0 0 0 1.7 3h16a2 2 0 0 0 1.7-3l-8-14a2 2 0 0 0-3.4 0Z" />
      <path d="M12 9v4m0 4h.01" />
    </>
  ),
  board: (
    <>
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M3 9h18M9 9v12" />
    </>
  ),
  bell: (
    <>
      <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M9 21h6" />
    </>
  ),
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5M17.5 17.5 19 19M5 19l1.5-1.5M17.5 6.5 19 5" />
    </>
  ),
  moon: <path d="M21 12.8A9 9 0 0 1 11.2 3 9 9 0 1 0 21 12.8Z" />,
  refresh: (
    <>
      <path d="M20 8a8 8 0 1 0 0 8M20 3v5h-5" />
    </>
  ),
  arrow: <path d="M5 12h14m-5-5 5 5-5 5" />,
  check: <path d="m5 12 4 4L19 6" />,
  key: (
    <>
      <circle cx="8" cy="8" r="5" />
      <path d="m12 12 9 9m-5-5 3-3m-6 0 3-3" />
    </>
  ),
  close: <path d="m6 6 12 12M6 18 18 6" />,
  clock: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </>
  ),
  pulse: <path d="M2 12h5l3-8 4 16 3-8h5" />,
}
export function Icon({ name, size = 20 }: { name: IconName; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name]}
    </svg>
  )
}
export function Range({ hours, change }: { hours: number; change: (value: number) => void }) {
  return (
    <label className="range-control">
      <Icon name="clock" size={16} />
      <span className="sr-only">时间范围</span>
      <select value={hours} onChange={(event) => change(Number(event.target.value))}>
        <option value={1}>最近 1 小时</option>
        <option value={6}>最近 6 小时</option>
        <option value={24}>最近 24 小时</option>
        <option value={168}>最近 7 天</option>
      </select>
    </label>
  )
}
export function Badge({ state, children }: { state: string; children?: ReactNode }) {
  return (
    <span className={'badge badge-' + state}>
      <span className="dot" />
      {children || targetState(state)}
    </span>
  )
}
export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <span className="empty-icon">
        <Icon name="pulse" size={26} />
      </span>
      <strong>{title}</strong>
      {children && <p>{children}</p>}
    </div>
  )
}
export function ErrorNotice({ message }: { message?: string }) {
  return message ? (
    <div className="notice notice-error" role="alert">
      <Icon name="alert" />
      <span>{message}</span>
    </div>
  ) : null
}
export function Panel({
  title,
  extra,
  children,
  className = '',
}: {
  title: string
  extra?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={'panel ' + className}>
      <div className="panel-heading">
        <h2>{title}</h2>
        {extra}
      </div>
      {children}
    </section>
  )
}
export function Modal({ title, close, children }: { title: string; close: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = ref.current!
    dialog.showModal()
    return () => dialog.close()
  }, [])
  return (
    <dialog
      ref={ref}
      className="modal"
      aria-label={title}
      onCancel={(event) => {
        event.preventDefault()
        close()
      }}
    >
      <div className="modal-heading">
        <h2>{title}</h2>
        <button className="icon-button" aria-label="关闭" onClick={close}>
          <Icon name="close" />
        </button>
      </div>
      {children}
    </dialog>
  )
}
