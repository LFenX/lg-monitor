/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { useGate } from './access'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago } from './kit'
import { PageLoading } from './loading'
import { bytes } from './model'
import { OSBadge } from './os'
import { Dialog } from './parts'
import { useApp, useResource } from './state'
import { Dot } from './status'
import type {
  Alert,
  CloudDeviceSystem,
  Device,
  DeviceAlert,
  DeviceAlerts,
  DeviceHistory,
  DeviceSystem,
} from './types'
import './devices.css'

const Chart = lazy(() => import('./Chart').then((value) => ({ default: value.Chart })))

/*
 * Device monitoring: the computers themselves rather than the services on them. Numbers over time (CPU, memory,
 * swap, disks, what each program and container uses), and warnings when one is about to run out, such as memory
 * before the system starts killing processes.
 */

type Level = 'ok' | 'warn' | 'bad'
const level = (value: number, warn: number, bad: number): Level =>
  value >= bad ? 'bad' : value >= warn ? 'warn' : 'ok'
const pct = (value?: number | null) =>
  typeof value === 'number' && Number.isFinite(value)
    ? (value >= 10 ? value.toFixed(1) : value.toFixed(2)) + '%'
    : '—'
const rate = (value: number) => bytes(value) + '/s'

function useL() {
  const { lang } = useI18n()
  return (zh: string, en: string) => (lang === 'en' ? en : zh)
}

/* How close the machine is to running out of memory: memory itself, and swap that is filling up as it compensates. */
export function memoryRisk(system: DeviceSystem): Level {
  const memory = level(system.memoryPercent, 85, 92)
  const swapping = system.swapTotal ? level(system.swapPercent || 0, 60, 85) : 'ok'
  if (memory === 'bad' || (memory === 'warn' && swapping === 'bad')) return 'bad'
  if (memory === 'warn' || swapping === 'bad') return 'warn'
  return 'ok'
}

function Bar({ value, tone }: { value: number; tone?: Level }) {
  return (
    <span className="dv-bar" data-tone={tone}>
      <i style={{ width: Math.max(0, Math.min(100, value)) + '%' }} />
    </span>
  )
}

function Ring({ value, tone }: { value: number; tone: Level }) {
  const r = 34
  const c = 2 * Math.PI * r
  return (
    <svg className="dv-ring" data-tone={tone} viewBox="0 0 84 84" aria-hidden="true">
      <circle cx="42" cy="42" r={r} className="track" />
      <circle
        cx="42"
        cy="42"
        r={r}
        className="value"
        strokeDasharray={c}
        strokeDashoffset={c * (1 - Math.max(0, Math.min(100, value)) / 100)}
      />
    </svg>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="dv-row">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  )
}

function CPUCard({ system, processCount }: { system: DeviceSystem; processCount?: number }) {
  const L = useL()
  const [cores, setCores] = useState(false)
  const tone = level(system.cpuPercent, 75, 90)
  return (
    <article className="dv-card" data-kind="cpu">
      <h3>
        <i className="dv-key" data-kind="cpu" />
        {L('CPU 使用率', 'CPU')}
      </h3>
      <div className="dv-cpu">
        <span className="dv-ring-box">
          <Ring value={system.cpuPercent} tone={tone} />
          <b>{pct(system.cpuPercent)}</b>
        </span>
        <dl>
          {system.cpuCores ? <Row label={L('核心', 'Cores')} value={String(system.cpuCores)} /> : null}
          {system.load1 ? (
            <Row
              label={L('负载 1/5/15', 'Load 1/5/15')}
              value={[system.load1, system.load5 || 0, system.load15 || 0]
                .map((v) => v.toFixed(2))
                .join(' / ')}
            />
          ) : null}
          {system.load1 && system.cpuCores ? (
            <Row
              label={L('负载 / 核数', 'Load per core')}
              value={((system.load1 / system.cpuCores) * 100).toFixed(0) + '%'}
            />
          ) : null}
          {processCount ? <Row label={L('进程数', 'Processes')} value={String(processCount)} /> : null}
        </dl>
      </div>
      {system.corePercent && system.corePercent.length > 1 && (
        <>
          <button type="button" className="dv-more" aria-expanded={cores} onClick={() => setCores((v) => !v)}>
            {cores
              ? L('收起各核心', 'Hide cores')
              : L('各核心', 'Per core') + ' · ' + system.corePercent.length}
          </button>
          {cores ? (
            <ul className="dv-cores" aria-label={L('CPU 各核心', 'CPU cores')}>
              {system.corePercent.map((value, index) => (
                <li key={index}>
                  <span>#{index}</span>
                  <Bar value={value} tone={level(value, 75, 90)} />
                  <em>{value.toFixed(0)}%</em>
                </li>
              ))}
            </ul>
          ) : (
            <div className="dv-spark" aria-hidden="true">
              {system.corePercent.map((value, index) => (
                <i
                  key={index}
                  style={{ height: Math.max(6, value) + '%' }}
                  data-tone={level(value, 75, 90)}
                />
              ))}
            </div>
          )}
        </>
      )}
    </article>
  )
}

