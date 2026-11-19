import { useMonitorHref } from './shared'
import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { RelationLine, RoleIcon, headPath, relations, roleOf, roles } from './roles'
import { Glyph } from './glyphs'
import { useCopy } from './kit'
import type { Monitor, Relation } from './types'
import './dependency-map.css'

/*
 * Who uses whom, drawn top-down: the services people reach first at the top, what they rest on (tunnels,
 * gateways, databases) below. Sub-services hang from their service on a thin grey line; dependencies are
 * coloured arrows with one line and arrowhead per relation. Hovering a service or a line lights up what it touches.
 */

export type MapEdge = {
  from: string
  to: string
  relation: Relation | 'part'
  note?: string
  optional?: boolean
}
export type MapNode = { id: string; x: number; y: number; layer: number }
/* One cubic piece of an edge: start, two controls, end. A straight piece has its controls on its ends. */
export type Piece = [number, number, number, number, number, number, number, number]
export type MapLayout = {
  nodes: MapNode[]
  edges: (MapEdge & { path: string; pieces: Piece[]; mid: [number, number] })[]
  width: number
  height: number
}

export const nodeWidth = 196
export const nodeHeight = 56
const gapX = 28
const waypointWidth = 18
// Room between layers for the labels that sit on the lines there.
const gapY = 96
const pad = 24
// Lines leaving or entering one side of a service spread out, so they and their labels do not start in one point.
const portStep = 26
const portMargin = 26

/* Where the line of the i-th of n edges on one side of a service meets it, relative to the side's centre. */
function spread(count: number, index: number) {
  if (count < 2) return 0
  const room = Math.min(nodeWidth - portMargin * 2, (count - 1) * portStep)
  return -room / 2 + (room * index) / (count - 1)
}

function pathOf(pieces: Piece[]) {
  let path = `M${pieces[0][0]} ${pieces[0][1]}`
  for (const [x0, y0, a, b, c, d, x1, y1] of pieces) {
    path +=
      a === x0 && b === y0 && c === x1 && d === y1 ? ` L${x1} ${y1}` : ` C${a} ${b} ${c} ${d} ${x1} ${y1}`
  }
  return path
}

function cubic(piece: Piece, t: number): [number, number] {
  const [x0, y0, a, b, c, d, x1, y1] = piece
  const s = 1 - t
  return [
    s * s * s * x0 + 3 * s * s * t * a + 3 * s * t * t * c + t * t * t * x1,
    s * s * s * y0 + 3 * s * s * t * b + 3 * s * t * t * d + t * t * t * y1,
  ]
}

/* The point a fraction t of the way along an edge, measured by an approximate length of each piece. */
export function pointAt(pieces: Piece[], t: number): [number, number] {
  const lengths = pieces.map(([x0, y0, a, b, c, d, x1, y1]) => {
    const chord = Math.hypot(x1 - x0, y1 - y0)
    const polygon = Math.hypot(a - x0, b - y0) + Math.hypot(c - a, d - b) + Math.hypot(x1 - c, y1 - d)
    return (chord + polygon) / 2
  })
  const total = lengths.reduce((sum, value) => sum + value, 0) || 1
  let left = Math.max(0, Math.min(1, t)) * total
  for (let index = 0; index < pieces.length; index++) {
    if (left <= lengths[index] || index === pieces.length - 1) {
      return cubic(pieces[index], lengths[index] ? Math.min(1, left / lengths[index]) : 0)
    }
    left -= lengths[index]
  }
  return [pieces[0][0], pieces[0][1]]
}

/*
 * The usual layered drawing: longest-path layers, a waypoint in every layer a long edge crosses (so it bends
 * around the services in between instead of running behind them), then a few barycenter sweeps to cut crossings.
 */
