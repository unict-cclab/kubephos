import {buildApplicationTopology, edgePath} from './applicationTopology'
import type {ReactNode} from 'react'
import type {ApplicationComponent} from '../types'
import './RequestMix.css'

// Service graph of the application (catalog `dependencies` layout) annotated with
// request rates of a measured or predicted call chain.

export function componentForService(components: ApplicationComponent[], service: string): ApplicationComponent | undefined {
  return components.find(component => component.workload?.name === service) ?? components.find(component => component.id === service)
}

export function formatRate(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (value >= 1000) return `${(value / 1000).toLocaleString('en-US', {maximumFractionDigits: value >= 10000 ? 0 : 1})}k`
  return value.toLocaleString('en-US', {maximumFractionDigits: value >= 100 ? 0 : value >= 10 ? 1 : 2})
}

export interface NodeRate {rps: number; perReplica?: number; secondary?: string}

// `actions` (e.g. figure export) is supplied by the caller so this module stays light.
export function CallChainGraph({components, rates, edges = [], chartID, heading, label, actions}: {actions?: ReactNode; components: ApplicationComponent[]; rates?: Record<string, NodeRate>; edges?: Array<{source: string; destination: string; rps: number}>; chartID: string; heading: string; label: string}) {
  const topology = buildApplicationTopology(components)
  if (!topology) return null
  const height = topology.height + (rates ? 22 : 0)
  const nodeByService = (service: string) => {
    const component = componentForService(components, service)
    return component && topology.nodes.find(node => node.component.id === component.id)
  }
  const edgeLabels = edges.flatMap(edge => {
    const source = nodeByService(edge.source), target = nodeByService(edge.destination)
    return source && target && source !== target ? [{key: `${edge.source}>${edge.destination}`, x: (source.x + target.x) / 2, y: (source.y + target.y) / 2, text: `${formatRate(edge.rps)}/s`}] : []
  })
  return <figure className="call-chain-figure">
    <div className="details-heading"><strong>{heading}</strong>{actions}</div>
    <div className="application-topology-scroll">
      <svg id={chartID} className="application-topology-graph" data-export-size="content" width={topology.width} height={height} viewBox={`0 0 ${topology.width} ${height}`} role="img" aria-label={label}>
        <rect width={topology.width} height={height} fill="#ffffff" />
        <defs><marker id={`${chartID}-arrow`} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#718096" /></marker></defs>
        <g className="application-topology-edges" fill="none" stroke="#566274" strokeWidth="2.2">{topology.edges.map((edge, index) => <path key={`${edge.source.component.id}-${edge.target.component.id}`} d={edgePath(edge, index, topology.width)} markerEnd={`url(#${chartID}-arrow)`} />)}</g>
        <g>{edgeLabels.map(item => <text key={item.key} className="call-chain-edge-label" x={item.x} y={item.y} textAnchor="middle" fill="#175cd3" fontSize="11" fontWeight="600" fontFamily="Geist Variable, Geist, sans-serif" stroke="#ffffff" strokeWidth="3" paintOrder="stroke">{item.text}</text>)}</g>
        <g>{topology.nodes.map(node => {
          const entrypoint = node.component.traits?.includes('traffic-entrypoint')
          const rate = rates?.[node.component.id]
          const text = rate ? `${formatRate(rate.rps)} req/s${rate.perReplica !== undefined ? ` · ${formatRate(rate.perReplica)}/pod` : ''}` : rates ? 'not measured' : ''
          return <g className={`application-topology-node${entrypoint ? ' entrypoint' : ''}`} key={node.component.id}><title>{`${node.component.id}${text ? `: ${text}` : ''}`}</title><circle cx={node.x} cy={node.y} r={14} fill={entrypoint ? '#59616b' : '#9198a1'} stroke="#ffffff" strokeWidth="2.5" /><rect className="application-topology-label-bg" x={node.x - node.labelWidth / 2} y={node.y + 22} width={node.labelWidth} height="25" rx="8" fill="#ffffff" stroke="#dfe4eb" strokeWidth="1" /><text className="application-topology-name" x={node.x} y={node.y + 39} textAnchor="middle" fill="#303842" fontSize="12" fontWeight="500" fontFamily="Geist Variable, Geist, sans-serif">{node.label}</text>
            {text && <text className="call-chain-node-rate" x={node.x} y={node.y + 62} textAnchor="middle" fill={rate ? '#175cd3' : '#5c5c5a'} fontSize="11" fontWeight={rate ? 600 : 400} fontFamily="Geist Variable, Geist, sans-serif">{text}</text>}</g>
        })}</g>
      </svg>
    </div>
  </figure>
}

export interface RankingRow {service: string; rps: number; replicas?: number; perReplica?: number; perExecution?: number; proxy: boolean; measured?: number}

export function ServiceRankingTable({rows, label, measured = false}: {rows: RankingRow[]; label: string; measured?: boolean}) {
  const total = rows.filter(row => !row.proxy).reduce((sum, row) => sum + row.rps, 0)
  return <div className="statistics-table-wrap"><table className="call-chain-ranking request-mix-table" aria-label={label}>
    <thead><tr><th scope="col">#</th><th scope="col">Service</th><th scope="col">Requests / s</th><th scope="col">Share</th><th scope="col">Replicas</th><th scope="col">Per replica (req/s)</th><th scope="col">Calls per task execution</th>{measured && <th scope="col">Measured (req/s)</th>}{measured && <th scope="col">Measured / predicted</th>}</tr></thead>
    <tbody>{rows.map((row, index) => <tr key={row.service}>
      <td>{index + 1}</td>
      <td>{row.service}{row.proxy && <span className="call-chain-tag" title="Forwarding hop (node proxy, gateway or waypoint): it relays requests rather than serving them">proxy</span>}</td>
      <td>{formatRate(row.rps)}</td>
      <td>{row.proxy || !(total > 0) ? '—' : `${(row.rps / total * 100).toFixed(1)}%`}</td>
      <td>{row.replicas ?? '—'}</td>
      <td>{formatRate(row.perReplica)}</td>
      <td>{row.perExecution === undefined ? '—' : row.perExecution.toLocaleString('en-US', {maximumFractionDigits: 2})}</td>
      {measured && <td>{formatRate(row.measured)}</td>}
      {measured && <td>{row.measured === undefined || !(row.rps > 0) ? '—' : `${(row.measured / row.rps * 100).toFixed(0)}%`}</td>}
    </tr>)}</tbody>
  </table></div>
}