function MemoryCard({ system }: { system: DeviceSystem }) {
  const L = useL()
  const risk = memoryRisk(system)
  return (
    <article className="dv-card" data-kind="memory" data-risk={risk === 'ok' ? undefined : risk}>
      <h3>
        <i className="dv-key" data-kind="memory" />
        {L('内存', 'Memory')}
        {risk !== 'ok' && (
          <span className="dv-risk" data-tone={risk}>
            {risk === 'bad' ? L('即将耗尽', 'Nearly out') : L('偏高', 'High')}
          </span>
        )}
      </h3>
      <p className="dv-big" data-tone={level(system.memoryPercent, 85, 92)}>
        {pct(system.memoryPercent)}
      </p>
      <Bar value={system.memoryPercent} tone={level(system.memoryPercent, 85, 92)} />
      <dl>
        <Row
          label={L('已用 / 总量', 'Used / total')}
          value={bytes(system.memoryUsed) + ' / ' + bytes(system.memoryTotal)}
        />
        <Row label={L('可用', 'Available')} value={bytes(system.memoryAvailable)} />
        {system.memoryCached ? <Row label={L('缓存', 'Cached')} value={bytes(system.memoryCached)} /> : null}
      </dl>
      {system.swapTotal ? (
        <>
          <h4>
            <i className="dv-key" data-kind="swap" />
            {L('交换区', 'Swap')}
            <span>{pct(system.swapPercent)}</span>
          </h4>
          <Bar value={system.swapPercent || 0} tone={level(system.swapPercent || 0, 60, 85)} />
          <dl>
            <Row
              label={L('已用 / 总量', 'Used / total')}
              value={bytes(system.swapUsed) + ' / ' + bytes(system.swapTotal)}
            />
          </dl>
        </>
      ) : null}
    </article>
  )
}

function DiskCard({ system }: { system: DeviceSystem }) {
  const L = useL()
  const disks = [...(system.disks || [])].filter((disk) => disk.total > 0).slice(0, 6)
  return (
    <article className="dv-card" data-kind="disk">
      <h3>
        <i className="dv-key" data-kind="disk" />
        {L('磁盘', 'Disks')}
      </h3>
      {disks.length ? (
        <ul className="dv-disks">
          {disks.map((disk) => (
            <li key={disk.path}>
              <div>
                <b title={disk.path}>{disk.path}</b>
                <span>{pct(disk.usedPercent)}</span>
              </div>
              <Bar value={disk.usedPercent} tone={level(disk.usedPercent, 80, 90)} />
              <small>
                {L('剩余', 'Free')} {bytes(disk.free)} / {bytes(disk.total)}
              </small>
            </li>
          ))}
        </ul>
      ) : (
        <p className="lm-muted">{L('没有读到磁盘', 'No disks reported')}</p>
      )}
    </article>
  )
}

