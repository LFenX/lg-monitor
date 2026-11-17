import { useState } from 'react'
import { Glyph } from './glyphs'
import { durationText } from './Incidents'
import { ago } from './kit'
import { Dot, healthText } from './status'
import { useApp, useResource } from './state'
import type {
  Health,
  JobAttempt,
  JobBrief,
  JobDay,
  JobDayDetail,
  JobDays,
  JobDependency,
  JobInfo,
  JobPartition,
  JobStageView,
  JobStatus,
  Monitor,
} from './types'
import './job.css'

export const jobStatusName: Record<JobStatus, string> = {
  pending: '待运行',
  running: '运行中',
  success: '成功',
  partial: '部分失败',
  failed: '失败',
  missed: '漏跑',
  unknown: '结果未知',
  disabled: '未启用',
}

/* Colour for a day or a run: only business evidence of success is green. */
export function jobTone(status: JobStatus): string {
  if (status === 'success') return 'up'
  if (status === 'failed' || status === 'partial' || status === 'missed') return 'down'
  if (status === 'running') return 'running'
  if (status === 'disabled') return 'off'
  if (status === 'pending') return 'waiting'
  return 'unknown'
}

const stageStateName: Record<JobStageView['state'], string> = {
  pending: '没有上报',
  running: '进行中',
  success: '成功',
  failed: '失败',
  unknown: '结果未知',
  skipped: '跳过',
}

const verifyName: Record<string, string> = { ok: '通过', failed: '不通过', unknown: '未知' }
const uploadName: Record<string, string> = { ok: '成功', failed: '失败', unknown: '未知' }

const weekday = ['日', '一', '二', '三', '四', '五', '六']

export function JobStatusBadge({ status }: { status: JobStatus }) {
  return (
    <span className="lm-badge lm-job-badge" data-state={jobTone(status)}>
      {jobStatusName[status]}
    </span>
  )
}

/* The board row's line for a job: what its latest day with news says. */
export function JobBriefText({ brief }: { brief?: JobBrief }) {
  if (!brief) return <span className="wait">还没有任何运行记录</span>
  const text = shortDate(brief.date) + ' ' + jobStatusName[brief.status] + ' · ' + brief.summary
  const tone = jobTone(brief.status)
  return <span className={tone === 'down' ? 'issue' : tone === 'up' ? '' : 'wait'}>{text}</span>
}

function shortDate(date: string) {
  const [, month, day] = date.split('-')
  return Number(month) + '月' + Number(day) + '日'
}

function dateLabel(date: string) {
  const [year, month, day] = date.split('-').map(Number)
  return shortDate(date) + ' 周' + weekday[new Date(Date.UTC(year, month - 1, day)).getUTCDay()]
}

/* Business dates and stage times are shown in Beijing time, the calendar the job is judged by. */
const beijing = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

function timeText(ts?: number) {
  return ts ? beijing.format(ts) : '—'
}

function count(value?: number) {
  return value == null ? '—' : value.toLocaleString('zh-CN')
}

function bytes(value?: number) {
  if (value == null) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = value,
    unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit++
  }
  return (unit ? size.toFixed(size >= 100 ? 0 : 1) : String(size)) + ' ' + units[unit]
}

function todayInBeijing(now: number) {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai' }).format(now)
}

function SourceLine({ job, now }: { job: JobInfo; now: number }) {
  if (job.source !== 'file')
    return (
      <p className="lm-job-source">
        <Glyph name="plug" size={14} />
        <span>
          任务自己调用上报接口；上报密钥在监控器电脑上用 <code>lgmon job key {job.id}</code> 生成
        </span>
      </p>
    )
  const file = job.file
  return (
    <div className="lm-job-source" data-state={file?.state === 'error' ? 'down' : undefined}>
      <Glyph name="computer" size={14} />
      <span>
        采集器 <strong>{file?.collectorId || job.collector}</strong> 读取{' '}
        <code>{file?.path || job.path}</code>
        {!file
          ? '，还没有读过（采集器连上并收到配置后开始）'
          : file.state === 'error'
            ? '，现在读不了'
            : file.lastEventAt
              ? '，' + ago(file.lastEventAt, now) + '收到新事件'
              : '，还没有读到事件'}
      </span>
      {file?.problem && (
        <small>
          {file.state === 'error' ? '' : '最近一次问题：'}
          {file.problem}
          {file.problemAt ? '（' + ago(file.problemAt, now) + '）' : ''}
        </small>
      )}
    </div>
  )
}

