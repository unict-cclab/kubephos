import {useState} from 'react'
import type {ManagedResource, Session} from '../types'
import {clusterHasComponent, useClusterComponentCatalog, type ClusterComponentCatalog} from '../clusterComponents'

type Endpoint = {name: string; address: string; url: string}

// Each installed component lists its dashboards in the catalog; Hubble belongs to Cilium networking.
function endpoints(spec: Record<string, unknown>, catalog?: ClusterComponentCatalog): Endpoint[] {
  if (typeof spec.managedProfile !== 'string' || typeof spec.addressStart !== 'string' || !spec.addressStart || !catalog) return []
  const dashboards = catalog.items.filter(component => clusterHasComponent(spec, component.id)).flatMap(component => component.dashboards ?? [])
  if (spec.networking === 'cilium') dashboards.push({name: 'Hubble', port: 31235})
  return dashboards.map(({name, port, path = ''}) => ({name, address: `${spec.addressStart}:${port}`, url: `http://${spec.addressStart}:${port}${path}`}))
}

function ExternalMark() {
  return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M14 3h7v7m0-7L10 14M10 3H3v18h18v-7" /></svg>
}

function EndpointLink({endpoint}: {endpoint: Endpoint}) {
  return <a className="dashboard-link" href={endpoint.url} target="_blank" rel="noreferrer"><span><strong>{endpoint.name}</strong><span>{endpoint.address}</span></span><ExternalMark /></a>
}

export function ClusterDashboards({resource}: {resource: ManagedResource; session?: Session}) {
  const [workloadCluster, setWorkloadCluster] = useState('')
  const {catalog} = useClusterComponentCatalog()
  if (resource.kind !== 'kubernetes-cluster' || resource.status !== 'ready') return null
  const multi = resource.spec.mode === 'multi'
  const members = multi && Array.isArray(resource.spec.members)
    ? resource.spec.members.filter((member): member is Record<string, unknown> => !!member && typeof member === 'object').map(member => ({name: String(member.name), links: endpoints(member, catalog)}))
    : [{name: resource.name, links: endpoints(resource.spec, catalog)}]
  const groups = members.filter(member => member.links.length > 0)
  if (!groups.length) return null
  const lensSources = multi ? groups.filter(group => group.links.some(link => link.name === 'Cluster Lens')) : []
  const selected = lensSources.find(group => group.name === workloadCluster) ?? lensSources[0]
  const lens = selected?.links.find(link => link.name === 'Cluster Lens')
  return <section className="details-section cluster-dashboards"><div className="details-heading"><strong>Dashboards</strong></div>
    {lens && <section className="mesh-dashboard" aria-label="Cluster Lens">
      <div className="mesh-dashboard-title"><strong>Cluster Lens</strong><span>Network · All clusters</span></div>
      <label>Workload cluster<select value={selected.name} onChange={event => setWorkloadCluster(event.target.value)}>{lensSources.map(group => <option key={group.name} value={group.name}>{group.name}</option>)}</select></label>
      <a className="button secondary compact" href={lens.url} target="_blank" rel="noreferrer" aria-label="Open Cluster Lens">Open<ExternalMark /></a>
    </section>}
    <div className={multi ? 'dashboard-clusters' : ''}>{groups.map(group => <section key={group.name} className="dashboard-cluster" aria-label={multi ? `${group.name} dashboards` : 'Cluster dashboards'}>
      {multi && <h3>{group.name}</h3>}
      <div className={multi ? 'dashboard-links' : 'dashboard-links dashboard-links-single'}>{group.links.filter(link => !multi || link.name !== 'Cluster Lens').map(endpoint => <EndpointLink key={endpoint.name} endpoint={endpoint} />)}</div>
    </section>)}</div>
  </section>
}
