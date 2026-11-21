import { fireEvent, render, screen, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import {
  DependencyMap,
  boardScope,
  edgeText,
  labelWidth,
  layoutMap,
  mapEdges,
  neighbourhood,
  nodeHeight,
  nodeWidth,
  placeLabels,
  relationsText,
} from './DependencyMap'
import DocView, { headings, linkServices } from './DocView'
import type { Monitor } from './types'

vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ now: () => Date.now() }),
}))

function service(id: string, name: string, extra: Partial<Monitor> = {}): Monitor {
  return {
    id,
    name,
    watching: [],
    state: 'up',
    overall: 'up',
    hosts: [],
    rules: [],
    firing: 0,
    beats: [],
    uptime: 100,
    latencyMs: null,
    kind: 'port',
    ...extra,
  }
}

const site = service('site', '虚构官网', {
  role: 'web',
  depends: [
    { id: 'db', relation: 'data', note: '订单表' },
    { id: 'tunnel', relation: 'via' },
  ],
})
const tunnel = service('tunnel', '虚构隧道', {
  role: 'tunnel',
  dependents: [{ id: 'site', relation: 'via' }],
})
const db = service('db', '虚构订单库', {
  role: 'database',
  state: 'down',
  overall: 'down',
  dependents: [{ id: 'site', relation: 'data' }],
})
const gateway = service('gateway', '虚构网关', { parent: 'site', depends: [{ id: 'db', relation: 'calls' }] })
const all = [site, tunnel, db, gateway]

it('lays services out in layers without overlap and routes long edges around the ones between', () => {
  const ids = all.map((item) => item.id)
  const map = layoutMap(ids, mapEdges(all, new Set(ids)))
  const at = new Map(map.nodes.map((node) => [node.id, node]))
  // The site is on top; its part and its tunnel one layer down; the database below the gateway that calls it.
  expect(at.get('site')!.layer).toBe(0)
  expect(at.get('gateway')!.layer).toBe(1)
  expect(at.get('db')!.layer).toBe(2)
  for (const a of map.nodes)
    for (const b of map.nodes)
      if (a !== b)
        expect(
          a.x + nodeWidth <= b.x ||
            b.x + nodeWidth <= a.x ||
            a.y + nodeHeight <= b.y ||
            b.y + nodeHeight <= a.y,
        ).toBe(true)
  // site → db skips a layer, so it passes a waypoint: a curve, a straight piece, and another curve.
  const long = map.edges.find((edge) => edge.from === 'site' && edge.to === 'db')!
  expect(long.path.match(/C/g)).toHaveLength(2)
  expect(long.path).toContain(' L')
  expect(map.width).toBeGreaterThanOrEqual(2 * nodeWidth)
})

it('spreads the lines of a busy service and keeps every label off services and other labels', () => {
  // One gateway forwarding to five backends that all read one database: many lines start and end together.
  const backends = ['a', 'b', 'c', 'd', 'e']
  const monitors = [
    service('gw', '虚构网关', {
      role: 'gateway',
      depends: backends.map((id) => ({ id, relation: 'forwards' as const, note: '路径 /' + id })),
    }),
    ...backends.map((id) =>
      service(id, '后端 ' + id, {
        depends: [{ id: 'db', relation: 'reads' as const, optional: id === 'e' }],
      }),
    ),
    service('db', '虚构数据库', { role: 'database' }),
  ]
  const ids = new Set(monitors.map((item) => item.id))
  const map = layoutMap([...ids], mapEdges(monitors, ids))
  const starts = map.edges.filter((edge) => edge.from === 'gw').map((edge) => edge.pieces[0][0])
  expect(new Set(starts).size).toBe(5)
  const ends = map.edges.filter((edge) => edge.to === 'db').map((edge) => edge.pieces.at(-1)![6])
  expect(new Set(ends).size).toBe(5)
  expect(map.edges.map((edge) => edgeText(edge))).toContain('读取 · 可选')
  const wanted = map.edges.map((edge, index) => ({ edge: index, width: labelWidth(edgeText(edge)) }))
  const boxes = placeLabels(map, wanted).map(([x, y], index) => ({
    left: x - wanted[index].width / 2,
    right: x + wanted[index].width / 2,
    top: y - 10,
    bottom: y + 10,
  }))
  const apart = (
    a: (typeof boxes)[number],
    b: { left: number; right: number; top: number; bottom: number },
  ) => a.right <= b.left || b.right <= a.left || a.bottom <= b.top || b.bottom <= a.top
  for (const [index, box] of boxes.entries()) {
    for (const node of map.nodes)
      expect(
        apart(box, { left: node.x, right: node.x + nodeWidth, top: node.y, bottom: node.y + nodeHeight }),
      ).toBe(true)
    for (const other of boxes.slice(index + 1)) expect(apart(box, other)).toBe(true)
  }
})