function SystemCard({ system }: { system: DeviceSystem }) {
  const L = useL()
  const networks = [...(system.networks || [])]
    .sort((a, b) => b.sendBytesPerSec + b.receiveBytesPerSec - (a.sendBytesPerSec + a.receiveBytesPerSec))
    .slice(0, 4)
  const days = Math.floor(system.uptimeSeconds / 86400)
  const hours = Math.floor((system.uptimeSeconds % 86400) / 3600)
  return (
    <article className="dv-card" data-kind="system">
      <h3>
        <i className="dv-key" data-kind="system" />
        {L('网络与运行', 'Network & uptime')}
      </h3>
      <dl>
        <Row
          label={L('已开机', 'Uptime')}
          value={days ? L(`${days} 天 ${hours} 小时`, `${days}d ${hours}h`) : L(`${hours} 小时`, `${hours}h`)}
        />
      </dl>
      <ul className="dv-nets">
        {networks.map((network) => (
          <li key={network.name}>
            <b title={network.name}>{network.name}</b>
            <span>↑ {rate(network.sendBytesPerSec)}</span>
            <span>↓ {rate(network.receiveBytesPerSec)}</span>
          </li>
        ))}
      </ul>
    </article>
  )
}

/* Where the memory goes: each program (its processes added up) and each container, largest first. */
function Usage({ device }: { device: Device }) {
  const L = useL()
  const usage = device.usage
  const total = device.system?.memoryTotal || 0
  const containers = usage?.containers || []
  const [tab, setTab] = useState<'programs' | 'containers'>('programs')
  useEffect(() => {
    if (!containers.length && tab === 'containers') setTab('programs')
  }, [containers.length, tab])
  if (!usage) return null
  const share = (value: number) => (total ? ((value / total) * 100).toFixed(1) + '%' : '—')
  return (
    <section className="dv-usage" aria-label={L('内存去向', 'Where the memory goes')}>
      <header>
        <h3>{L('内存去向', 'Where the memory goes')}</h3>
        <small>
          {L('约 30 秒采样 · ', 'Sampled about every 30 s · ')}
          {ago(usage.ts, Date.now())}
        </small>
        {containers.length > 0 && (
          <div className="lm-seg" role="group" aria-label={L('看程序或容器', 'Programs or containers')}>
            <button aria-pressed={tab === 'programs'} onClick={() => setTab('programs')}>
              {L('程序', 'Programs')}
            </button>
            <button aria-pressed={tab === 'containers'} onClick={() => setTab('containers')}>
              {L('容器', 'Containers')} · {containers.length}
            </button>
          </div>
        )}
      </header>
      {usage.containerError && <p className="lm-note">{usage.containerError}</p>}
      <table className="dv-table">
        <thead>
          <tr>
            <th>{tab === 'containers' ? L('容器', 'Container') : L('程序', 'Program')}</th>
            <th>{L('内存', 'Memory')}</th>
            <th>{L('占整机', 'Of total')}</th>
            <th>CPU</th>
            <th>{L('说明', 'Notes')}</th>
          </tr>
        </thead>
        <tbody>
          {tab === 'containers'
            ? containers.map((item) => (
                <tr key={item.id || item.name} data-tone={item.memoryPercent >= 90 ? 'bad' : undefined}>
                  <td>{item.name}</td>
                  <td>{bytes(item.memoryUsed)}</td>
                  <td>{share(item.memoryUsed)}</td>
                  <td>{pct(item.cpuPercent)}</td>
                  <td>
                    {item.memoryLimit
                      ? L(`占上限 ${pct(item.memoryPercent)} · `, `${pct(item.memoryPercent)} of limit · `)
                      : ''}
                    {'PIDs ' + item.pids}
                  </td>
                </tr>
              ))
            : usage.top.map((item) => (
                <tr key={item.name}>
                  <td>{item.name}</td>
                  <td>{bytes(item.rss)}</td>
                  <td>{share(item.rss)}</td>
                  <td>{pct(item.cpuPercent)}</td>
                  <td>
                    {item.count > 1
                      ? L(`${item.count} 个进程`, `${item.count} processes`)
                      : 'PID ' + (item.pid ?? '')}
                  </td>
                </tr>
              ))}
        </tbody>
      </table>
    </section>
  )
}

