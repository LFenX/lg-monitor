/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

/*
 * Serves the real worker on 127.0.0.1 with node:sqlite standing in for D1, so the whole sign-in and
 * multi-device flow can be tried without a Cloudflare account. Email codes are printed here instead
 * of being mailed. Not for deployment.
 *
 *   node dev/server.ts [--port 8787] [--db path\to\sync.db]
 */
import { createServer } from 'node:http'
import { parseArgs } from 'node:util'
import { openLocalD1 } from './d1.ts'
import { handleRequest } from '../src/index.ts'

const { values } = parseArgs({
  options: { port: { type: 'string', default: '8787' }, db: { type: 'string', default: ':memory:' } },
})
const port = Number(values.port)
const db = openLocalD1(values.db)
const env = { DB: db as unknown as D1Database, DEV_MODE: 'true', APP_NAME: 'lgmon' }

const server = createServer(async (req, res) => {
  try {
    const chunks: Buffer[] = []
    for await (const chunk of req) chunks.push(chunk as Buffer)
    const headers = new Headers()
    for (const [name, value] of Object.entries(req.headers)) {
      if (Array.isArray(value)) value.forEach((v) => headers.append(name, v))
      else if (value !== undefined) headers.set(name, value)
    }
    const method = req.method || 'GET'
    const request = new Request(`http://127.0.0.1:${port}${req.url || '/'}`, {
      method,
      headers,
      body: method === 'GET' || method === 'HEAD' ? undefined : Buffer.concat(chunks),
    })
    const response = await handleRequest(request, env)
    res.writeHead(response.status, Object.fromEntries(response.headers))
    res.end(Buffer.from(await response.arrayBuffer()))
    console.log(`${method} ${req.url} -> ${response.status}`)
  } catch (err) {
    console.error('request failed', err)
    res.writeHead(500, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ error: { code: 'dev_server', message: String(err) } }))
  }
})

server.listen(port, '127.0.0.1', () => {
  console.log(`lgmon sync dev server on http://127.0.0.1:${port} (db: ${values.db})`)
})
