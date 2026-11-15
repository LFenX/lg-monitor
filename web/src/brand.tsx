import type { CSSProperties } from 'react'
import './brand.css'

/* The mark is the product's own heartbeat bar: four checks on a warm tile, the newest one brightest. */
export function BrandMark({ size = 28 }: { size?: number }) {
  return (
    <svg
      className="brand-mark"
      viewBox="0 0 32 32"
      width={size}
      height={size}
      aria-hidden="true"
      focusable="false"
    >
      <rect width="32" height="32" rx="9" fill="var(--brand, #c96442)" />
      {[0.42, 0.6, 0.8, 1].map((opacity, index) => (
        <rect
          key={index}
          x={7 + index * 5}
          y="9.5"
          width="3"
          height="13"
          rx="1.5"
          fill="#faf9f5"
          opacity={opacity}
        />
      ))}
    </svg>
  )
}

export function Wordmark() {
  return (
    <span className="wordmark" aria-label="lgmon">
      lgmon
    </span>
  )
}

/* The assistant is a single four-point spark; it turns slowly while an answer is being written. */
export function AssistantMark({
  size = 28,
  think = false,
  glow = false,
}: {
  size?: number
  think?: boolean
  glow?: boolean
}) {
  const classes = ['am', think && 'think', glow && 'glow'].filter(Boolean).join(' ')
  return (
    <span className={classes} style={{ '--am': size + 'px' } as CSSProperties} aria-hidden="true">
      <svg viewBox="0 0 24 24" width={size} height={size} focusable="false">
        <path
          className="am-spark"
          d="M12 3c.7 4.6 3.4 7.3 8 8-4.6.7-7.3 3.4-8 8-.7-4.6-3.4-7.3-8-8 4.6-.7 7.3-3.4 8-8z"
        />
      </svg>
    </span>
  )
}