it('folds plain sub-services into a chip on a board map and opens them on request', () => {
  const kids = ['k1', 'k2', 'k3'].map((id, index) =>
    service(id, '部件 ' + id, { parent: 'site', ...(index === 1 ? { state: 'down', overall: 'down' } : {}) }),
  )
  const monitors = [...all, ...kids]
  render(<DependencyMap monitors={monitors} ids={new Set(monitors.map((item) => item.id))} fold />)
  const map = screen.getByRole('figure', { name: '依赖关系图' })
  // The gateway has links of its own and k2 is failing, so both stay on the map; the healthy plain parts fold.
  expect(within(map).getByRole('link', { name: /虚构网关/ })).toBeVisible()
  expect(within(map).getByRole('link', { name: /部件 k2/ })).toHaveAttribute('data-state', 'down')
  expect(within(map).queryByRole('link', { name: /部件 k1/ })).not.toBeInTheDocument()
  const chip = within(map).getByRole('button', { name: '展开虚构官网的 2 个子服务' })
  expect(chip).toHaveTextContent('2 个子服务')
  fireEvent.click(chip)
  expect(within(map).getByRole('link', { name: /部件 k1/ })).toBeVisible()
  fireEvent.click(within(map).getByRole('button', { name: '收起虚构官网的 2 个子服务' }))
  expect(within(map).queryByRole('link', { name: /部件 k1/ })).not.toBeInTheDocument()
})

it('draws a cycle without looping forever', () => {
  const map = layoutMap(
    ['a', 'b'],
    [
      { from: 'a', to: 'b', relation: 'calls' },
      { from: 'b', to: 'a', relation: 'calls' },
    ],
  )
  expect(map.nodes).toHaveLength(2)
  expect(map.edges).toHaveLength(2)
})

it('picks the services around one service and a board with what it links to elsewhere', () => {
  expect([...neighbourhood(all, 'site')].sort()).toEqual(['db', 'gateway', 'site', 'tunnel'])
  expect([...neighbourhood(all, 'tunnel')].sort()).toEqual(['site', 'tunnel'])
  const scope = boardScope(all, ['site', 'gateway'])
  expect([...scope.all].sort()).toEqual(['db', 'gateway', 'site', 'tunnel'])
  expect([...scope.inside].sort()).toEqual(['gateway', 'site'])
})

it('shows the map with states, relation labels and a legend, outside services marked', () => {
  const scope = boardScope(all, ['site', 'gateway'])
  render(<DependencyMap monitors={all} ids={scope.all} inside={scope.inside} />)
  const map = screen.getByRole('figure', { name: '依赖关系图' })
  expect(within(map).getByRole('link', { name: /虚构订单库/ })).toHaveAttribute('data-outside')
  expect(within(map).getByRole('link', { name: /虚构订单库/ })).toHaveAttribute('data-state', 'down')
  expect(within(map).getByRole('link', { name: /虚构官网/ })).toHaveAttribute('href', '#/monitors/site')
  expect(map).toHaveTextContent('读写数据 · 订单表')
  expect(map).toHaveTextContent('经由')
  expect(within(map).getByText('子服务', { selector: '.dm-legend span' })).toBeVisible()
})

