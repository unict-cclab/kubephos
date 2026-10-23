import {useState} from 'react'
import type {ApplicationComponent} from '../types'
import {exportChart} from '../charts'

const margin = 54
const nodeRadius = 14

export interface TopologyNode {
  component: ApplicationComponent
  x: number
  y: number
  label: string
  labelWidth: number
  row: number
}

export interface TopologyEdge {
  source: TopologyNode
  target: TopologyNode
}

export interface ApplicationTopology {
  nodes: TopologyNode[]
  edges: TopologyEdge[]
  width: number
  height: number
}

function compareComponents(left: ApplicationComponent, right: ApplicationComponent): number {
  return Number(!left.traits?.includes('traffic-entrypoint')) - Number(!right.traits?.includes('traffic-entrypoint')) || left.id.localeCompare(right.id, undefined, {numeric: true})
}

export function buildApplicationTopology(components: ApplicationComponent[]): ApplicationTopology | null {
  if (!components.length) return null
  const ordered = [...components].sort(compareComponents)
  const byID = new Map(ordered.map(component => [component.id, component]))
  const dependencies = new Map(ordered.map(component => [component.id, [...new Set(component.dependencies ?? [])].filter(id => byID.has(id)).sort((left, right) => compareComponents(byID.get(left)!, byID.get(right)!))]))
  // Collapse cycles before assigning dependency levels; all original edges remain visible.
  const indices = new Map<string, number>()
  const low = new Map<string, number>()
  const stack: string[] = []
  const active = new Set<string>()
  const groups: string[][] = []
  const visit = (id: string) => {
    indices.set(id, indices.size)
    low.set(id, indices.get(id)!)
    stack.push(id)
    active.add(id)
    for (const next of dependencies.get(id)!) {
      if (!indices.has(next)) {
        visit(next)
        low.set(id, Math.min(low.get(id)!, low.get(next)!))
      } else if (active.has(next)) low.set(id, Math.min(low.get(id)!, indices.get(next)!))
    }
    if (low.get(id) === indices.get(id)) {
      const group: string[] = []
      let next: string
      do {
        next = stack.pop()!
        active.delete(next)
        group.push(next)
      } while (next !== id)
      groups.push(group)
    }
  }
  ordered.forEach(component => {if (!indices.has(component.id)) visit(component.id)})
  const groupOf = new Map(groups.flatMap((group, index) => group.map(id => [id, index] as const)))
  const outgoing = groups.map(() => new Set<number>())
  const incoming = groups.map(() => 0)
  const levels = groups.map(() => 0)
  const parents = new Map(ordered.map(component => [component.id, [] as string[]]))
  for (const [source, targets] of dependencies) for (const target of targets) {
    parents.get(target)!.push(source)
    const from = groupOf.get(source)!, to = groupOf.get(target)!
    if (from === to || outgoing[from].has(to)) continue
    outgoing[from].add(to)
    incoming[to]++
  }
  const ready = incoming.flatMap((count, index) => count === 0 ? [index] : [])
  while (ready.length) {
    const current = ready.shift()!
    for (const next of outgoing[current]) {
      levels[next] = Math.max(levels[next], levels[current] + 1)
      if (--incoming[next] === 0) ready.push(next)
    }
  }
  const layers: ApplicationComponent[][] = []
  for (const component of ordered) (layers[levels[groupOf.get(component.id)!]] ??= []).push(component)
  const labelWidth = (component: ApplicationComponent) => Math.max(74, component.id.length * 7 + 22)
  const cellWidth = Math.max(160, ...ordered.map(component => labelWidth(component) + 32))
  // A forest has at most one caller per node. Allocate each subtree its own
  // horizontal span so levels never wrap and parents stay centered on children.
  if (groups.every(group => group.length === 1) && ordered.every(component => parents.get(component.id)!.length <= 1 && !dependencies.get(component.id)!.includes(component.id))) {
    const roots = ordered.filter(component => !parents.get(component.id)!.length)
    const spans = new Map<string, number>()
    const siblingGap = 18
    const minimumSpan = Math.max(...ordered.map(component => labelWidth(component) + 32))
    const measure = (id: string): number => {
      const children = dependencies.get(id)!
      const childWidth = children.reduce((total, child) => total + measure(child), 0) + Math.max(0, children.length - 1) * siblingGap
      const span = Math.max(minimumSpan, childWidth)
      spans.set(id, span)
      return span
    }
    const contentWidth = Math.max(220, roots.reduce((total, root) => total + measure(root.id), 0) + (roots.length - 1) * 64)
    const width = contentWidth + margin * 2
    const placed = new Map<string, TopologyNode>()
    const place = (id: string, left: number, depth: number): TopologyNode => {
      const component = byID.get(id)!
      const children = dependencies.get(id)!
      const childWidth = children.reduce((total, child) => total + spans.get(child)!, 0) + Math.max(0, children.length - 1) * siblingGap
      let cursor = left + (spans.get(id)! - childWidth) / 2
      const childNodes = children.map(child => {
        const node = place(child, cursor, depth + 1)
        cursor += spans.get(child)! + siblingGap
        return node
      })
      const x = childNodes.length ? (childNodes[0].x + childNodes.at(-1)!.x) / 2 : left + spans.get(id)! / 2
      const node = {component, x, y: margin + nodeRadius + depth * 132, row: depth, label: id, labelWidth: labelWidth(component)}
      placed.set(id, node)
      return node
    }
    const totalSpan = roots.reduce((total, root) => total + spans.get(root.id)!, 0) + (roots.length - 1) * 64
    let cursor = margin + (contentWidth - totalSpan) / 2
    roots.forEach(root => { place(root.id, cursor, 0); cursor += spans.get(root.id)! + 64 })
    const nodes = [...placed.values()].sort((left, right) => left.row - right.row || left.x - right.x || compareComponents(left.component, right.component))
    const edges = nodes.flatMap(source => dependencies.get(source.component.id)!.map(id => ({source, target: placed.get(id)!})))
    return {nodes, edges, width, height: Math.max(190, margin * 2 + nodeRadius + Math.max(...nodes.map(node => node.row)) * 132 + 52)}
  }
  const contentWidth = Math.max(220, Math.max(...layers.map(layer => layer.length)) * cellWidth)
  const width = contentWidth + margin * 2
  const nodes: TopologyNode[] = []
  const placed = new Map<string, TopologyNode>()
  let row = 0
  for (const layer of layers) {
    // Keep siblings near one another using the horizontal position of their callers.
    const parentCenter = (component: ApplicationComponent) => {
      const callers = parents.get(component.id)!.flatMap(id => placed.has(id) ? [placed.get(id)!.x] : [])
      return callers.length ? callers.reduce((sum, x) => sum + x, 0) / callers.length : width / 2
    }
    layer.sort((left, right) => parentCenter(left) - parentCenter(right) || compareComponents(left, right))
    layer.forEach((component, index) => {
      const node = {component, x: width / 2 + (index - (layer.length - 1) / 2) * cellWidth, y: margin + nodeRadius + row * 132, row, label: component.id, labelWidth: labelWidth(component)}
      nodes.push(node)
      placed.set(component.id, node)
    })
    row++
  }
  const edges = nodes.flatMap(source => dependencies.get(source.component.id)!.map(id => ({source, target: placed.get(id)!})))
  return {nodes, edges, width, height: Math.max(190, margin * 2 + nodeRadius + (row - 1) * 132 + 52)}
}

