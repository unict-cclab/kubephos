import {useState} from 'react'
import type {ApplicationComponent} from '../types'
import {exportChart} from './ResultsView'

const nodeWidth = 190
const nodeHeight = 46
const columnGap = 92
const rowGap = 22
const marginX = 34
const headerHeight = 48
const footerHeight = 28

export interface TopologyNode {
  component: ApplicationComponent
  layer: number
  x: number
  y: number
}

export interface TopologyEdge {
  source: TopologyNode
  target: TopologyNode
}

export interface ApplicationTopology {
  nodes: TopologyNode[]
  edges: TopologyEdge[]
  layers: number[]
  width: number
  height: number
  entryLayer: number
}

export function buildApplicationTopology(components: ApplicationComponent[]): ApplicationTopology | null {
  if (!components.length || components.some(component => !Number.isInteger(component.index))) return null
  const minimum = Math.min(...components.map(component => component.index as number))
  const entryLayer = minimum - 1
  const effectiveLayer = (component: ApplicationComponent) => component.traits?.includes('traffic-entrypoint') ? entryLayer : component.index as number
  const layers = [...new Set(components.map(effectiveLayer))].sort((left, right) => left - right)
  const columns = new Map<number, ApplicationComponent[]>()
  for (const layer of layers) columns.set(layer, components.filter(component => effectiveLayer(component) === layer).sort((left, right) => left.id.localeCompare(right.id)))
  const maximumRows = Math.max(...[...columns.values()].map(items => items.length))
  const contentHeight = maximumRows * nodeHeight + Math.max(0, maximumRows - 1) * rowGap
  const height = Math.max(260, headerHeight + contentHeight + footerHeight)
  const width = marginX * 2 + layers.length * nodeWidth + Math.max(0, layers.length - 1) * columnGap
  const nodes: TopologyNode[] = []
  layers.forEach((layer, column) => {
    const items = columns.get(layer) ?? []
    const columnHeight = items.length * nodeHeight + Math.max(0, items.length - 1) * rowGap
    const startY = headerHeight + (contentHeight - columnHeight) / 2
    items.forEach((component, row) => nodes.push({component, layer, x: marginX + column * (nodeWidth + columnGap), y: startY + row * (nodeHeight + rowGap)}))
  })
  const byID = new Map(nodes.map(node => [node.component.id, node]))
  const edges: TopologyEdge[] = []
  for (const source of nodes) for (const dependency of source.component.dependencies ?? []) {
    const target = byID.get(dependency)
    if (target) edges.push({source, target})
  }
  return {nodes, edges, layers, width, height, entryLayer}
}

export function ApplicationTopologyGraph({components}: {components: ApplicationComponent[]}) {
  const topology = buildApplicationTopology(components)
  const [exportError, setExportError] = useState('')
  if (!topology) return null
  const chartID = 'application-topology-graph'
  const output = (format: 'pdf' | 'png') => exportChart(chartID, 'application-topology', format).then(() => setExportError('')).catch(cause => setExportError(cause instanceof Error ? cause.message : 'Could not export application topology.'))
  const path = ({source, target}: TopologyEdge) => {
    const startX = source.x + 30
    const startY = source.y + nodeHeight / 2
    const endX = target.x + 6
    const endY = target.y + nodeHeight / 2
    const bend = Math.max(38, (endX - startX) / 2)
    return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`
  }
  return <section className="details-section application-topology-section">
    <div className="details-heading"><div><strong>Application topology</strong><span>Traffic flow and scheduling index</span></div><div className="chart-export-actions"><button type="button" onClick={() => void output('pdf')}>PDF</button><button type="button" onClick={() => void output('png')}>PNG 600 DPI</button></div></div>
    {exportError && <p className="form-error">{exportError}</p>}
    <div className="application-topology-scroll">
      <svg id={chartID} className="application-topology-graph" width={topology.width} height={topology.height} viewBox={`0 0 ${topology.width} ${topology.height}`} role="img" aria-label="Application component topology">
        <title>Application component topology ordered by scheduling index</title>
        <rect width={topology.width} height={topology.height} fill="#ffffff" />
        {topology.layers.map((layer, position) => <g key={layer}><text className="application-topology-layer" x={marginX + position * (nodeWidth + columnGap) + nodeWidth / 2} y={25} textAnchor="middle" fill="#626b78" fontSize="11" fontWeight="700" fontFamily="Manrope, sans-serif">{layer === topology.entryLayer ? 'Traffic entry' : `Index ${layer}`}</text><line className="application-topology-guide" x1={marginX + position * (nodeWidth + columnGap) + nodeWidth / 2} x2={marginX + position * (nodeWidth + columnGap) + nodeWidth / 2} y1={34} y2={topology.height - 12} stroke="#e5e9ef" strokeWidth="1" strokeDasharray="4 5" /></g>)}
        <g className="application-topology-edges" fill="none" stroke="#718096" strokeWidth="1.7">{topology.edges.map(edge => {
          const endX = edge.target.x + 6
          const endY = edge.target.y + nodeHeight / 2
          return <g key={`${edge.source.component.id}-${edge.target.component.id}`}><path d={path(edge)} /><polygon points={`${endX - 8},${endY - 4} ${endX},${endY} ${endX - 8},${endY + 4}`} fill="#718096" stroke="none" /></g>
        })}</g>
        <g>{topology.nodes.map(node => {
          const entrypoint = node.component.traits?.includes('traffic-entrypoint')
          return <g className={`application-topology-node${entrypoint ? ' entrypoint' : ''}`} key={node.component.id}><title>{`${node.component.id} · index ${node.component.index}`}</title><circle cx={node.x + 18} cy={node.y + nodeHeight / 2} r="12" fill={entrypoint ? '#59616b' : '#9198a1'} stroke="#ffffff" strokeWidth="2.5" /><text className="application-topology-name" x={node.x + 39} y={node.y + nodeHeight / 2 + 4} fill="#303842" fontSize="12" fontWeight="700" fontFamily="Manrope, sans-serif">{node.component.id}</text></g>
        })}</g>
      </svg>
    </div>
    <div className="application-topology-legend"><span><i className="entrypoint" />Node proxy</span><span><i />Application service</span><span>Arrows show declared downstream dependencies</span></div>
  </section>
}