function Production({ items }: { items: JobDependency[] }) {
  if (!items.length) return <p className="lm-muted lm-small">没有设置生产通路依赖（production）。</p>
  return (
    <ul className="lm-job-deps">
      {items.map((item) => (
        <li key={item.id}>
          <Dot state={item.state} />
          <a href={'#/monitors/' + encodeURIComponent(item.id)}>{item.name || item.id}</a>
          <span className="lm-muted">{healthText[item.state] || item.state}</span>
          {item.issue && <small>{item.issue}</small>}
        </li>
      ))}
    </ul>
  )
}

/* Two different questions side by side: is the production path healthy now, and did this run get its data. */
function DependencyVersusFetch({
  production,
  attempt,
}: {
  production: JobDependency[]
  attempt?: JobAttempt
}) {
  const fetch = attempt?.stages.find((stage) => stage.id === 'fetch')
  const down = production.filter((item) => item.state !== 'up')
  const now: Health = !production.length ? 'unknown' : down.length ? 'down' : 'up'
  const fetchTone =
    !fetch || fetch.state === 'pending'
      ? 'unknown'
      : fetch.state === 'success'
        ? 'up'
        : fetch.state === 'failed'
          ? 'down'
          : 'unknown'
  return (
    <div className="lm-job-compare">
      <div data-state={now}>
        <small>生产通路现在</small>
        <strong>
          {!production.length ? '没有设置' : down.length ? down.length + ' 个不正常' : '全部正常'}
        </strong>
        {down.map((item) => (
          <span key={item.id}>
            {item.name}：{item.issue || healthText[item.state]}
          </span>
        ))}
      </div>
      <div data-state={fetchTone}>
        <small>本次生产取数</small>
        <strong>{fetch ? stageStateName[fetch.state] : '这次运行没有取数阶段'}</strong>
        {fetch?.error && <span>{fetch.error}</span>}
      </div>
    </div>
  )
}

function StageTimeline({ stages, now }: { stages: JobStageView[]; now: number }) {
  return (
    <ol className="lm-job-stages">
      {stages.map((stage) => {
        const end = stage.finishedAt || (stage.state === 'running' ? now : 0)
        return (
          <li key={stage.id} data-state={stage.state}>
            <i aria-hidden="true" />
            <div>
              <strong>{stage.name}</strong>
              <span className="lm-job-stage-state">{stageStateName[stage.state]}</span>
              {stage.needsReceipt && stage.state === 'success' && !stage.receipt && (
                <span className="lm-job-warn">没有回执，不算成功</span>
              )}
            </div>
            <small>
              {stage.startedAt || stage.finishedAt
                ? timeText(stage.startedAt) + ' – ' + timeText(stage.finishedAt)
                : '没有时间'}
              {stage.startedAt && end ? ' · ' + durationText(end - stage.startedAt) : ''}
            </small>
            {stage.state === 'running' && stage.progress != null && (
              <span
                className="lm-job-progress"
                role="progressbar"
                aria-valuenow={stage.progress}
                aria-valuemin={0}
                aria-valuemax={100}
              >
                <span style={{ width: stage.progress + '%' }} />
              </span>
            )}
            {stage.error && <p className="lm-job-error">{stage.error}</p>}
            {stage.receipt && (
              <p className="lm-job-receipt">
                回执 <code>{stage.receipt}</code>
              </p>
            )}
          </li>
        )
      })}
    </ol>
  )
}