type Window = '1h' | '6h' | '24h' | '7d'
const windows: Record<Window, { ms: number; step: string }> = {
  '1h': { ms: 3600_000, step: 'raw' },
  '6h': { ms: 6 * 3600_000, step: '1m' },
  '24h': { ms: 24 * 3600_000, step: '1m' },
  '7d': { ms: 7 * 24 * 3600_000, step: '1h' },
}

/* A synced device's curves: the same ranges, read from what it sent rather than from this computer's store. */
function syncedSeries(history: DeviceHistory, span: Window) {
  const series = history.series.find((item) => item.span === span)
  if (!series) return { lists: [] as [number, number][][], from: 0, to: 0 }
  const lists = [series.cpu, series.mem, series.swap || []].map((values) =>
    values.flatMap((value, index) =>
      value >= 0 ? [[series.from + index * series.step, value] as [number, number]] : [],
    ),
  )
  return { lists, from: series.from, to: series.from + (series.cpu.length - 1) * series.step }
}

function History({ device, synced }: { device: Device; synced?: DeviceHistory }) {
  const L = useL()
  const { api } = useApp()
  const [span, setSpan] = useState<Window>('6h')
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), 60_000)
    return () => clearInterval(timer)
  }, [])
  const names = ['system.cpu_percent', 'system.memory_percent', 'system.swap_percent']
  const labels = [L('CPU', 'CPU'), L('内存', 'Memory'), L('交换区', 'Swap')]
  const history = useResource(
    async (signal) => {
      if (synced) {
        const value = syncedSeries(synced, span)
        return {
          from: value.from,
          to: value.to,
          lists: value.lists.map((points) => points.map(([ts, average]) => ({ ts, average, max: average }))),
        }
      }
      const to = Date.now()
      const from = to - windows[span].ms
      const lists = await Promise.all(
        names.map((name) =>
          api
            .request<{ metrics: { ts: number; average: number; max: number }[] }>(
              `/metrics?collector=${encodeURIComponent(device.id)}&name=${name}&from=${from}&to=${to}&step=${windows[span].step}`,
              { signal },
            )
            .then((value) => value.metrics || [])
            .catch(() => []),
        ),
      )
      return { from, to, lists }
    },
    [span, tick, synced],
    device.id + ':' + span + (synced ? ':synced' : ''),
  )
  const series = (history.value?.lists || [])
    .map((points, index) => ({
      name: labels[index],
      data: points.map((point) => [point.ts, Math.round(point.average * 10) / 10] as [number, number]),
    }))
    .filter((item) => item.data.length > 0)
  return (
    <section className="dv-history" aria-label={L('历史趋势', 'History')}>
      <header>
        <h3>{L('历史趋势', 'History')}</h3>
        <div className="lm-seg" role="group" aria-label={L('时间范围', 'Range')}>
          {(Object.keys(windows) as Window[]).map((key) => (
            <button key={key} aria-pressed={span === key} onClick={() => setSpan(key)}>
              {key}
            </button>
          ))}
        </div>
      </header>
      {series.length ? (
        <Suspense fallback={<PageLoading rows={2} />}>
          <Chart
            title={L('CPU、内存和交换区', 'CPU, memory and swap')}
            series={series}
            unit="%"
            period={history.value ? { from: history.value.from, to: history.value.to } : undefined}
          />
        </Suspense>
      ) : (
        <p className="lm-muted">
          {history.loading ? L('正在读取…', 'Loading…') : L('这段时间还没有数据', 'No data in this range')}
        </p>
      )}
    </section>
  )
}

