const warning =
  '正在通过不加密的 HTTP 在局域网提供面板，同一网络里的人可能截获令牌。建议改为只监听 Tailscale 地址'

function safeHost(host: string): boolean {
  if (host === 'localhost' || host === '::1' || host.startsWith('fd7a:115c:a1e0:')) return true
  if (host.startsWith('::ffff:')) {
    const words = host.slice(7).split(':')
    if (words.length === 2) {
      const value = parseInt(words[0], 16) * 65536 + parseInt(words[1], 16)
      host = [value >>> 24, (value >>> 16) & 255, (value >>> 8) & 255, value & 255].join('.')
    }
  }
  const parts = host.split('.').map(Number)
  return (
    parts.length === 4 &&
    parts.every((part) => Number.isInteger(part) && part >= 0 && part <= 255) &&
    (parts[0] === 127 || (parts[0] === 100 && parts[1] >= 64 && parts[1] <= 127))
  )
}

export function HTTPWarning({ address = window.location.href }: { address?: string }) {
  const url = new URL(address),
    host = url.hostname
      .replace(/^\[|\]$/g, '')
      .replace(/\.$/, '')
      .toLowerCase()
  if (url.protocol !== 'http:' || safeHost(host)) return null
  return (
    <div className="notice notice-warning" role="alert">
      {warning}
    </div>
  )
}
