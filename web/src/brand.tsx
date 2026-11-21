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
      <metadata>Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor</metadata>
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