export function layoutMap(ids: string[], edges: MapEdge[]): MapLayout {
  const known = new Set(ids)
  const usable = edges.filter((edge) => known.has(edge.from) && known.has(edge.to) && edge.from !== edge.to)
  const out = new Map<string, string[]>(ids.map((id) => [id, []]))
  for (const edge of usable) out.get(edge.from)!.push(edge.to)
  // A cycle (A uses B, B uses A) is drawn, but one of its edges does not push layers down.
  const back = new Set<string>()
  const state = new Map<string, number>()
  const visit = (id: string) => {
    state.set(id, 1)
    for (const next of out.get(id)!) {
      if (state.get(next) === 1) back.add(id + '>' + next)
      else if (!state.get(next)) visit(next)
    }
    state.set(id, 2)
  }
  ids.forEach((id) => state.get(id) || visit(id))
  const forward = usable.filter((edge) => !back.has(edge.from + '>' + edge.to))
  const layer = new Map<string, number>(ids.map((id) => [id, 0]))
  for (let round = 0; round < ids.length; round++) {
    let moved = false
    for (const edge of forward) {
      const want = layer.get(edge.from)! + 1
      if (layer.get(edge.to)! < want) {
        layer.set(edge.to, want)
        moved = true
      }
    }
    if (!moved) break
  }
  const depth = Math.max(0, ...layer.values())
  const rows: string[][] = Array.from({ length: depth + 1 }, () => [])
  ids.forEach((id) => rows[layer.get(id)!].push(id))
  // Waypoints: "a>b#2" sits in layer 2 on the way from a down to b. Two edges between the same pair share them.
  const links: [string, string][] = []
  const waypoints = new Map<string, string[]>()
  for (const edge of forward) {
    const key = edge.from + '>' + edge.to
    if (waypoints.has(key)) continue
    const from = layer.get(edge.from)!
    const to = layer.get(edge.to)!
    let previous = edge.from
    const chain: string[] = []
    for (let at = from + 1; at < to; at++) {
      const id = key + '#' + at
      rows[at].push(id)
      chain.push(id)
      links.push([previous, id])
      previous = id
    }
    links.push([previous, edge.to])
    waypoints.set(key, chain)
  }
  const isWaypoint = (id: string) => id.includes('#')
  const above = new Map<string, string[]>()
  const below = new Map<string, string[]>()
  for (const [a, b] of links) {
    below.set(a, [...(below.get(a) || []), b])
    above.set(b, [...(above.get(b) || []), a])
  }
  const position = new Map<string, number>()
  rows.forEach((row) => row.forEach((id, index) => position.set(id, index)))
  for (let sweep = 0; sweep < 8; sweep++) {
    const down = sweep % 2 === 0
    const order = down ? rows.slice(1) : rows.slice(0, -1).reverse()
    for (const row of order) {
      const weight = (id: string) => {
        const linked = (down ? above : below).get(id) || []
        return linked.length
          ? linked.reduce((sum, other) => sum + position.get(other)!, 0) / linked.length
          : position.get(id)!
      }
      const weights = new Map(row.map((id) => [id, weight(id)]))
      row.sort((a, b) => weights.get(a)! - weights.get(b)! || position.get(a)! - position.get(b)!)
      row.forEach((id, index) => position.set(id, index))
    }
  }
  const size = (id: string) => (isWaypoint(id) ? waypointWidth : nodeWidth)
  const span = (row: string[]) =>
    row.reduce((sum, id) => sum + size(id), 0) + Math.max(0, row.length - 1) * gapX
  const width = pad * 2 + Math.max(nodeWidth, ...rows.map(span))
  const height = pad * 2 + rows.length * nodeHeight + (rows.length - 1) * gapY
  const nodes: MapNode[] = []
  const centers = new Map<string, number>()
  rows.forEach((row, index) => {
    let x = (width - span(row)) / 2
    for (const id of row) {
      centers.set(id, x + size(id) / 2)
      if (!isWaypoint(id)) nodes.push({ id, layer: index, x, y: pad + index * (nodeHeight + gapY) })
      x += size(id) + gapX
    }
  })
  const top = (index: number) => pad + index * (nodeHeight + gapY)
  const at = new Map(nodes.map((node) => [node.id, node]))
  // Ports: the edges leaving a service's bottom are ordered by where they head, those entering its top by where
  // they come from, and each gets its own point along that side.
  const leaving = new Map<string, { index: number; toward: number }[]>()
  const entering = new Map<string, { index: number; from: number }[]>()
  usable.forEach((edge, index) => {
    if (layer.get(edge.to)! <= layer.get(edge.from)!) return
    const chain = waypoints.get(edge.from + '>' + edge.to) || []
    const toward = centers.get(chain[0] ?? edge.to)!
    const from = centers.get(chain[chain.length - 1] ?? edge.from)!
    leaving.set(edge.from, [...(leaving.get(edge.from) || []), { index, toward }])
    entering.set(edge.to, [...(entering.get(edge.to) || []), { index, from }])
  })
  const startX = new Map<number, number>()
  const endX = new Map<number, number>()
  for (const [id, list] of leaving) {
    list.sort((a, b) => a.toward - b.toward || a.index - b.index)
    list.forEach((item, order) => startX.set(item.index, centers.get(id)! + spread(list.length, order)))
  }
  for (const [id, list] of entering) {
    list.sort((a, b) => a.from - b.from || a.index - b.index)
    list.forEach((item, order) => endX.set(item.index, centers.get(id)! + spread(list.length, order)))
  }
  const drawn = usable.map((edge, index) => {
    const a = at.get(edge.from)!
    const b = at.get(edge.to)!
    if (b.layer <= a.layer) {
      // Same row or pointing back up: leave and enter from the sides.
      const right = b.x >= a.x
      const x1 = right ? a.x + nodeWidth : a.x
      const y1 = a.y + nodeHeight / 2
      const x2 = right ? b.x : b.x + nodeWidth
      const y2 = b.y + nodeHeight / 2
      const bend = Math.max(40, Math.abs(x2 - x1) / 2)
      const c1 = right ? x1 + bend : x1 - bend
      const c2 = right ? x2 - bend : x2 + bend
      const lift = b.layer === a.layer ? -46 : 0
      const pieces: Piece[] = [[x1, y1, c1, y1 + lift, c2, y2 + lift, x2, y2]]
      return { ...edge, path: pathOf(pieces), pieces, mid: pointAt(pieces, 0.5) }
    }
    // Down through each waypoint: enter it at the top of its layer, leave at the bottom.
    const chain = waypoints.get(edge.from + '>' + edge.to) || []
    const points: [number, number][] = [[startX.get(index)!, a.y + nodeHeight]]
    chain.forEach((id, step) => {
      const x = centers.get(id)!
      points.push([x, top(a.layer + 1 + step)], [x, top(a.layer + 1 + step) + nodeHeight])
    })
    points.push([endX.get(index)!, b.y])
    const pieces: Piece[] = []
    for (let step = 1; step < points.length; step++) {
      const [x0, y0] = points[step - 1]
      const [x1, y1] = points[step]
      if (x0 === x1 && step % 2 === 0) {
        pieces.push([x0, y0, x0, y0, x1, y1, x1, y1])
        continue
      }
      const bend = Math.max(20, (y1 - y0) / 2)
      pieces.push([x0, y0, x0, y0 + bend, x1, y1 - bend, x1, y1])
    }
    // The default label spot is on the first bend, between the source and the next layer.
    return { ...edge, path: pathOf(pieces), pieces, mid: pointAt([pieces[0]], 0.5) }
  })
  return { nodes, edges: drawn, width, height }
}

