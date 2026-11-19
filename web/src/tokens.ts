/* A token the person chose to keep on this computer, used by every new window until they forget it. */
export const keptKey = 'lgmon-token-keep'

export function hasKeptToken() {
  try {
    return !!localStorage.getItem(keptKey)
  } catch {
    return false
  }
}

export function forgetKeptToken() {
  try {
    localStorage.removeItem(keptKey)
    sessionStorage.removeItem('lgmon-token')
  } catch {
    // Storage blocked: nothing was kept.
  }
  window.location.reload()
}
