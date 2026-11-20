import {ChartExportActions} from './ChartExportActions'
import type {ApplicationComponent} from '../types'
import {buildApplicationTopology, edgePath, nodeRadius} from './applicationTopology'
export {buildApplicationTopology, edgePath} from './applicationTopology'
export type {ApplicationTopology, TopologyEdge, TopologyNode} from './applicationTopology'

// `heading` is omitted (null) when a surrounding panel already titles the graph.
export function ApplicationTopologyGraph({components, heading = 'Application topology'}: {components: ApplicationComponent[]; heading?: string | null}) {
  const topology = buildApplicationTopology(components)
  if (!topology) return null
  const chartID = 'application-topology-graph'
  return <section className="details-section application-topology-section">
    <div className="details-heading">{heading && <strong>{heading}</strong>}<ChartExportActions chartID={chartID} metric="application-topology" /></div>
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