function DeviceSection({ device, alerts, synced }: { device: Device; alerts: Alert[]; synced?: boolean }) {
  const L = useL()
  const system = device.system
  return (
    <section className="dv-device" id={'device-' + device.id} aria-label={device.hostname || device.id}>
      <header className="dv-device-head">
        <Glyph name="computer" size={18} />
        <h2>{device.hostname || device.id}</h2>
        <OSBadge os={device.os} />
        <span className="dv-live" data-online={device.online || undefined}>
          <Dot state={device.online ? 'up' : 'unknown'} />
          {device.online
            ? L('在线', 'Online')
            : L('离线 · ', 'Offline · ') + (device.lastSeen ? ago(device.lastSeen, Date.now()) : '')}
        </span>
        {alerts.map((alert) => (
          <a
            key={alert.id}
            className="dv-alert"
            data-severity={alert.severity}
            href={'#/alerts/' + encodeURIComponent(alert.id)}
          >
            <Glyph name="bell" size={13} />
            {alert.summary}
          </a>
        ))}
      </header>
      {system ? (
        <>
          <div className="dv-grid">
            <CPUCard system={system} processCount={device.usage?.processCount} />
            <MemoryCard system={system} />
            <DiskCard system={system} />
            <SystemCard system={system} />
          </div>
          <Usage device={device} />
          {synced ? (
            device.history && <History device={device} synced={device.history} />
          ) : (
            <History device={device} />
          )}
        </>
      ) : (
        <p className="lm-muted">
          {L('这台电脑还没有上报设备数据，升级采集器后会出现。', 'No device data from this computer yet.')}
        </p>
      )}
    </section>
  )
}

const presetOrder: {
  key: keyof Omit<DeviceAlerts, 'notify'>
  zh: string
  en: string
  hint: string
  hintEn: string
}[] = [
  {
    key: 'memory',
    zh: '内存即将耗尽（OOM 预警）',
    en: 'Memory nearly out (OOM warning)',
    hint: '内存使用率达到',
    hintEn: 'Memory at or above',
  },
  {
    key: 'containerMemory',
    zh: '容器内存接近上限',
    en: 'Container near its memory limit',
    hint: '任意容器占上限达到',
    hintEn: 'Any container at or above',
  },
  {
    key: 'swap',
    zh: '交换区快用完了',
    en: 'Swap filling up',
    hint: '交换区使用率达到',
    hintEn: 'Swap at or above',
  },
  {
    key: 'cpu',
    zh: 'CPU 持续繁忙',
    en: 'CPU busy for long',
    hint: 'CPU 使用率达到',
    hintEn: 'CPU at or above',
  },
  {
    key: 'disk',
    zh: '磁盘快满了',
    en: 'Disk filling up',
    hint: '任意磁盘使用率达到',
    hintEn: 'Any disk at or above',
  },
]
const holds = ['30s', '1m', '2m', '5m', '10m', '30m']