function PartitionTable({ rows }: { rows: JobPartition[] }) {
  if (!rows.length) return <p className="lm-muted lm-small">这次运行没有上报小时分区数据。</p>
  return (
    <div className="lm-job-table" role="region" aria-label="小时分区数据" tabIndex={0}>
      <table>
        <thead>
          <tr>
            <th>区域</th>
            <th>小时分区</th>
            <th>独立收件数</th>
            <th>独立发件数</th>
            <th>收件数据条数</th>
            <th>发件数据条数</th>
            <th>gz 文件数</th>
            <th>压缩大小</th>
            <th>目标目录</th>
            <th>校验</th>
            <th>上传</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.region + row.hour}>
              <td>{row.regionName}</td>
              <td className="nowrap">{row.hourLabel}</td>
              <td className="num">{count(row.uniqueRecipients)}</td>
              <td className="num">{count(row.uniqueSenders)}</td>
              <td className="num">{count(row.receivedRows)}</td>
              <td className="num">{count(row.sentRows)}</td>
              <td className="num">{count(row.gzFiles)}</td>
              <td className="num">{bytes(row.compressedBytes)}</td>
              <td className="dir" title={row.targetDir}>
                {row.targetDir || '—'}
              </td>
              <td data-outcome={row.verify}>{row.verify ? verifyName[row.verify] : '—'}</td>
              <td data-outcome={row.upload} title={row.receipt ? '回执 ' + row.receipt : undefined}>
                {row.upload ? uploadName[row.upload] : '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function AttemptView({
  attempt,
  index,
  total,
  now,
}: {
  attempt: JobAttempt
  index: number
  total: number
  now: number
}) {
  const end = attempt.finishedAt || (attempt.status === 'running' ? now : attempt.lastEventAt)
  return (
    <article className="lm-job-attempt" data-state={jobTone(attempt.status)}>
      <header>
        <strong>
          第 {total - index} 次 · {attempt.trigger === 'manual' ? '手动补跑' : '计划运行'}
        </strong>
        <JobStatusBadge status={attempt.status} />
        <small>
          {timeText(attempt.startedAt)} 开始
          {attempt.finishedAt ? '，' + timeText(attempt.finishedAt) + ' 结束' : ''}
          {end > attempt.startedAt ? ' · ' + durationText(end - attempt.startedAt) : ''} · {attempt.events}{' '}
          条事件
        </small>
      </header>
      <p className="lm-job-summary">{attempt.summary}</p>
      <h4>阶段</h4>
      <StageTimeline stages={attempt.stages} now={now} />
      <h4>小时分区</h4>
      <PartitionTable rows={attempt.partitions} />
      <p className="lm-muted lm-small">运行 ID {attempt.id}</p>
    </article>
  )
}

function DayRow({
  day,
  open,
  toggle,
  production,
  now,
}: {
  day: JobDay
  open: boolean
  toggle: () => void
  production: JobDependency[]
  now: number
}) {
  return (
    <li
      className="lm-job-day"
      data-state={jobTone(day.status)}
      data-open={open || undefined}
      id={'job-day-' + day.date}
    >
      <button
        className="lm-job-day-head"
        aria-expanded={open}
        onClick={toggle}
        disabled={!day.attempts.length && !day.enabled}
      >
        <Glyph name="chevron" size={14} />
        <span className="lm-job-date">{dateLabel(day.date)}</span>
        <JobStatusBadge status={day.status} />
        <span className="lm-job-day-summary">{day.summary}</span>
        {day.attempts.length > 1 && <small>{day.attempts.length} 次运行</small>}
      </button>
      {open && (
        <div className="lm-job-day-body">
          {day.attempts.length ? (
            <>
              <DependencyVersusFetch production={production} attempt={day.attempts[0]} />
              {day.attempts.map((attempt, index) => (
                <AttemptView
                  key={attempt.id}
                  attempt={attempt}
                  index={index}
                  total={day.attempts.length}
                  now={now}
                />
              ))}
            </>
          ) : (
            <p className="lm-muted lm-small">
              {day.expected ? '计划北京时间 ' + timeText(day.expected).slice(0, 5) + ' 开始。' : ''}
              这一天还没有收到任何运行的事件。
            </p>
          )}
        </div>
      )}
    </li>
  )
}