it('turns [[service]] into links outside code and lists the sections', () => {
  const text =
    '用到 [[db]] 和 [[tunnel|那条隧道]]。\n\n`[[not-a-link]]`\n\n```\n[[still-code]]\n```\n\n## 概览\n\n### 细节\n\n```bash\n## not a heading\n```\n'
  const linked = linkServices(text)
  expect(linked).toContain('[db](#/monitors/db)')
  expect(linked).toContain('[那条隧道](#/monitors/tunnel)')
  expect(linked).toContain('`[[not-a-link]]`')
  expect(linked).toContain('[[still-code]]')
  expect(headings(text).map((item) => item.level + item.text)).toEqual(['2概览', '3细节'])
})

it('renders a service document like product docs, with live service links and a dependency map', async () => {
  const doc =
    '# 虚构官网\n\n经由 [[tunnel]]，数据在 [[db|订单库]]，还有 [[ghost]]。\n\n## 依赖\n\n```deps\n```\n\n## 启动\n\n```powershell\nRestart-Service fake\n```\n\n> **注意** 重启会断开十秒。\n\n| 服务 | 关系 |\n|---|---|\n| [[db]] | 读写 |\n'
  render(<DocView markdown={doc} monitors={all} focus="site" />)
  const tunnelLink = screen.getAllByRole('link', { name: /虚构隧道/ })[0]
  expect(tunnelLink).toHaveAttribute('href', '#/monitors/tunnel')
  expect(screen.getAllByRole('link', { name: /订单库/ })[0]).toHaveAttribute('data-state', 'down')
  expect(screen.getByTitle('没有找到服务 ghost')).toBeVisible()
  expect(screen.getByRole('figure', { name: '依赖关系图' })).toBeVisible()
  expect(screen.getByRole('button', { name: '复制代码' })).toBeVisible()
  expect(screen.getByText('Restart-Service fake')).toBeVisible()
  expect(screen.getByRole('complementary')).toHaveAttribute('data-tone', 'warn')
  expect(screen.getByRole('table')).toBeVisible()
  const toc = screen.getByRole('navigation', { name: '本页内容' })
  expect(
    within(toc)
      .getAllByRole('button')
      .map((item) => item.textContent),
  ).toEqual(['依赖', '启动'])
})

it('writes the map out as Markdown and as plain text for an agent to read', () => {
  const ids = new Set(all.map((item) => item.id))
  const markdown = relationsText(all, ids, '虚构面板 的依赖关系', 'markdown')
  expect(markdown).toContain('# 虚构面板 的依赖关系')
  expect(markdown).toContain('| 虚构官网 | `site` | 网站 | 正常 |  |')
  expect(markdown).toContain('| 虚构官网 | 读写数据 | 虚构订单库 |  | 订单表 |')
  expect(markdown).toContain('| 虚构网关 | 调用接口 | 虚构订单库 |  |  |')
  expect(markdown).toContain('- 虚构官网：虚构网关')
  expect(markdown).toContain('```mermaid\ngraph LR')
  expect(markdown).toContain('site["虚构官网"] -->|经由| tunnel["虚构隧道"]')
  const text = relationsText(all, ids, '虚构面板 的依赖关系', 'text')
  expect(text).toContain('- 虚构官网 → 虚构订单库：读写数据，订单表')
  expect(text).toContain('- 虚构官网 包含 虚构网关')
  expect(text).not.toContain('|')
})

it('renders GitHub-style [!TIP] and [!WARNING] alerts as styled callouts without literal tags', () => {
  const doc = '> [!TIP]\n> 仅用于本地预览。\n\n> [!WARNING] 请先备份数据库。\n'
  render(<DocView markdown={doc} monitors={all} focus="site" />)
  const boxes = screen.getAllByRole('complementary')
  expect(boxes).toHaveLength(2)
  expect(boxes[0]).toHaveAttribute('data-tone', 'tip')
  expect(boxes[0]).toHaveTextContent('提示')
  expect(boxes[0]).toHaveTextContent('仅用于本地预览。')
  expect(boxes[0]).not.toHaveTextContent('[!TIP]')
  expect(boxes[1]).toHaveAttribute('data-tone', 'warn')
  expect(boxes[1]).toHaveTextContent('注意')
  expect(boxes[1]).toHaveTextContent('请先备份数据库。')
  expect(boxes[1]).not.toHaveTextContent('[!WARNING]')
})