function AlertsDialog({ close }: { close: () => void }) {
  const L = useL()
  const { api, refresh } = useApp()
  const current = useResource((signal) => api.request<DeviceAlerts>('/devices/alerts', { signal }), [])
  const [draft, setDraft] = useState<DeviceAlerts>()
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    if (current.value && !draft) setDraft(current.value)
  }, [current.value, draft])
  const change = (key: keyof Omit<DeviceAlerts, 'notify'>, patch: Partial<DeviceAlert>) =>
    setDraft((value) => (value ? { ...value, [key]: { ...value[key], ...patch } } : value))
  const save = () => {
    if (!draft) return
    setSaving(true)
    setError('')
    api
      .request<DeviceAlerts>('/devices/alerts', { method: 'PUT', body: JSON.stringify(draft) })
      .then(() => {
        refresh()
        close()
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : L('没有保存', 'Not saved')))
      .finally(() => setSaving(false))
  }
  return (
    <Dialog title={L('设备预警', 'Device warnings')} close={close}>
      {!draft ? (
        <PageLoading rows={3} />
      ) : (
        <div className="dv-alerts">
          <p className="lm-muted">
            {L(
              '每台电脑分别判断，达到阈值并持续一段时间才告警，和服务告警一样发到通知渠道。',
              'Each computer is checked on its own; a warning goes out once the threshold has held for a while.',
            )}
            {draft.notify?.length
              ? L(' 发到：', ' Sent to: ') + draft.notify.join('、')
              : L(
                  ' 还没有通知渠道，保存时会启用本机 Windows 通知。',
                  ' No channel yet; a local notification is enabled on save.',
                )}
          </p>
          {presetOrder.map((preset) => {
            const item = draft[preset.key] || { threshold: 90, for: '2m' }
            return (
              <div key={preset.key} className="dv-alert-row" data-on={item.enabled || undefined}>
                <label className="dv-switch">
                  <input
                    type="checkbox"
                    checked={!!item.enabled}
                    onChange={(event) => change(preset.key, { enabled: event.target.checked })}
                  />
                  <b>{L(preset.zh, preset.en)}</b>
                </label>
                <span className="dv-alert-rule">
                  {L(preset.hint, preset.hintEn)}
                  <input
                    className="lm-input"
                    type="number"
                    min={1}
                    max={100}
                    value={item.threshold ?? ''}
                    aria-label={L(preset.zh, preset.en) + L(' 阈值', ' threshold')}
                    onChange={(event) => change(preset.key, { threshold: Number(event.target.value) })}
                  />
                  %{L('，持续', ' for')}
                  <select
                    className="lm-input"
                    value={item.for || '2m'}
                    aria-label={L(preset.zh, preset.en) + L(' 持续时间', ' duration')}
                    onChange={(event) => change(preset.key, { for: event.target.value })}
                  >
                    {holds.map((hold) => (
                      <option key={hold} value={hold}>
                        {hold}
                      </option>
                    ))}
                  </select>
                </span>
              </div>
            )
          })}
          {error && (
            <p className="lm-note" data-tone="down">
              {error}
            </p>
          )}
          <div className="dv-alert-actions">
            <button type="button" className="lm-btn" onClick={close}>
              {L('取消', 'Cancel')}
            </button>
            <button type="button" className="lm-btn primary" disabled={saving} onClick={save}>
              {saving ? L('正在保存…', 'Saving…') : L('保存', 'Save')}
            </button>
          </div>
        </div>
      )}
    </Dialog>
  )
}

export function DevicesPage({ focus, cloudDevice }: { focus?: string; cloudDevice?: string }) {
  const L = useL()
  const { api, data } = useApp()
  if (cloudDevice) return <SyncedDevicesPage key={cloudDevice} deviceId={cloudDevice} />
  return <LocalDevicesPage focus={focus} L={L} api={api} data={data} />
}

/* Another device of the account: its last synced sample, refreshed about every five minutes. History and
   warnings stay on that computer, so this page shows neither. */
