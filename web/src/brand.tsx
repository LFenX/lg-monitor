import { useId } from 'react'
import type { CSSProperties } from 'react'
import './brand.css'

/*
 * The mark: an open radar ring on an ink tile, a pulse crossing it, and a live signal dot sitting in the ring's gap.
 * The gap is where the ring "listens"; the dot is what it heard.
 */
export function BrandMark({ size = 34 }: { size?: number }) {
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  return (
    <svg
      className="brand-mark"
      viewBox="0 0 40 40"
      width={size}
      height={size}
      aria-hidden="true"
      focusable="false"
    >
      <defs>
        <linearGradient id={'t' + uid} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#2a3150" />
          <stop offset="1" stopColor="#0b0d16" />
        </linearGradient>
        <linearGradient id={'p' + uid} x1="0" y1="0" x2="1" y2="0">
          <stop offset="0" stopColor="#9fb4ff" />
          <stop offset="1" stopColor="#ffffff" />
        </linearGradient>
      </defs>
      <rect x="0.5" y="0.5" width="39" height="39" rx="11" fill={'url(#t' + uid + ')'} />
      <rect
        x="0.5"
        y="0.5"
        width="39"
        height="39"
        rx="11"
        fill="none"
        stroke="#ffffff"
        strokeOpacity="0.12"
      />
      <path
        d="M31.28 15.9A12 12 0 1 1 24.1 8.72"
        fill="none"
        stroke="#ffffff"
        strokeOpacity="0.92"
        strokeWidth="2.4"
        strokeLinecap="round"
      />
      <path
        d="M10.5 20.5H14L16.2 15.5L19.4 25L22.2 17.5L23.8 20.5H28"
        fill="none"
        stroke={'url(#p' + uid + ')'}
        strokeWidth="2.1"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle cx="28.5" cy="11.5" r="2.6" fill="#45e3a5" />
      <circle cx="28.5" cy="11.5" r="4.6" fill="#45e3a5" opacity="0.22" />
    </svg>
  )
}

export function Wordmark({ caption = true }: { caption?: boolean }) {
  return (
    <span className="wordmark">
      <span className="wm-name" aria-label="lg-monitor">
        <em>lg</em>
        <span>monitor</span>
      </span>
      {caption && <span className="wm-caption">WATCH EVERYTHING</span>}
    </span>
  )
}

const SPARK = 'M0 -4C.5 -1.2 1.2 -.5 4 0C1.2 .5 .5 1.2 0 4C-.5 1.2 -1.2 .5 -4 0C-1.2 -.5 -.5 -1.2 0 -4Z'

/* The assistant's mark: a breathing core inside two crossed orbits. While it thinks the orbits spin up and the sparks hurry. */
export function AssistantMark({
  size = 28,
  think = false,
  glow = false,
}: {
  size?: number
  think?: boolean
  glow?: boolean
}) {
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const id = (name: string) => 'am' + uid + name
  const classes = ['am', think && 'think', glow && 'glow', size < 26 && 'sm'].filter(Boolean).join(' ')
  return (
    <span className={classes} style={{ '--am': size + 'px' } as CSSProperties} aria-hidden="true">
      <svg viewBox="0 0 48 48" width={size} height={size} focusable="false">
        <defs>
          <radialGradient id={id('c')} cx="0.38" cy="0.32" r="0.75">
            <stop offset="0" stopColor="#f4fbff" />
            <stop offset="0.45" stopColor="#8fd8ff" />
            <stop offset="1" stopColor="#5a63ff" />
          </radialGradient>
          <linearGradient id={id('o')} x1="0" y1="0" x2="1" y2="1">
            <stop offset="0" stopColor="#7c8cff" />
            <stop offset="0.5" stopColor="#45e3a5" />
            <stop offset="1" stopColor="#ffb86b" />
          </linearGradient>
          <filter id={id('f')} x="-1" y="-1" width="3" height="3">
            <feGaussianBlur stdDeviation="3.2" />
          </filter>
        </defs>
        <circle className="am-glow" cx="24" cy="24" r="9" fill="#7c8cff" filter={'url(#' + id('f') + ')'} />
        <g className="am-orbit a">
          <ellipse
            cx="24"
            cy="24"
            rx="19"
            ry="7.5"
            fill="none"
            stroke={'url(#' + id('o') + ')'}
            strokeWidth="1.6"
            transform="rotate(-28 24 24)"
          />
          <circle cx="40.8" cy="15.2" r="2.1" fill="#45e3a5" />
        </g>
        <g className="am-orbit b">
          <ellipse
            cx="24"
            cy="24"
            rx="19"
            ry="7.5"
            fill="none"
            stroke={'url(#' + id('o') + ')'}
            strokeWidth="1.3"
            strokeOpacity="0.7"
            transform="rotate(32 24 24)"
          />
        </g>
        <circle className="am-core" cx="24" cy="24" r="7.2" fill={'url(#' + id('c') + ')'} />
        <circle cx="21.6" cy="21.4" r="2.1" fill="#ffffff" opacity="0.75" />
        <g className="am-sparks">
          <g transform="translate(9 9)">
            <path className="am-spark s1" d={SPARK} fill="#ffc94a" />
          </g>
          <g transform="translate(41 37)">
            <path className="am-spark s2" d={SPARK} fill="#4fdcff" />
          </g>
          <g transform="translate(6 36) scale(.8)">
            <path className="am-spark s3" d={SPARK} fill="#ff8fc8" />
          </g>
        </g>
      </svg>
    </span>
  )
}
