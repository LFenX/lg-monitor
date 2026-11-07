import { once } from 'node:events'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

// The parent assigns this process to its private Windows Job before browsers may start.
process.stdin.resume()
await once(process.stdin, 'data')
const cli = fileURLToPath(new URL('../node_modules/@playwright/test/cli.js', import.meta.url))
const result = spawnSync(process.execPath, [cli, 'test'], {
  cwd: fileURLToPath(new URL('..', import.meta.url)),
  stdio: 'inherit',
  env: process.env,
})
process.stdin.destroy()
if (result.error) console.error('Browser test runner failed to start')
process.exitCode = result.status ?? 1
