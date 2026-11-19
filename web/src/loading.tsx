import './loading.css'

/*
 * What a page shows while its data is on the way: the shape of what is coming, softly shimmering, with one quiet
 * line saying what is being read. People who only look in now and then see a page forming instead of a blank one.
 */
export function PageLoading({ label = '正在读取…', rows = 4 }: { label?: string; rows?: number }) {
  return (
    <div className="lm-loading" role="status" aria-live="polite" aria-label={label}>
      <div className="lm-loading-head">
        <i className="lm-skel" style={{ width: 44, height: 44, borderRadius: 14 }} />
        <span>
          <i className="lm-skel" style={{ width: 220, height: 22 }} />
          <i className="lm-skel" style={{ width: 140, height: 12 }} />
        </span>
      </div>
      <div className="lm-loading-card">
        {Array.from({ length: rows }, (_, index) => (
          <div className="lm-loading-row" key={index} style={{ animationDelay: index * 80 + 'ms' }}>
            <i className="lm-skel" style={{ width: 46, height: 22, borderRadius: 999 }} />
            <i className="lm-skel" style={{ width: 120 + ((index * 53) % 90), height: 14 }} />
            <i className="lm-skel lm-skel-beats" />
          </div>
        ))}
      </div>
      <p className="lm-loading-label">
        <span className="lm-loading-dot" aria-hidden="true" />
        {label}
      </p>
    </div>
  )
}