function edgePath({source, target}: TopologyEdge, position: number, width: number): string {
  if (source === target) return `M ${source.x + 12} ${source.y - 9} C ${source.x + 48} ${source.y - 48}, ${source.x - 48} ${source.y - 48}, ${source.x - 12} ${source.y - 9}`
  if (source.row === target.row) {
    const top = source.y - (source.x < target.x ? 42 : 58)
    return `M ${source.x} ${source.y - nodeRadius - 2} C ${source.x} ${top}, ${target.x} ${top}, ${target.x} ${target.y - nodeRadius - 4}`
  }
  const startY = source.y + nodeRadius + 2
  const endY = target.y - nodeRadius - 4
  if (target.row === source.row + 1) {
    const middle = source.y + 82
    return `M ${source.x} ${startY} C ${source.x} ${middle}, ${target.x} ${middle}, ${target.x} ${endY}`
  }
  // Route long and backward links outside the service rows.
  const gutter = width - 14 - position % 3 * 10
  return `M ${source.x} ${startY} L ${source.x} ${source.y + 66} L ${gutter} ${source.y + 66} L ${gutter} ${target.y - 36} L ${target.x} ${target.y - 36} L ${target.x} ${endY}`
}

export function ApplicationTopologyGraph({components}: {components: ApplicationComponent[]}) {
  const topology = buildApplicationTopology(components)
  const [exportError, setExportError] = useState('')
  if (!topology) return null
  const chartID = 'application-topology-graph'
  const output = (format: 'pdf' | 'png') => exportChart(chartID, 'application-topology', format).then(() => setExportError('')).catch(cause => setExportError(cause instanceof Error ? cause.message : 'Could not export application topology.'))
  return <section className="details-section application-topology-section">
    <div className="details-heading"><strong>Application topology</strong><div className="chart-export-actions"><button className="button secondary compact" type="button" onClick={() => void output('pdf')}>PDF</button><button className="button secondary compact" type="button" onClick={() => void output('png')}>PNG</button></div></div>
    {exportError && <p className="form-error">{exportError}</p>}
    <div className="application-topology-scroll">
      <svg id={chartID} className="application-topology-graph" data-export-size="content" width={topology.width} height={topology.height} viewBox={`0 0 ${topology.width} ${topology.height}`} role="img" aria-label="Application component topology">
        <title>Application service graph</title>
        <rect width={topology.width} height={topology.height} fill="#ffffff" />
        <defs><marker id="application-topology-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#718096" /></marker></defs>
        <g className="application-topology-edges" fill="none" stroke="#566274" strokeWidth="2.2">{topology.edges.map((edge, index) => <path key={`${edge.source.component.id}-${edge.target.component.id}`} d={edgePath(edge, index, topology.width)} markerEnd="url(#application-topology-arrow)" />)}</g>
        <g>{topology.nodes.map(node => {
          const entrypoint = node.component.traits?.includes('traffic-entrypoint')
          return <g className={`application-topology-node${entrypoint ? ' entrypoint' : ''}`} key={node.component.id}><title>{node.component.id}</title><circle cx={node.x} cy={node.y} r={nodeRadius} fill={entrypoint ? '#59616b' : '#9198a1'} stroke="#ffffff" strokeWidth="2.5" /><rect className="application-topology-label-bg" x={node.x - node.labelWidth / 2} y={node.y + 22} width={node.labelWidth} height="25" rx="8" fill="#ffffff" stroke="#dfe4eb" strokeWidth="1" /><text className="application-topology-name" x={node.x} y={node.y + 39} textAnchor="middle" fill="#303842" fontSize="12" fontWeight="500" fontFamily="Geist Variable, Geist, sans-serif">{node.label}</text></g>
        })}</g>
      </svg>
    </div>
    <div className="application-topology-legend"><span><i className="entrypoint" />Node proxy</span><span><i />Application service</span><span>{topology.edges.length} connections</span></div>
  </section>
}
