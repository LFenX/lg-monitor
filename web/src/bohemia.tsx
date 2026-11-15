import { useId } from 'react'
import './bohemia.css'
import { BUBBLE, BUBBLE_PAGES, CLOUD, CLOUD_PAGES, CORE_R } from './bohemia-paths'

/*
 * 波西米亚's mark as its lgmon 分身 wears it: the owner's logo (a cloud and a speech bubble holding open pages,
 * turning about a core) drawn in the page's ink with lgmon's terracotta for the bubble and the core. While it
 * thinks the two lobes turn and breathe apart; reduced motion keeps it still.
 */
export function BohemiaMark({ size = 24, think = false }: { size?: number; think?: boolean }) {
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  return (
    <span
      className={'bh' + (think ? ' think' : '') + (size < 26 ? ' sm' : '')}
      style={{ width: size, height: size }}
      aria-hidden="true"
    >
      <svg viewBox="0 0 240 240" width={size} height={size} focusable="false">
        <defs>
          <filter id={'bh' + uid} x="-1" y="-1" width="3" height="3">
            <feGaussianBlur stdDeviation="16" />
          </filter>
        </defs>
        <circle className="bh-glow" cx="120" cy="120" r="44" filter={'url(#bh' + uid + ')'} />
        <g className="bh-turn">
          <g className="bh-cloud">
            <path d={CLOUD} className="bh-ink" />
            <path d={CLOUD_PAGES} className="bh-ink-page" />
          </g>
          <g className="bh-bubble">
            <path d={BUBBLE} className="bh-tint" />
            <path d={BUBBLE_PAGES} className="bh-page" />
          </g>
        </g>
        <circle className="bh-core" cx="120" cy="120" r={CORE_R} />
      </svg>
    </span>
  )
}