/* About how wide a label pill is: CJK characters are about twice as wide as Latin ones at 11px. */
export function labelWidth(text: string) {
  let width = 18
  for (const char of text) width += char.charCodeAt(0) >= 0x2e80 ? 11 : 6.4
  return Math.min(labelMax, Math.ceil(width))
}
const labelMax = 168
const labelHeight = 20

type Box = { left: number; top: number; right: number; bottom: number }
const overlap = (a: Box, b: Box) =>
  Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left)) *
  Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top))

/*
 * Labels go where they cover neither a service nor another label: each tries spots along its own line, nearest
 * the middle first, and takes the first clear one, or the least covered if none is clear.
 */
export function placeLabels(map: MapLayout, labels: { edge: number; width: number }[]): [number, number][] {
  const nodes: Box[] = map.nodes.map((node) => ({
    left: node.x - 4,
    top: node.y - 4,
    right: node.x + nodeWidth + 4,
    bottom: node.y + nodeHeight + 14,
  }))
  const placed: Box[] = []
  const spots = [0.5, 0.4, 0.6, 0.3, 0.7, 0.22, 0.78, 0.14, 0.86]
  return labels.map(({ edge, width }) => {
    const pieces = map.edges[edge].pieces
    let best: { at: [number, number]; box: Box; score: number } | undefined
    for (const t of spots) {
      for (const nudge of [0, -labelHeight * 0.75, labelHeight * 0.75]) {
        const [px, py] = pointAt(pieces, t)
        const x = Math.max(width / 2 + 2, Math.min(map.width - width / 2 - 2, px))
        const y = py + nudge
        const box = {
          left: x - width / 2 - 3,
          top: y - labelHeight / 2 - 2,
          right: x + width / 2 + 3,
          bottom: y + labelHeight / 2 + 2,
        }
        const score =
          nodes.reduce((sum, node) => sum + overlap(box, node) * 3, 0) +
          placed.reduce((sum, other) => sum + overlap(box, other) * 2, 0) +
          Math.abs(t - 0.5) * 8 +
          Math.abs(nudge) * 0.5
        if (!best || score < best.score) best = { at: [x, y], box, score }
      }
      if (best && best.score <= Math.abs(t - 0.5) * 8 + 0.01) break
    }
    placed.push(best!.box)
    return best!.at
  })
}

