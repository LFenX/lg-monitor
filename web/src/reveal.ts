import { useEffect } from 'react'
import { useApp } from './state'

/*
 * lgmon's window and tray reuse a browser tab that is already open: lgmon tells pages here to go to the page that
 * was asked for and to mark their title for a few seconds, and the window brings the marked browser window to
 * the front. Only when no page answers does a new one open.
 */

const digits = '0123456789abcdef'
const [joiner, zero, one] = [0x2060, 0x200b, 0x200c].map((code) => String.fromCharCode(code))

// Must match internal/desktop.RevealMarker: a word joiner, then each bit of the nonce as a zero-width space (0)
// or zero-width non-joiner (1). Browsers keep these in the window title, and nobody sees them.
export function revealMarker(nonce: string) {
  if (!/^[0-9a-f]{4}$/.test(nonce)) return ''
  let marker = joiner
  for (const digit of nonce) {
    const value = digits.indexOf(digit)
    for (let bit = 3; bit >= 0; bit--) marker += (value >> bit) & 1 ? one : zero
  }
  return marker
}

const loopback = (host: string) =>
  host === 'localhost' || host === '127.0.0.1' || host === '[::1]' || host === '::1'

/* Listens for a reveal while the page is open in a browser on this computer. */
export function useRevealWhenAsked() {
  const { subscribeLive } = useApp()
  useEffect(() => {
    if ((window as unknown as { lgmonLauncher?: boolean }).lgmonLauncher || !loopback(location.hostname))
      return
    let restore: ReturnType<typeof setTimeout> | undefined
    const stop = subscribeLive((update) => {
      if (update.event !== 'reveal') return
      const data = update.data as { hash?: unknown; nonce?: unknown }
      if (typeof data.hash === 'string' && data.hash.startsWith('#/') && data.hash !== location.hash)
        location.hash = data.hash
      const marker = typeof data.nonce === 'string' ? revealMarker(data.nonce) : ''
      if (!marker) return
      clearTimeout(restore)
      const title = document.title.replace(new RegExp(joiner + '[' + zero + one + ']*$'), '')
      document.title = title + marker
      restore = setTimeout(() => {
        if (document.title === title + marker) document.title = title
      }, 5000)
    })
    return () => {
      stop()
      clearTimeout(restore)
    }
  }, [subscribeLive])
}