function SyncedDevicesPage({ deviceId }: { deviceId: string }) {
  const L = useL()
  const { api } = useApp()
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), 30_000)
    return () => clearInterval(timer)
  }, [])
  const resource = useResource(
    (signal) =>
      api.request<CloudDeviceSystem>('/cloud/devices/' + encodeURIComponent(deviceId) + '/system', {
        signal,
      }),
    [tick],
    deviceId,
  )
  const value = resource.value
  const sampled = value?.devices.reduce((latest, device) => Math.max(latest, device.system?.ts || 0), 0) || 0
  return (
    <div className="lm-page wide dv-page">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">
            {L('设备', 'Devices')}
            {value ? ' · ' + value.deviceName : ''}
          </h1>
          <p className="lm-lead">
            {L(
              '同一账号的这台设备同步过来的采样和历史曲线（1 小时到 7 天），大约每 5 分钟更新。预警设置留在那台电脑上。',
              "This device's synced sample and curves (1 hour to 7 days), refreshed about every five minutes. Warnings stay on that computer.",
            )}
            {sampled ? ' ' + L('采样于 ', 'Sampled ') + ago(sampled, Date.now()) : ''}
          </p>
        </div>
      </header>
      {!value ? (
        resource.error ? (
          <p className="lm-note" data-tone="down">
            {resource.error}
          </p>
        ) : (
          <PageLoading label={L('正在读取设备…', 'Loading devices…')} />
        )
      ) : value.devices.length === 0 ? (
        <p className="lm-muted">
          {L(
            '这台设备还没有同步资源数据。它升级到新版本、并完成下一次同步后就会出现。',
            'This device has not synced its resources yet. They appear after it updates to the new version and syncs.',
          )}
        </p>
      ) : (
        value.devices.map((device) => (
          <DeviceSection
            key={device.id}
            device={{ ...device, online: value.online && device.online }}
            alerts={[]}
            synced
          />
        ))
      )}
    </div>
  )
}

function LocalDevicesPage({
  focus,
  L,
  api,
  data,
}: {
  focus?: string
  L: ReturnType<typeof useL>
  api: ReturnType<typeof useApp>['api']
  data: ReturnType<typeof useApp>['data']
}) {
  const gate = useGate()
  const [tick, setTick] = useState(0)
  const [editing, setEditing] = useState(false)
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), 5000)
    return () => clearInterval(timer)
  }, [])
  const resource = useResource((signal) => api.request<Device[]>('/devices', { signal }), [tick])
  const devices = useMemo(() => {
    const list = resource.value || []
    return focus ? list.filter((device) => device.id === focus) : list
  }, [resource.value, focus])
  const alerts = (data?.alerts || []).filter((alert) => alert.ruleId.startsWith('device-'))
  return (
    <div className="lm-page wide dv-page">
      <header className="lm-head">
        <div>
          <h1 className="lm-title">{L('设备', 'Devices')}</h1>
          <p className="lm-lead">
            {L(
              '电脑本身的数值：CPU、内存、交换区、磁盘、网络，以及每个程序和容器占了多少。和服务面板分开，不影响服务的正常或异常。',
              'The computers themselves: CPU, memory, swap, disks, network, and what each program and container uses. Kept apart from the service boards.',
            )}
          </p>
        </div>
        <div className="lm-actions">
          <button
            className="lm-btn"
            onClick={() => gate('config', L('设备预警', 'Device warnings'), () => setEditing(true))}
          >
            <Glyph name="bell" size={15} />
            {L('预警设置', 'Warnings')}
          </button>
        </div>
      </header>
      {devices.length > 1 && (
        <nav className="dv-jump" aria-label={L('电脑', 'Computers')}>
          {devices.map((device) => (
            <a key={device.id} href={'#/devices/' + encodeURIComponent(device.id)}>
              <Dot
                state={
                  device.system && memoryRisk(device.system) === 'bad'
                    ? 'down'
                    : device.online
                      ? 'up'
                      : 'unknown'
                }
              />
              {device.hostname || device.id}
            </a>
          ))}
        </nav>
      )}
      {!resource.value ? (
        resource.error ? (
          <p className="lm-note" data-tone="down">
            {resource.error}
          </p>
        ) : (
          <PageLoading label={L('正在读取设备…', 'Loading devices…')} />
        )
      ) : devices.length === 0 ? (
        <p className="lm-muted">{L('还没有采集器上报数据。', 'No collector has reported yet.')}</p>
      ) : (
        devices.map((device) => (
          <DeviceSection
            key={device.id}
            device={device}
            alerts={alerts.filter((alert) => alert.collectorId === device.id)}
          />
        ))
      )}
      {editing && <AlertsDialog close={() => setEditing(false)} />}
    </div>
  )
}
