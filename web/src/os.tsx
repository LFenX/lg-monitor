/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

export type OSFamily = 'windows' | 'debian' | 'ubuntu' | 'alpine' | 'redhat' | 'arch' | 'linux' | 'macos' | 'other'

export type OSInfo = {
  family: OSFamily
  label: string
  short: string
}

export function formatOS(raw?: string | null): OSInfo | null {
  const trimmed = (raw || '').trim()
  if (!trimmed) return null
  const lower = trimmed.toLowerCase()

  if (lower === 'windows') return { family: 'windows', label: 'Windows', short: 'Windows' }
  if (lower === 'linux') return { family: 'linux', label: 'Linux', short: 'Linux' }
  if (lower === 'darwin' || lower === 'macos') return { family: 'macos', label: 'macOS', short: 'macOS' }

  let family: OSFamily = 'other'
  if (lower.startsWith('windows') || lower.includes('windows')) family = 'windows'
  else if (lower.includes('debian') || lower.includes('raspbian') || lower.includes('kali')) family = 'debian'
  else if (lower.includes('ubuntu')) family = 'ubuntu'
  else if (lower.includes('alpine')) family = 'alpine'
  else if (
    lower.includes('rocky') ||
    lower.includes('almalinux') ||
    lower.includes('centos') ||
    lower.includes('fedora') ||
    lower.includes('rhel') ||
    lower.includes('oracle')
  )
    family = 'redhat'
  else if (lower.includes('arch') || lower.includes('manjaro')) family = 'arch'
  else if (lower.includes('mac') || lower.includes('darwin')) family = 'macos'
  else if (
    lower.includes('linux') ||
    lower.includes('suse') ||
    lower.includes('nixos') ||
    lower.includes('deepin') ||
    lower.includes('euler') ||
    lower.includes('anolis') ||
    lower.includes('amazon') ||
    lower.includes('sles')
  )
    family = 'linux'

  // Compact mode keeps e.g. "Windows 11" while hover title preserves "Windows 11 24H2".
  const short =
    family === 'windows' ? trimmed.replace(/\s+\d{2}H\d$/i, '').trim() : trimmed

  return { family, label: trimmed, short }
}

function OSIcon({ family }: { family: OSFamily }) {
  switch (family) {
    case 'windows':
      return (
        <svg className="lm-os-icon" viewBox="0 0 16 16" width="11" height="11" aria-hidden="true">
          <path
            fill="currentColor"
            d="M1.5 2.7 7.3 1.9v5.6H1.5V2.7zm6.7-.9L14.5 1v6.5H8.2V1.8zM1.5 8.5h5.8v5.6l-5.8-.8V8.5zm6.7 0h6.3V15l-6.3-.9V8.5z"
          />
        </svg>
      )
    case 'debian':
      return (
        <svg className="lm-os-icon" viewBox="0 0 16 16" width="11" height="11" aria-hidden="true">
          <path
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            d="M9.6 13.7A5.8 5.8 0 1 1 13.4 7c0 2.5-1.9 4.2-4 4.2-1.7 0-2.9-1.1-2.9-2.6 0-1.2.9-2.1 2-2.1.9 0 1.5.6 1.5 1.4 0 .6-.4 1-.9 1"
          />
        </svg>
      )
    case 'ubuntu':
      return (
        <svg className="lm-os-icon" viewBox="0 0 16 16" width="11" height="11" aria-hidden="true">
          <circle cx="8" cy="8" r="4.4" fill="none" stroke="currentColor" strokeWidth="1.4" />
          <circle cx="3.1" cy="8" r="1.4" fill="currentColor" />
          <circle cx="10.6" cy="3.8" r="1.4" fill="currentColor" />
          <circle cx="10.6" cy="12.2" r="1.4" fill="currentColor" />
        </svg>
      )
    case 'macos':
      return (
        <svg className="lm-os-icon" viewBox="0 0 16 16" width="11" height="11" aria-hidden="true">
          <path
            fill="currentColor"
            d="M11.6 8.4c0-1.5 1.2-2.2 1.3-2.3-.7-1-1.8-1.2-2.2-1.2-.9-.1-1.8.6-2.3.6-.5 0-1.2-.5-2-.5-1 0-2 .6-2.5 1.5-1.1 1.9-.3 4.6.8 6.1.5.7 1.1 1.5 1.9 1.5.8 0 1.1-.5 2-.5s1.2.5 2 .5c.8 0 1.4-.7 1.9-1.5.6-.9.8-1.7.9-1.8-.1 0-1.8-.7-1.8-2.4zM10.1 3.7c.4-.5.7-1.2.6-1.9-.6 0-1.4.4-1.8.9-.4.4-.7 1.2-.6 1.9.7.1 1.4-.3 1.8-.9z"
          />
        </svg>
      )
    default:
      return (
        <svg className="lm-os-icon" viewBox="0 0 16 16" width="11" height="11" aria-hidden="true">
          <rect x="2" y="2.5" width="12" height="11" rx="2.2" fill="none" stroke="currentColor" strokeWidth="1.4" />
          <path
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M4.8 6.2 7 8 4.8 9.8M8.5 10h2.7"
          />
        </svg>
      )
  }
}

export function OSBadge({ os, compact = false }: { os?: string | null; compact?: boolean }) {
  const info = formatOS(os)
  if (!info) return null
  return (
    <span className="lm-os-tag" data-os={info.family} data-compact={compact || undefined} title={info.label}>
      <OSIcon family={info.family} />
      <span>{compact ? info.short : info.label}</span>
    </span>
  )
}
