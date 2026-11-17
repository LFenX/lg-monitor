import { useEffect, useMemo, useRef, useState } from 'react'
import { RelationLine, RoleIcon, relations, roleOf, roles } from './roles'
import type { Monitor, Relation } from './types'
import './dependency-map.css'

/*
 * Who uses whom, drawn top-down: the services people reach first at the top, what they rest on (tunnels,
 * gateways, databases) below. Sub-services hang from their service on a thin grey line; dependencies are
 * coloured arrows, one style per relation. Hovering a service lights up what it touches.
 */

export type MapEdge = { from: string; to: string; relation: Relation | 'part'; note?: string }
export type MapNode = { id: string; x: number; y: number; layer: number }
export type MapLayout = {
  nodes: MapNode[]
  edges: (MapEdge & { path: string; mid: [number, number] })[]
  width: number
  height: number
}

export const nodeWidth = 196
export const nodeHeight = 56
const gapX = 28
const waypointWidth = 18
const gapY = 78
const pad = 20

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
  // Waypoints: "a>b#2" sits in layer 2 on the way from a down to b.
  const links: [string, string][] = []
  const waypoints = new Map<string, string[]>()
  for (const edge of forward) {
    const from = layer.get(edge.from)!
    const to = layer.get(edge.to)!
    let previous = edge.from
    const chain: string[] = []
    for (let at = from + 1; at < to; at++) {
      const id = edge.from + '>' + edge.to + '#' + at
      rows[at].push(id)
      chain.push(id)
      links.push([previous, id])
      previous = id
    }
    links.push([previous, edge.to])
    waypoints.set(edge.from + '>' + edge.to, chain)
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
  for (let sweep = 0; sweep < 6; sweep++) {
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
  const drawn = usable.map((edge) => {
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
      return {
        ...edge,
        path: `M${x1} ${y1} C${c1} ${y1 + lift} ${c2} ${y2 + lift} ${x2} ${y2}`,
        mid: [(x1 + x2) / 2, (y1 + y2) / 2 + lift * 0.75] as [number, number],
      }
    }
    // Down through each waypoint: enter it at the top of its layer, leave at the bottom.
    const chain = waypoints.get(edge.from + '>' + edge.to) || []
    const points: [number, number][] = [[a.x + nodeWidth / 2, a.y + nodeHeight]]
    chain.forEach((id, index) => {
      const x = centers.get(id)!
      points.push([x, top(a.layer + 1 + index)], [x, top(a.layer + 1 + index) + nodeHeight])
    })
    points.push([b.x + nodeWidth / 2, b.y])
    let path = `M${points[0][0]} ${points[0][1]}`
    for (let index = 1; index < points.length; index++) {
      const [x0, y0] = points[index - 1]
      const [x1, y1] = points[index]
      if (x0 === x1 && index % 2 === 0) {
        path += ` L${x1} ${y1}`
        continue
      }
      const bend = Math.max(20, (y1 - y0) / 2)
      path += ` C${x0} ${y0 + bend} ${x1} ${y1 - bend} ${x1} ${y1}`
    }
    // The label sits on the first bend, between the source and the next layer.
    const [x0, y0] = points[0]
    const [x1, y1] = points[1]
    return { ...edge, path, mid: [(x0 + x1) / 2, (y0 + y1) / 2] as [number, number] }
  })
  return { nodes, edges: drawn, width, height }
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
        edges.push({ from: monitor.id, to: item.id, relation: item.relation, note: item.note })
    }
  }
  return edges
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

export function DependencyMap({
  monitors,
  ids,
  inside,
  focus,
  label = '依赖关系图',
}: {
  monitors: Monitor[]
  ids: Set<string>
  /* Services outside this set are drawn faded, as belonging elsewhere. */
  inside?: Set<string>
  focus?: string
  label?: string
}) {
  const [active, setActive] = useState<string>()
  const byID = useMemo(() => new Map(monitors.map((monitor) => [monitor.id, monitor])), [monitors])
  const ordered = useMemo(
    () => monitors.filter((monitor) => ids.has(monitor.id)).map((monitor) => monitor.id),
    [monitors, ids],
  )
  const edges = useMemo(() => mapEdges(monitors, ids), [monitors, ids])
  const map = useMemo(() => layoutMap(ordered, edges), [ordered, edges])
  const touching = (id: string) => edges.some((edge) => edge.from === id || edge.to === id)
  const lit = (edge: MapEdge) => !active || edge.from === active || edge.to === active
  const near = (id: string) =>
    !active ||
    id === active ||
    edges.some((edge) => (edge.from === active && edge.to === id) || (edge.to === active && edge.from === id))
  const used = [...new Set(edges.map((edge) => edge.relation))]
  const scroller = useRef<HTMLDivElement>(null)
  // A map wider than the screen opens centred, so the top service is in view on a phone.
  useEffect(() => {
    const box = scroller.current
    if (box) box.scrollLeft = Math.max(0, (box.scrollWidth - box.clientWidth) / 2)
  }, [map.width])
  return (
    <figure className="dm" aria-label={label}>
      <div className="dm-scroll" ref={scroller}>
        <div className="dm-canvas" style={{ width: map.width, height: map.height }}>
          <svg className="dm-edges" width={map.width} height={map.height} aria-hidden="true">
            <defs>
              {(['uses', 'calls', 'data', 'via', 'runs', 'sends'] as Relation[]).map((relation) => (
                <marker
                  key={relation}
                  id={'dm-arrow-' + relation}
                  data-relation={relation}
                  viewBox="0 0 10 10"
                  refX="9"
                  refY="5"
                  markerWidth="7"
                  markerHeight="7"
                  orient="auto-start-reverse"
                >
                  <path d="M0 1L10 5L0 9z" />
                </marker>
              ))}
            </defs>
            {map.edges.map((edge) => (
              <path
                key={edge.from + '>' + edge.to}
                className="dm-edge"
                data-relation={edge.relation}
                data-dim={lit(edge) ? undefined : ''}
                d={edge.path}
                markerEnd={edge.relation === 'part' ? undefined : `url(#dm-arrow-${edge.relation})`}
              />
            ))}
          </svg>
          {map.edges
            .filter((edge) => edge.relation !== 'part' && (edge.relation !== 'uses' || edge.note))
            .map((edge) => (
              <span
                key={'label' + edge.from + '>' + edge.to}
                className="dm-label"
                data-relation={edge.relation}
                data-dim={lit(edge) ? undefined : ''}
                style={{ left: edge.mid[0], top: edge.mid[1] }}
              >
                {relations[edge.relation as Relation].name}
                {edge.note ? ' · ' + edge.note : ''}
              </span>
            ))}
          {map.nodes.map((node) => {
            const monitor = byID.get(node.id)!
            const role = roleOf(monitor)
            const state = monitor.overall || monitor.state
            const outside = inside && !inside.has(node.id)
            return (
              <a
                key={node.id}
                className="dm-node"
                href={'#/monitors/' + encodeURIComponent(node.id)}
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
            )
          })}
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
        </figcaption>
      )}
    </figure>
  )
}
