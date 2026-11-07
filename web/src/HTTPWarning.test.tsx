import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { HTTPWarning } from './HTTPWarning'

it.each([
  'http://192.168.1.10:7444/',
  'http://0.0.0.0:7444/',
  'http://100.63.255.255/',
  'http://100.128.0.0/',
  'http://[::]/',
  'http://[fd7a:115c:a1df::1]/',
  'http://[fd7a:115c:a1e1::1]/',
  'http://[::ffff:192.168.1.10]/',
  'http://monitor.example/',
  'http://localhost.example/',
])('warns before tokens are entered on %s', (address) => {
  render(<HTTPWarning address={address} />)
  expect(screen.getByRole('alert')).toHaveTextContent(
    '正在通过不加密的 HTTP 在局域网提供面板，同一网络里的人可能截获令牌。建议改为只监听 Tailscale 地址',
  )
})

it.each([
  'http://127.0.0.1:7444/',
  'http://127.23.45.67/',
  'http://LOCALHOST.:7444/',
  'http://[::1]/',
  'http://[0:0:0:0:0:0:0:1]/',
  'http://[::ffff:127.0.0.1]/',
  'http://100.64.0.0/',
  'http://100.127.255.255/',
  'http://[fd7a:115c:a1e0::1]/',
  'http://[FD7A:115C:A1E0:ffff:ffff:ffff:ffff:ffff]/',
  'http://[::ffff:100.126.79.86]/',
  'https://192.168.1.10/',
  'https://monitor.example/',
])('does not warn for loopback, Tailscale or HTTPS on %s', (address) => {
  render(<HTTPWarning address={address} />)
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})