/* The edges among a set of services: sub-service lines and dependencies. */
export function mapEdges(monitors: Monitor[], ids: Set<string>): MapEdge[] {
  const edges: MapEdge[] = []
  for (const monitor of monitors) {
    if (!ids.has(monitor.id)) continue
    if (monitor.parent && ids.has(monitor.parent))
      edges.push({ from: monitor.parent, to: monitor.id, relation: 'part' })
    for (const item of monitor.depends || []) {
      if (ids.has(item.id))
        edges.push({
          from: monitor.id,
          to: item.id,
          relation: item.relation in relations ? item.relation : 'uses',
          note: item.note,
          optional: item.optional,
        })
    }
  }
  return edges
}

/*
 * The map as words, for pasting into an agent's context or a document: every service with its role, every link
 * "A → B：关系（说明）", the sub-services, and in Markdown a mermaid graph of the same.
 */
export function relationsText(
  monitors: Monitor[],
  ids: Set<string>,
  title: string,
  format: 'markdown' | 'text',
) {
  const byID = new Map(monitors.map((monitor) => [monitor.id, monitor]))
  const shown = monitors.filter((monitor) => ids.has(monitor.id))
  const edges = mapEdges(monitors, ids)
  const name = (id: string) => byID.get(id)?.name || id
  const role = (monitor: Monitor) => roles[roleOf(monitor)].name
  const links = edges.filter((edge) => edge.relation !== 'part')
  const parts = new Map<string, string[]>()
  for (const edge of edges)
    if (edge.relation === 'part') parts.set(edge.from, [...(parts.get(edge.from) || []), name(edge.to)])
  const relationName = (edge: MapEdge) => relations[edge.relation as Relation]?.name || '依赖'
  const cell = (text: string) => text.replace(/\|/g, '\\|').replace(/\n/g, ' ')
  if (format === 'text') {
    const lines = [title, '', '服务：']
    for (const monitor of shown)
      lines.push(
        '- ' +
          monitor.name +
          '（' +
          role(monitor) +
          '）' +
          (monitor.description ? '：' + monitor.description : ''),
      )
    lines.push('', '关系（使用方 → 被使用方）：')
    if (!links.length) lines.push('- 还没有写依赖关系')
    for (const edge of links)
      lines.push(
        '- ' +
          name(edge.from) +
          ' → ' +
          name(edge.to) +
          '：' +
          relationName(edge) +
          (edge.optional ? '（可选）' : '') +
          (edge.note ? '，' + edge.note : ''),
      )
    if (parts.size) {
      lines.push('', '子服务：')
      for (const [parent, kids] of parts) lines.push('- ' + name(parent) + ' 包含 ' + kids.join('、'))
    }
    return lines.join('\n') + '\n'
  }
  const lines = [
    '# ' + title,
    '',
    '## 服务',
    '',
    '| 服务 | ID | 角色 | 状态 | 说明 |',
    '|---|---|---|---|---|',
  ]
  const stateText: Record<string, string> = { up: '正常', down: '异常', unknown: '未知', pending: '等待数据' }
  for (const monitor of shown)
    lines.push(
      '| ' +
        [
          cell(monitor.name),
          '`' + monitor.id + '`',
          role(monitor),
          stateText[monitor.overall || monitor.state] || '未知',
          cell(monitor.description || ''),
        ].join(' | ') +
        ' |',
    )
  lines.push('', '## 关系', '')
  if (links.length) {
    lines.push('| 使用方 | 关系 | 被使用方 | 可选 | 说明 |', '|---|---|---|---|---|')
    for (const edge of links)
      lines.push(
        '| ' +
          [
            cell(name(edge.from)),
            relationName(edge),
            cell(name(edge.to)),
            edge.optional ? '是' : '',
            cell(edge.note || ''),
          ].join(' | ') +
          ' |',
      )
  } else lines.push('还没有写依赖关系。')
  if (parts.size) {
    lines.push('', '## 子服务', '')
    for (const [parent, kids] of parts) lines.push('- ' + name(parent) + '：' + kids.join('、'))
  }
  const node = (id: string) => id.replace(/[^a-zA-Z0-9_]/g, '_') + '["' + name(id).replace(/"/g, "'") + '"]'
  lines.push('', '```mermaid', 'graph LR')
  for (const edge of edges)
    lines.push(
      '  ' +
        node(edge.from) +
        (edge.relation === 'part'
          ? ' -.-|子服务| '
          : edge.optional
            ? ' -.->|' + relationName(edge) + '| '
            : ' -->|' + relationName(edge) + '| ') +
        node(edge.to),
    )
  for (const monitor of shown)
    if (!edges.some((edge) => edge.from === monitor.id || edge.to === monitor.id))
      lines.push('  ' + node(monitor.id))
  lines.push('```', '')
  return lines.join('\n')
}

/* The services worth drawing around one service: its parent and children, what it uses and who uses it. */
export function neighbourhood(monitors: Monitor[], focus: string) {
  const byID = new Map(monitors.map((monitor) => [monitor.id, monitor]))
  const center = byID.get(focus)
  const ids = new Set<string>()
  if (!center) return ids
  ids.add(focus)
  if (center.parent) ids.add(center.parent)
  monitors.filter((monitor) => monitor.parent === focus).forEach((child) => ids.add(child.id))
  for (const item of [...(center.depends || []), ...(center.dependents || [])]) ids.add(item.id)
  return new Set([...ids].filter((id) => byID.has(id)))
}

/* A board's services plus whatever they use or are used by elsewhere, marked as outside. */
export function boardScope(monitors: Monitor[], ids: string[]) {
  const inside = new Set(ids)
  const all = new Set(ids)
  for (const monitor of monitors) {
    if (!inside.has(monitor.id)) continue
    for (const item of [...(monitor.depends || []), ...(monitor.dependents || [])]) all.add(item.id)
  }
  const known = new Set(monitors.map((monitor) => monitor.id))
  return { all: new Set([...all].filter((id) => known.has(id))), inside }
}

/* What a line says in words: its relation, whether the service can live without the other, and the note. */
export function edgeText(edge: MapEdge) {
  if (edge.relation === 'part') return '子服务'
  return (
    relations[edge.relation].name + (edge.optional ? ' · 可选' : '') + (edge.note ? ' · ' + edge.note : '')
  )
}

const labelled = (edge: MapEdge) =>
  edge.relation !== 'part' && (edge.relation !== 'uses' || !!edge.note || !!edge.optional)

export function DependencyMap({
  monitors,
  ids,
  inside,
  focus,
  fold = false,
  label = '依赖关系图',
}: {
  monitors: Monitor[]
  ids: Set<string>
  /* Services outside this set are drawn faded, as belonging elsewhere. */
  inside?: Set<string>
  focus?: string
  /* Healthy plain sub-services (no links of their own) fold into a chip under their service until it is opened. */
  fold?: boolean
  label?: string
}) {
  const [active, setActive] = useState<string>()
  const href = useMonitorHref()
  const [open, setOpen] = useState<Set<string>>(() => new Set())
  const [line, setLine] = useState<number>()
  const byID = useMemo(() => new Map(monitors.map((monitor) => [monitor.id, monitor])), [monitors])
  const folded = useMemo(() => {
    const result = new Map<string, Monitor[]>()
    if (!fold) return result
    for (const monitor of monitors) {
      if (!ids.has(monitor.id) || !monitor.parent || !ids.has(monitor.parent)) continue
      const linked =
        !!monitor.depends?.length ||
        !!monitor.dependents?.length ||
        monitors.some((other) => other.parent === monitor.id)
      // A failing part stays on the map: trouble is never folded away.
      const failing = (monitor.overall || monitor.state) === 'down'
      if (!linked && !failing) result.set(monitor.parent, [...(result.get(monitor.parent) || []), monitor])
    }
    return result
  }, [fold, monitors, ids])
  const shown = useMemo(() => {
    const hidden = new Set<string>()
    for (const [parent, kids] of folded) if (!open.has(parent)) kids.forEach((kid) => hidden.add(kid.id))
    return new Set([...ids].filter((id) => !hidden.has(id)))
  }, [ids, folded, open])
  const ordered = useMemo(
    () => monitors.filter((monitor) => shown.has(monitor.id)).map((monitor) => monitor.id),
    [monitors, shown],
  )
  const edges = useMemo(() => mapEdges(monitors, shown), [monitors, shown])
  const toggle = (id: string) =>
    setOpen((previous) => {
      const next = new Set(previous)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  const map = useMemo(() => layoutMap(ordered, edges), [ordered, edges])
  const labels = useMemo(() => {
    const wanted = map.edges
      .map((edge, index) => ({ edge: index, text: edgeText(edge) }))
      .filter((item) => labelled(map.edges[item.edge]))
    const spots = placeLabels(
      map,
      wanted.map((item) => ({ edge: item.edge, width: labelWidth(item.text) })),
    )
    return wanted.map((item, index) => ({ ...item, at: spots[index] }))
  }, [map])
  const touching = (id: string) => edges.some((edge) => edge.from === id || edge.to === id)
  const lit = (edge: MapEdge, index: number) =>
    line !== undefined ? index === line : !active || edge.from === active || edge.to === active
  const near = (id: string) => {
    if (line !== undefined) return id === map.edges[line].from || id === map.edges[line].to
    return (
      !active ||
      id === active ||
      edges.some(
        (edge) => (edge.from === active && edge.to === id) || (edge.to === active && edge.from === id),
      )
    )
  }
  const used = [...new Set(edges.map((edge) => edge.relation))]
  const anyOptional = edges.some((edge) => edge.optional)
  const scroller = useRef<HTMLDivElement>(null)
  const [room, setRoom] = useState(0)
  const [whole, setWhole] = useState(false)
  const { copied, copy } = useCopy()
  const [copiedAs, setCopiedAs] = useState('')
  // The map shrinks to the width it is given rather than scrolling sideways; only past about half size does it
  // scroll. Small width changes (a scrollbar coming and going) are ignored, so the map never shivers.
  useEffect(() => {
    const box = scroller.current
    if (!box || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => {
      const width = box.clientWidth
      setRoom((previous) => (Math.abs(previous - width) > 24 ? width : previous))
    })
    observer.observe(box)
    return () => observer.disconnect()
  }, [])
  const scale = !whole && room > 0 && map.width > room ? Math.max(0.55, room / map.width) : 1
  // A map still wider than its box opens centred, so the top service is in view on a phone.
  useEffect(() => {
    const box = scroller.current
    if (box) box.scrollLeft = Math.max(0, (box.scrollWidth - box.clientWidth) / 2)
  }, [map.width, scale])
  useEffect(() => {
    if (!whole) return
    const key = (event: KeyboardEvent) => event.key === 'Escape' && setWhole(false)
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [whole])
  const copyAs = (format: 'markdown' | 'text') => {
    copy(relationsText(monitors, ids, label, format))
    setCopiedAs(format)
  }
  return (
    <figure className="dm" aria-label={label} data-whole={whole || undefined}>
      <div className="dm-tools" role="toolbar" aria-label="关系图操作">
        <button
          onClick={() => copyAs('markdown')}
          title="复制成 Markdown：服务表、关系表和 mermaid 图，可直接交给 Agent"
        >
          <Glyph name={copied && copiedAs === 'markdown' ? 'check' : 'copy'} size={13} />
          {copied && copiedAs === 'markdown' ? '已复制' : 'Markdown'}
        </button>
        <button onClick={() => copyAs('text')} title="复制成一段纯文本的关系说明">
          <Glyph name={copied && copiedAs === 'text' ? 'check' : 'copy'} size={13} />
          {copied && copiedAs === 'text' ? '已复制' : '文本'}
        </button>
        {(scale < 1 || whole) && (
          <button onClick={() => setWhole((value) => !value)} aria-pressed={whole}>
            <Glyph name={whole ? 'close' : 'eye'} size={13} />
            {whole ? '退出全屏' : '全屏查看'}
          </button>
        )}
      </div>
      <div className="dm-scroll" ref={scroller}>
        <div
          className="dm-fit"
          style={scale < 1 ? { width: map.width * scale, height: map.height * scale } : undefined}
        >
          <div
            className="dm-canvas"
            style={{
              width: map.width,
              height: map.height,
              transform: scale < 1 ? 'scale(' + scale + ')' : undefined,
            }}
          >
            <svg className="dm-edges" width={map.width} height={map.height} aria-hidden="true">
              <defs>
                {(Object.keys(relations) as Relation[]).map((relation) => (
                  <marker
                    key={relation}
                    id={'dm-head-' + relation}
                    className="dm-head"
                    data-relation={relation}
                    data-head={relations[relation].head}
                    viewBox="-1 -1 12 12"
                    refX="9"
                    refY="5"
                    markerWidth="7.5"
                    markerHeight="7.5"
                    orient="auto-start-reverse"
                  >
                    <path d={headPath(relations[relation].head)} />
                  </marker>
                ))}
              </defs>
              {map.edges.map((edge, index) => {
                const meta = edge.relation === 'part' ? undefined : relations[edge.relation]
                const head = meta ? `url(#dm-head-${edge.relation})` : undefined
                return (
                  <g
                    key={edge.from + '>' + edge.to + ':' + edge.relation}
                    className="dm-link"
                    data-relation={edge.relation}
                    data-dim={lit(edge, index) ? undefined : ''}
                    onMouseEnter={() => setLine(index)}
                    onMouseLeave={() => setLine(undefined)}
                  >
                    <title>
                      {(byID.get(edge.from)?.name || edge.from) +
                        ' → ' +
                        (byID.get(edge.to)?.name || edge.to) +
                        '：' +
                        edgeText(edge)}
                    </title>
                    <path className="dm-hit" d={edge.path} />
                    <path
                      className="dm-edge"
                      data-relation={edge.relation}
                      data-dash={meta?.dash}
                      data-optional={edge.optional || undefined}
                      d={edge.path}
                      markerEnd={head}
                      markerStart={meta?.both ? head : undefined}
                    />
                  </g>
                )
              })}
            </svg>
            {labels.map((item) => {
              const edge = map.edges[item.edge]
              return (
                <span
                  key={'label' + edge.from + '>' + edge.to + ':' + edge.relation}
                  className="dm-label"
                  data-relation={edge.relation}
                  data-optional={edge.optional || undefined}
                  data-dim={lit(edge, item.edge) ? undefined : ''}
                  style={{ left: item.at[0], top: item.at[1], maxWidth: labelMax }}
                  title={item.text}
                >
                  {item.text}
                </span>
              )
            })}
            {map.nodes.map((node) => {
              const monitor = byID.get(node.id)!
              const role = roleOf(monitor)
              const state = monitor.overall || monitor.state
              const outside = inside && !inside.has(node.id)
              const kids = folded.get(node.id)
              const opened = open.has(node.id)
              const failing = kids?.filter((kid) => (kid.overall || kid.state) === 'down').length || 0
              return (
                <Fragment key={node.id}>
                  <a
                    className="dm-node"
                    href={href(node.id)}
                    style={{ left: node.x, top: node.y, width: nodeWidth, height: nodeHeight }}
                    data-state={state}
                    data-focus={node.id === focus || undefined}
                    data-outside={outside || undefined}
                    data-dim={near(node.id) ? undefined : ''}
                    data-alone={touching(node.id) ? undefined : ''}
                    onMouseEnter={() => setActive(node.id)}
                    onMouseLeave={() => setActive(undefined)}
                    onFocus={() => setActive(node.id)}
                    onBlur={() => setActive(undefined)}
                    title={monitor.description || monitor.name}
                  >
                    <RoleIcon role={role} size={30} title="" />
                    <span className="dm-text">
                      <b>{monitor.name}</b>
                      <small>
                        {roles[role].name}
                        {outside ? ' · 在别的面板' : monitor.parent ? ' · 子服务' : ''}
                      </small>
                    </span>
                    <i
                      className="dm-dot"
                      aria-label={state === 'up' ? '正常' : state === 'down' ? '异常' : '未知'}
                    />
                  </a>
                  {kids && (
                    <button
                      className="dm-kids"
                      style={{ left: node.x + nodeWidth / 2, top: node.y + nodeHeight }}
                      data-dim={near(node.id) ? undefined : ''}
                      data-failing={failing || undefined}
                      aria-expanded={opened}
                      aria-label={
                        (opened ? '收起' : '展开') + monitor.name + '的 ' + kids.length + ' 个子服务'
                      }
                      onClick={() => toggle(node.id)}
                    >
                      <span aria-hidden="true">
                        {kids.slice(0, 6).map((kid) => (
                          <i key={kid.id} data-state={kid.overall || kid.state} />
                        ))}
                      </span>
                      {opened
                        ? '收起子服务'
                        : kids.length + ' 个子服务' + (failing ? ' · ' + failing + ' 异常' : '')}
                    </button>
                  )}
                </Fragment>
              )
            })}
          </div>
        </div>
      </div>
      {used.length > 0 && (
        <figcaption className="dm-legend">
          {used.includes('part') && (
            <span>
              <i className="dm-part" />
              子服务
            </span>
          )}
          {(Object.keys(relations) as Relation[])
            .filter((relation) => used.includes(relation))
            .map((relation) => (
              <span key={relation}>
                <RelationLine relation={relation} />
                {relations[relation].name}
              </span>
            ))}
          {anyOptional && (
            <span>
              <RelationLine relation="uses" optional />
              可选依赖
            </span>
          )}
        </figcaption>
      )}
    </figure>
  )
}
