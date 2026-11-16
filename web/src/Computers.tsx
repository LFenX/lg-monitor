import { Glyph } from './glyphs'
import { bytes, clockWarning, hostLink } from './model'
import { ServiceSwitchImport } from './ServiceSwitchImport'
import { useApp } from './state'
import { Dot } from './status'
import type { Collector } from './types'

const configLabels: Record<string, string> = {
  applied: '已应用',
  pending: '等待确认',
  rejected: '被采集器拒收',
  outdated: '采集器需要升级',
}

function cacheShort(host: Collector) {
  const hours = host.cache?.projectedHours
  return hours != null && hours < 24 ? hours : null
}

/* Problems with a computer itself, shown above every page because they make every number less trustworthy. */
export function HostWarnings() {
  const { data } = useApp()
  const notes: { key: string; text: string }[] = []
  for (const host of data?.collectors || []) {
    const name = host.hostname || host.id
    const clock = clockWarning(host.clockOffsetMs)
    if (clock) notes.push({ key: host.id + ':clock', text: name + '：' + clock })
    const hours = cacheShort(host)
    if (hours != null)
      notes.push({
        key: host.id + ':cache',
        text:
          name +
          '：按当前数据量，断网时只能缓存约 ' +
          hours.toFixed(1) +
          ' 小时。可以减少目标，或者调大 cache.maxSize。',
      })
  }
  if (!notes.length) return null
  return (
    <div className="notice notice-warning" role="status" aria-label="电脑提醒">
      {notes.map((note) => (
        <span key={note.key}>{note.text}</span>
      ))}
    </div>
  )
}

function Computer({ host }: { host: Collector }) {
  const { data } = useApp()
  const summary = data?.summaries.find((item) => item.collector_id === host.id)
  const cache = host.cache
  const short = cacheShort(host)
  const clock = clockWarning(host.clockOffsetMs)
  return (
    <article className="set-card set-host" aria-label={host.hostname || host.id}>
      <div className="set-row set-row-head">
        <Dot state={host.online ? 'up' : 'down'} />
        <h3>{host.hostname || host.id}</h3>
        <span className="set-chip" data-tone={host.online ? 'up' : 'down'}>
          {host.online ? '在线' : '离线'}
        </span>
      </div>
      <dl>
        <dt>CPU</dt>
        <dd>{summary ? summary.cpu.toFixed(1) + '%' : '等待系统指标'}</dd>
        <dt>内存</dt>
        <dd>
          {summary
            ? bytes(summary.memory_used) +
              ' / ' +
              bytes(summary.memory_total) +
              '（' +
              summary.memory_percent.toFixed(1) +
              '%）'
            : '等待系统指标'}
        </dd>
        {host.config && (
          <>
            <dt>监控配置</dt>
            <dd style={host.config.state === 'applied' ? undefined : { color: 'var(--pending-ink)' }}>
              <span>配置：{configLabels[host.config.state]}</span>
              {host.config.state !== 'applied' && host.config.message && <span>；{host.config.message}</span>}
            </dd>
          </>
        )}
        {cache && (
          <>
            <dt>离线缓存</dt>
            <dd className="vital-cache" style={{ display: 'grid', gap: 2 }}>
              <span>
                缓存用量：{(cache.bytes / 1000000).toFixed(1)} / {(cache.maxBytes / 1000000).toFixed(1)} MB
              </span>
              <span>
                写入速度：
                {cache.bytesPerHour === null
                  ? '估算中'
                  : (cache.bytesPerHour / 1000000).toFixed(1) + ' MB/小时'}
              </span>
              <span>
                预计可缓存：
                {cache.projectedHours === null
                  ? '估算中（需至少 5 分钟采样）'
                  : cache.projectedHours.toFixed(1) + ' 小时'}
              </span>
            </dd>
          </>
        )}
      </dl>
      {short != null && (
        <p className="lm-reason" data-state="unknown" role="note">
          按当前数据量，断网时只能缓存约 {short.toFixed(1)} 小时。可以减少目标，或者调大 cache.maxSize。
        </p>
      )}
      {clock && (
        <p className="lm-reason" data-state="unknown" role="note">
          {clock}
        </p>
      )}
      <footer className="set-actions">
        <a className="text-link" href={hostLink(host.id)}>
          资源、服务与启动项
          <Glyph name="chevron" size={14} />
        </a>
        <ServiceSwitchImport host={host} />
      </footer>
    </article>
  )
}

export function Computers() {
  const { data } = useApp()
  const hosts = data?.collectors || []
  if (!hosts.length)
    return (
      <p className="set-note" style={{ margin: 0 }}>
        还没有电脑连上来。监控器以 --role both 运行时会自带这台电脑；其他电脑运行 lgmon pair join 完成配对。
      </p>
    )
  return (
    <div style={{ display: 'grid', gap: 14 }}>
      {hosts.map((host) => (
        <Computer key={host.id} host={host} />
      ))}
    </div>
  )
}
