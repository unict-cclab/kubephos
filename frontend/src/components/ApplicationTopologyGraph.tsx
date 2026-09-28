import {useState} from 'react'
import type {ApplicationComponent} from '../types'
import {exportChart} from './ResultsView'

const margin = 54
const nodeRadius = 14

export interface TopologyNode {
  component: ApplicationComponent
  x: number
  y: number
  label: string
  labelWidth: number
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

function displayLabel(value: string): string {
  return value
}

function componentOrder(components: ApplicationComponent[]): ApplicationComponent[] {
  const byID = new Map(components.map(component => [component.id, component]))
  const incoming = new Map(components.map(component => [component.id, 0]))
  const outgoing = new Map(components.map(component => [component.id, [] as string[]]))
  for (const component of components) for (const dependency of component.dependencies ?? []) {
    if (!byID.has(dependency)) continue
    outgoing.get(component.id)?.push(dependency)
    incoming.set(dependency, (incoming.get(dependency) ?? 0) + 1)
  }
  const compare = (left: ApplicationComponent, right: ApplicationComponent) => {
    const leftEntry = left.traits?.includes('traffic-entrypoint') ? 0 : 1
    const rightEntry = right.traits?.includes('traffic-entrypoint') ? 0 : 1
    return leftEntry - rightEntry || left.id.localeCompare(right.id)
  }
  const ready = components.filter(component => incoming.get(component.id) === 0).sort(compare)
  const ordered: ApplicationComponent[] = []
  const visited = new Set<string>()
  while (ready.length) {
    const current = ready.shift()!
    if (visited.has(current.id)) continue
    visited.add(current.id)
    ordered.push(current)
    for (const dependency of outgoing.get(current.id) ?? []) {
      incoming.set(dependency, (incoming.get(dependency) ?? 1) - 1)
      if (incoming.get(dependency) === 0) {
        ready.push(byID.get(dependency)!)
        ready.sort(compare)
      }
    }
  }
  ordered.push(...components.filter(component => !visited.has(component.id)).sort(compare))
  return ordered
}

export function buildApplicationTopology(components: ApplicationComponent[]): ApplicationTopology | null {
  if (!components.length) return null
  const ordered = componentOrder(components)
  const positions = ordered.map((_, position) => {
    if (position === 0) return {x: 0, y: 0}
    const angle = position * Math.PI * (3 - Math.sqrt(5))
    const radius = Math.sqrt(position) * 96
    return {x: Math.cos(angle) * radius, y: Math.sin(angle) * radius * .82}
  })
  const labelWidths = ordered.map(component => {
    const label = displayLabel(component.id)
    return Math.max(74, label.length * 7 + 22)
  })
  const componentPosition = new Map(ordered.map((component, position) => [component.id, position]))
  const links = ordered.flatMap((component, source) => (component.dependencies ?? []).flatMap(dependency => {
    const target = componentPosition.get(dependency)
    return target === undefined ? [] : [{source, target}]
  }))
  for (let iteration = 0; iteration < 180; iteration++) {
    const forces = positions.map(() => ({x: 0, y: 0}))
    for (let left = 0; left < positions.length; left++) for (let right = left + 1; right < positions.length; right++) {
      let dx = positions[right].x - positions[left].x
      let dy = positions[right].y - positions[left].y
      if (Math.abs(dx) + Math.abs(dy) < .01) {
        dx = right % 2 === 0 ? 1 : -1
        dy = 1
      }
      const distanceSquared = Math.max(64, dx * dx + dy * dy)
      const distance = Math.sqrt(distanceSquared)
      const strength = 18000 / distanceSquared
      const forceX = dx / distance * strength
      const forceY = dy / distance * strength
      forces[left].x -= forceX
      forces[left].y -= forceY
      forces[right].x += forceX
      forces[right].y += forceY
    }
    for (const link of links) {
      const dx = positions[link.target].x - positions[link.source].x
      const dy = positions[link.target].y - positions[link.source].y
      const distance = Math.max(1, Math.hypot(dx, dy))
      const strength = (distance - 175) * .018
      const forceX = dx / distance * strength
      const forceY = dy / distance * strength
      forces[link.source].x += forceX
      forces[link.source].y += forceY
      forces[link.target].x -= forceX
      forces[link.target].y -= forceY
    }
    const cooling = 1 - iteration / 220
    positions.forEach((position, index) => {
      position.x += (forces[index].x - position.x * .003) * cooling
      position.y += (forces[index].y - position.y * .003) * cooling
    })
  }
  for (let iteration = 0; iteration < 120; iteration++) {
    let moved = false
    for (let left = 0; left < positions.length; left++) for (let right = left + 1; right < positions.length; right++) {
      const dx = positions[right].x - positions[left].x
      const dy = positions[right].y - positions[left].y
      const overlapX = (labelWidths[left] + labelWidths[right]) / 2 + 24 - Math.abs(dx)
      const overlapY = 84 - Math.abs(dy)
      if (overlapX <= 0 || overlapY <= 0) continue
      moved = true
      if (overlapX < overlapY * 1.8) {
        const shift = overlapX / 2 + .5
        const direction = dx >= 0 ? 1 : -1
        positions[left].x -= direction * shift
        positions[right].x += direction * shift
      } else {
        const shift = overlapY / 2 + .5
        const direction = dy >= 0 ? 1 : -1
        positions[left].y -= direction * shift
        positions[right].y += direction * shift
      }
    }
    if (!moved) break
  }
  const minimumX = Math.min(...positions.map((position, index) => position.x - labelWidths[index] / 2))
  const maximumX = Math.max(...positions.map((position, index) => position.x + labelWidths[index] / 2))
  const minimumY = Math.min(...positions.map(position => position.y - nodeRadius))
  const maximumY = Math.max(...positions.map(position => position.y + 52))
  const width = Math.max(300, maximumX - minimumX + margin * 2)
  const height = Math.max(190, maximumY - minimumY + margin * 2)
  const nodes = ordered.map((component, position) => ({
    component,
    x: positions[position].x - minimumX + margin,
    y: positions[position].y - minimumY + margin,
    label: displayLabel(component.id),
    labelWidth: labelWidths[position]
  }))
  const byID = new Map(nodes.map(node => [node.component.id, node]))
  const edges: TopologyEdge[] = []
  for (const source of nodes) for (const dependency of source.component.dependencies ?? []) {
    const target = byID.get(dependency)
    if (target) edges.push({source, target})
  }
  return {nodes, edges, width, height}
}

function edgePath({source, target}: TopologyEdge, position: number): string {
  const dx = target.x - source.x
  const dy = target.y - source.y
  const distance = Math.max(1, Math.hypot(dx, dy))
  const directionX = dx / distance
  const directionY = dy / distance
  const startX = source.x + directionX * (nodeRadius + 2)
  const startY = source.y + directionY * (nodeRadius + 2)
  const endX = target.x - directionX * (nodeRadius + 4)
  const endY = target.y - directionY * (nodeRadius + 4)
  const bend = Math.min(38, distance * .1) * (position % 2 === 0 ? 1 : -1)
  const middleX = (startX + endX) / 2 - directionY * bend
  const middleY = (startY + endY) / 2 + directionX * bend
  return `M ${startX} ${startY} Q ${middleX} ${middleY}, ${endX} ${endY}`
}

export function ApplicationTopologyGraph({components}: {components: ApplicationComponent[]}) {
  const topology = buildApplicationTopology(components)
  const [exportError, setExportError] = useState('')
  if (!topology) return null
  const chartID = 'application-topology-graph'
  const output = (format: 'pdf' | 'png') => exportChart(chartID, 'application-topology', format).then(() => setExportError('')).catch(cause => setExportError(cause instanceof Error ? cause.message : 'Could not export application topology.'))
  return <section className="details-section application-topology-section">
    <div className="details-heading"><strong>Application topology</strong><div className="chart-export-actions"><button type="button" onClick={() => void output('pdf')}>PDF</button><button type="button" onClick={() => void output('png')}>PNG 600 DPI</button></div></div>
    {exportError && <p className="form-error">{exportError}</p>}
    <div className="application-topology-scroll">
      <svg id={chartID} className="application-topology-graph" width={topology.width} height={topology.height} viewBox={`0 0 ${topology.width} ${topology.height}`} role="img" aria-label="Application component topology">
        <title>Application service graph</title>
        <rect width={topology.width} height={topology.height} fill="#ffffff" />
        <defs><marker id="application-topology-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#718096" /></marker></defs>
        <g className="application-topology-edges" fill="none" stroke="#566274" strokeWidth="2.2">{topology.edges.map((edge, index) => <path key={`${edge.source.component.id}-${edge.target.component.id}`} d={edgePath(edge, index)} markerEnd="url(#application-topology-arrow)" />)}</g>
        <g>{topology.nodes.map(node => {
          const entrypoint = node.component.traits?.includes('traffic-entrypoint')
          return <g className={`application-topology-node${entrypoint ? ' entrypoint' : ''}`} key={node.component.id}><title>{node.component.id}</title><circle cx={node.x} cy={node.y} r={nodeRadius} fill={entrypoint ? '#59616b' : '#9198a1'} stroke="#ffffff" strokeWidth="2.5" /><rect className="application-topology-label-bg" x={node.x - node.labelWidth / 2} y={node.y + 22} width={node.labelWidth} height="25" rx="8" fill="#ffffff" stroke="#dfe4eb" strokeWidth="1" /><text className="application-topology-name" x={node.x} y={node.y + 39} textAnchor="middle" fill="#303842" fontSize="12" fontWeight="700" fontFamily="Manrope, sans-serif">{node.label}</text></g>
        })}</g>
      </svg>
    </div>
    <div className="application-topology-legend"><span><i className="entrypoint" />Node proxy</span><span><i />Application service</span><span>{topology.edges.length} connections</span></div>
  </section>
}
