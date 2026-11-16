import {useState} from 'react'
import type {ManagedResource, Session} from '../types'
import {openLiveDashboard} from '../liveDashboard'

type Endpoint = {name: string; address: string; url: string}

function endpoints(spec: Record<string, unknown>): Endpoint[] {
  if (typeof spec.managedProfile !== 'string' || typeof spec.addressStart !== 'string' || !spec.addressStart) return []
  const cilium = spec.networking === 'cilium'
  const services: Array<[string, number, string?]> = [
    ['Grafana', 32000], ['Prometheus', 32090], ['Cluster Lens', 32088],
    ['Chaos dashboard', 32300], ['Loki API', 32099], ['Jaeger', 30002],
    ...(cilium ? [] : [['Kiali', 30001, '/kiali'] as [string, number, string]]),
    ['Headlamp', 30003], ...(cilium ? [['Hubble', 31235] as [string, number]] : [])
  ]
  return services.map(([name, port, path = '']) => ({name, address: `${spec.addressStart}:${port}`, url: `http://${spec.addressStart}:${port}${path}`}))
}

function ExternalMark() {
  return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M14 3h7v7m0-7L10 14M10 3H3v18h18v-7" /></svg>
}

function EndpointLink({endpoint}: {endpoint: Endpoint}) {
  return <a className="dashboard-link" href={endpoint.url} target="_blank" rel="noreferrer"><span><strong>{endpoint.name}</strong><span>{endpoint.address}</span></span><ExternalMark /></a>
}

// The live experiment dashboard is installed in Grafana on demand, then opened
// on the namespace currently receiving load. It serves single clusters.
function LiveDashboardLink({resource, session, address}: {resource: ManagedResource; session?: Session; address: string}) {
  const [opening, setOpening] = useState(false)
  const [error, setError] = useState('')
  const open = async () => {
    setOpening(true)
    setError('')
    try {
      await openLiveDashboard(`/kubernetes-clusters/${resource.id}/live-dashboard`, session?.csrfToken)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not open the live dashboard.')
    } finally {
      setOpening(false)
    }
  }
  return <>
    <button type="button" className="dashboard-link" disabled={opening} onClick={() => void open()}><span><strong>Live experiment</strong><span>{opening ? 'Preparing the dashboard…' : `${address} · Grafana`}</span></span><ExternalMark /></button>
    {error && <p className="dashboard-link-error" role="alert">{error}</p>}
  </>
}

export function ClusterDashboards({resource, session}: {resource: ManagedResource; session?: Session}) {
  const [workloadCluster, setWorkloadCluster] = useState('')
  if (resource.kind !== 'kubernetes-cluster' || resource.status !== 'ready') return null
  const multi = resource.spec.mode === 'multi'
  const members = multi && Array.isArray(resource.spec.members)
    ? resource.spec.members.filter((member): member is Record<string, unknown> => !!member && typeof member === 'object').map(member => ({name: String(member.name), links: endpoints(member)}))
    : [{name: resource.name, links: endpoints(resource.spec)}]
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
      <div className={multi ? 'dashboard-links' : 'dashboard-links dashboard-links-single'}>{group.links.filter(link => !multi || link.name !== 'Cluster Lens').map(endpoint => <EndpointLink key={endpoint.name} endpoint={endpoint} />)}{!multi && <LiveDashboardLink resource={resource} session={session} address={group.links[0].address} />}</div>
    </section>)}</div>
  </section>
}
