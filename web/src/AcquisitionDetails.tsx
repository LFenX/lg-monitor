/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import type { GitHubStats } from './types'

type Location = { channel: string; country: string; region: string }
export type Acquisition = {
  days: number
  downloads: (Location & { sent: number; linked: number })[]
  gates: (Location & { seen: number; pending: number; waitingDay: number })[]
}

export function AcquisitionDetails({ stats, data, lang }: { stats: GitHubStats | null; data: Acquisition | null; lang: string }) {
  const en = lang === 'en'
  const counts = stats?.assets
  const channels: Record<string, string> = {
    readme: 'README',
    guest: en ? 'Guest dashboard' : '访客看板',
    'web-update': en ? 'Web update' : '网页更新',
    'native-update': en ? 'Windows desktop update' : 'Windows 小窗口更新',
    'windows-update': en ? 'Windows update' : 'Windows 更新',
    'linux-update': en ? 'Linux CLI update' : 'Linux CLI 更新',
    'linux-install': en ? 'Linux install script' : 'Linux 一键安装',
    windows: 'Windows',
    linux: 'Linux',
    unknown: en ? 'Unattributed' : '来源未知',
  }
  const names = new Intl.DisplayNames([en ? 'en' : 'zh-CN'], { type: 'region' })
  const area = ({ country, region }: Location) =>
    [country && names.of(country), region].filter(Boolean).join(' · ') || (en ? 'Unknown' : '地区未知')
  const unavailable = en ? 'Data unavailable' : '数据暂不可用'
  const headings = (labels: string[]) => <thead><tr>{labels.map(label => <th key={label}>{label}</th>)}</tr></thead>
  const files = en
    ? ['Windows installer', 'Linux package', 'Linux script', 'Legacy Windows', 'Checksums', 'Installer metadata', 'Other']
    : ['Windows 主安装包', 'Linux 安装包', 'Linux 安装脚本', '旧版 Windows 包', '校验文件', '安装信息', '其他附件']
  const values = counts && [
    counts.installer,
    counts.linux || 0,
    counts.linuxScript || 0,
    counts.legacyInstaller,
    counts.checksums,
    counts.metadata,
    counts.other,
  ]
  return <>
    <details className="lm-acquisition">
      <summary>{en ? 'Downloads by file, channel and region' : '下载 · 展开文件、渠道与地区'}</summary>
      <h3>{en ? 'GitHub release files · lifetime' : 'GitHub 发布文件 · 累计'}</h3>
      {values ? <dl>{files.map((name, i) => <div key={name}><dt>{name}</dt><dd>{values[i].toLocaleString()}</dd></div>)}</dl> :
        <p>{unavailable}</p>}
      <h3>{en ? 'Installer responses from project links · last 30 days' : '项目入口发出的安装包 · 近 30 天'}</h3>
      <p>{en ? 'Channel and IP region are available for project links. Direct GitHub downloads have no region or activation attribution. These counts overlap with GitHub.' :
        '项目入口可记录下载渠道与出口 IP 所在地区。GitHub 直下及历史下载的渠道、地区和激活状态未知。两组次数有重叠。'}</p>
      {!data ? <p>{unavailable}</p> : data.downloads.length === 0 ? <p>{en ? 'No recorded installer responses yet.' : '尚无安装包发出记录。'}</p> :
        <div className="lm-growth-table-wrap"><table className="lm-growth-table">
          {headings(en ? ['Channel', 'Download region', 'Sent', 'Linked activation', 'No activation receipt'] :
            ['入口', '下载地区', '已发出', '已关联激活', '未收到激活回执'])}
          <tbody>{data.downloads.map(row => <tr key={JSON.stringify([row.channel, row.country, row.region])}>
            <td>{channels[row.channel] || row.channel}</td><td>{area(row)}</td><td>{row.sent}</td>
            <td>{row.channel.endsWith('-update') ? '—' : row.linked}</td>
            <td>{row.channel.endsWith('-update') ? '—' : row.sent - row.linked}</td></tr>)}</tbody>
        </table></div>}
      <p>{en ? 'A response can be cancelled. A missing receipt can also mean the installer was renamed or feedback was unavailable. Update requests are shown separately.' :
        '发出后可能取消下载。没有激活回执也可能是安装包被改名或反馈未送达。更新入口单列。'}</p>
    </details>
    <details className="lm-acquisition">
      <summary>{en ? 'Installed and reached the beta key screen' : '已安装 · 展开内测密钥页记录'}</summary>
      <p>{en ? 'Installs first seen on the local key screen in the last 30 days. Refreshes count once per installation. Observed activation removes an install from waiting.' :
        '统计近 30 天首次在本机打开内测密钥页的安装实例。刷新会去重，成功激活后移出待激活，管理员设备排除。'}</p>
      {!data ? <p>{unavailable}</p> : data.gates.length === 0 ? <p>{en ? 'No observed key-screen visits yet.' : '尚未收到密钥页到达记录。'}</p> :
        <div className="lm-growth-table-wrap"><table className="lm-growth-table">
          {headings(en ? ['Channel', 'Install region', 'Reached', 'Waiting', 'Waiting ≥24h'] :
            ['入口', '安装地区', '已到达', '待激活', '已等待 ≥24 小时'])}
          <tbody>{data.gates.map(row => <tr key={JSON.stringify([row.channel, row.country, row.region])}>
            <td>{channels[row.channel] || row.channel}</td><td>{area(row)}</td><td>{row.seen}</td>
            <td>{row.pending}</td><td>{row.waitingDay}</td></tr>)}</tbody>
        </table></div>}
      <p>{en ? 'Coverage starts with v0.16.0. Offline installs and browsers with tracking protection may be missing.' :
        '从 v0.16.0 开始观测；离线安装、启用请勿跟踪的浏览器可能没有记录。'}</p>
    </details>
  </>
}