/* Days the job is not meant to run say nothing new one by one, so a stretch of them becomes one line. */
function quietRuns(days: JobDay[]): (JobDay | { off: JobDay[] })[] {
  const result: (JobDay | { off: JobDay[] })[] = []
  let run: JobDay[] = []
  const flush = () => {
    if (run.length >= 3) result.push({ off: run })
    else result.push(...run)
    run = []
  }
  for (const day of days) {
    if (day.status === 'disabled' && !day.attempts.length) run.push(day)
    else {
      flush()
      result.push(day)
    }
  }
  flush()
  return result
}

/* A batch job's page: one record per business day, each opening onto its runs, stages and hourly data. */
export function JobSection({ monitor }: { monitor: Monitor }) {
  const { api, pulse, now } = useApp()
  const current = now()
  const id = encodeURIComponent(monitor.id)
  const days = useResource(
    (signal) => api.request<JobDays>('/jobs/' + id + '/days?days=30', { signal }),
    [pulse],
    monitor.id,
  )
  const today = todayInBeijing(current)
  const detail = useResource(
    (signal) => api.request<JobDayDetail>('/jobs/' + id + '/days/' + today, { signal }),
    [pulse, today],
    monitor.id,
  )
  const [opened, setOpened] = useState<Record<string, boolean>>({})
  const [all, setAll] = useState(false)
  const value = days.value
  if (!value)
    return (
      <section className="lm-section lm-job-section" aria-label="每日记录">
        {days.error ? (
          <p className="lm-reason" data-state="down">
            {days.error}
          </p>
        ) : (
          <p className="lm-muted">正在读取每日记录…</p>
        )}
      </section>
    )
  const production = detail.value?.production || []
  const job = detail.value?.job || value.job
  // The latest day with news opens by itself; quiet or disabled days stay closed.
  const first = value.days.find((day) => day.attempts.length > 0)
  const isOpen = (day: JobDay) => opened[day.date] ?? day === first
  const shown = all ? value.days : value.days.slice(0, 14)
  return (
    <>
      <section className="lm-section lm-job-section" aria-label="每日记录">
        <header>
          <h2 className="lm-h2">每日记录</h2>
          <small>{job.schedule}；业务日期按北京时间，一天一条</small>
        </header>
        <div className="lm-job-strip" role="list" aria-label="最近 30 天">
          {[...value.days].reverse().map((day) => (
            <a
              key={day.date}
              role="listitem"
              href={'#job-day-' + day.date}
              data-state={jobTone(day.status)}
              title={dateLabel(day.date) + ' ' + jobStatusName[day.status] + ' · ' + day.summary}
              aria-label={dateLabel(day.date) + ' ' + jobStatusName[day.status]}
              onClick={(event) => {
                event.preventDefault()
                setAll(all || value.days.indexOf(day) >= 14)
                setOpened((state) => ({ ...state, [day.date]: true }))
                requestAnimationFrame(() =>
                  document.getElementById('job-day-' + day.date)?.scrollIntoView({ block: 'nearest' }),
                )
              }}
            />
          ))}
        </div>
        <div className="lm-axis">
          <span>{value.days.length ? shortDate(value.days[value.days.length - 1].date) : ''}</span>
          <span>今天</span>
        </div>
        <SourceLine job={job} now={current} />
        <ul className="lm-job-days">
          {quietRuns(shown).map((item) =>
            'off' in item ? (
              <li key={item.off[0].date} className="lm-job-day lm-job-off" data-state="off">
                <span>
                  {shortDate(item.off[item.off.length - 1].date)} – {shortDate(item.off[0].date)}
                </span>
                <span className="lm-muted">未启用，共 {item.off.length} 天（不算漏跑）</span>
              </li>
            ) : (
              <DayRow
                key={item.date}
                day={item}
                open={isOpen(item)}
                toggle={() => setOpened((state) => ({ ...state, [item.date]: !isOpen(item) }))}
                production={production}
                now={current}
              />
            ),
          )}
        </ul>
        {!all && value.days.length > 14 && (
          <button className="lm-btn small" onClick={() => setAll(true)}>
            显示 30 天
          </button>
        )}
      </section>
      <section className="lm-section lm-job-section" aria-label="生产通路">
        <header>
          <h2 className="lm-h2">生产通路</h2>
          <small>这些服务现在是否正常；每次运行的取数结果在当天记录里</small>
        </header>
        <Production items={production} />
      </section>
    </>
  )
}
