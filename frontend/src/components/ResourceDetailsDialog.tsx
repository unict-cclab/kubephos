import {useState} from 'react'
import {request} from '../api'
import {formatDate} from '../lib'
import type {Connection, ManagedResource, Pipeline, PipelineRun, Session, Workspace} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {InputData} from './InputData'
import {ExecutionProgressGraph} from './ExecutionProgressGraph'

interface Props {
  resource: ManagedResource | null
  close: () => void
  connections: Connection[]
  workspaces: Workspace[]
  resources: ManagedResource[]
  runs: PipelineRun[]
  pipelines?: Pipeline[]
  session: Session
  openOperation: (id: string) => void
  recreate?: () => Promise<void>
  recreation?: {pending: boolean; message: string; error: boolean}
}

export function ResourceDetailsPage({resource, close, connections, workspaces, resources, runs, pipelines, session, openOperation, recreate, recreation}: Props) {
  const [tab, setTab] = useState<'overview' | 'access' | 'activity' | 'configuration'>(['pending', 'provisioning', 'recreating', 'deleting'].includes(resource?.status ?? '') ? 'activity' : 'overview')
  if (!resource) return null
  const workspace = workspaces.find(item => item.id === resource.workspaceId)?.name ?? resource.workspaceId
  const connection = connections.find(item => item.id === resource.connectionId)?.name ?? resource.connectionId ?? '—'
  const dependencies = dependencyEntries(resource).map(item => ({...item, name: resources.find(candidate => candidate.id === item.id)?.name ?? item.id}))
  const references = Object.fromEntries([...resources.map(item => [item.id, item.name]), ...connections.map(item => [item.id, item.name]), ...workspaces.map(item => [item.id, item.name])])
  const run = runs.find(item => item.id === (resource.recreationPipelineRunId ?? resource.deletionPipelineRunId ?? resource.pipelineRunId))
  const pipeline = pipelines?.find(item => item.id === run?.pipelineId)
  const progressNodes = run?.stages.map((stage, index) => {
    const definition = pipeline?.definition.stages.find(item => item.id === stage.stageId)
    const dependencies = [...new Set(definition?.bindings?.map(binding => pipeline?.definition.stages.find(item => item.id === binding.fromStage)?.title ?? binding.fromStage) ?? (index ? [run.stages[index - 1].title] : []))]
    return {id: stage.id, title: stage.title, status: stage.status, detail: stage.error ?? (stage.cleanupError ? `Cleanup: ${stage.cleanupError}` : undefined), dependencies, operationId: stage.operationId}
  }) ?? []
  const endpoints = managedEndpoints(resource)
  const cluster = resource.kind === 'kubernetes-cluster'
  return <div className="detail-page"><DetailBreadcrumb parent={cluster ? 'Kubernetes' : 'Infrastructure'} current={resource.name} back={close} /><div className="detail-page-heading"><div><h2>{resource.name}</h2><p>{resourceLabel(resource.kind)} · {workspace}</p></div><div className="detail-page-actions">{recreate && session.user?.role === 'admin' && <button className="button secondary" disabled={recreation?.pending || resource.status !== 'ready' && resource.status !== 'recreation-failed'} onClick={() => void recreate()}>{recreation?.pending ? 'Recreating…' : 'Recreate cluster'}</button>}<span className={`status-pill ${resource.status}`}>{resource.status}</span></div></div>{recreation?.message && <p className={recreation.error ? 'managed-error' : 'managed-feedback'}>{recreation.message}</p>}<nav className="detail-tabs" aria-label="Resource details"><button className={tab === 'overview' ? 'active' : ''} aria-current={tab === 'overview' ? 'page' : undefined} onClick={() => setTab('overview')}>Overview</button><button className={tab === 'access' ? 'active' : ''} aria-current={tab === 'access' ? 'page' : undefined} onClick={() => setTab('access')}>Access</button><button className={tab === 'activity' ? 'active' : ''} aria-current={tab === 'activity' ? 'page' : undefined} onClick={() => setTab('activity')}>Activity</button><button className={tab === 'configuration' ? 'active' : ''} aria-current={tab === 'configuration' ? 'page' : undefined} onClick={() => setTab('configuration')}>Configuration</button></nav><article className={`detail-page-card ${cluster ? 'cluster-detail-card' : ''}`}>
    {tab === 'overview' && <><div className="details-summary">
      <div><span>Status</span><strong>{resource.status}</strong></div>
      <div><span>Type</span><strong>{resourceLabel(resource.kind)}</strong></div>
      <div><span>Environment</span><strong>{workspace}</strong></div>
      <div><span>Proxmox connection</span><strong>{connection}</strong></div>
      <div><span>Created</span><strong>{formatDate(resource.createdAt)}</strong></div>
      <div><span>Updated</span><strong>{formatDate(resource.updatedAt)}</strong></div>
    </div>
    {dependencies.length > 0 && <section className="details-section"><div className="details-heading"><strong>Dependencies</strong></div><div className="details-dependencies">{dependencies.map(item => <div key={`${item.relation}-${item.id}`}><span>{item.relation}</span><strong>{item.name}</strong></div>)}</div></section>}
    {cluster && <section className="details-section"><div className="details-heading"><strong>Topology</strong></div><Topology resource={resource} /></section>}
    {endpoints.length > 0 && <section className="details-section"><div className="details-heading"><strong>Dashboards</strong></div><div className="details-endpoints">{endpoints.map(endpoint => <a key={endpoint.name} href={endpoint.url} target="_blank" rel="noreferrer"><span>{endpoint.name}</span><strong>{endpoint.label}</strong></a>)}</div></section>}</>}
    {tab === 'access' && <>{resource.kind === 'kubernetes-cluster' && resource.status === 'ready' && <section className="details-section"><div className="details-heading"><strong>Kubernetes access</strong></div><a className="button secondary compact" href={`/api/v1/kubernetes-clusters/${resource.id}/kubeconfig`}>Download kubeconfig</a></section>}<AccessPanel key={resource.id} resource={resource} session={session} /></>}
    {tab === 'activity' && <>
    <section className="details-section"><div className="details-heading"><strong>Validation</strong><span>{resource.validation.valid ? 'Passed' : 'Needs attention'}</span></div><div className="details-gates">{resource.validation.issues?.length ? resource.validation.issues.map((issue, index) => <div key={`${issue.path ?? ''}-${index}`} className={issue.level}><span>{issue.level}</span><p>{issue.message}</p></div>) : <p>No issues</p>}</div></section>
    {(run || resource.deletionOperationId || resource.operationId) && <section className="details-section"><div className="details-heading"><strong>Lifecycle</strong><button className="text-button" onClick={() => {
      const operation = [...(run?.stages ?? [])].reverse().find(stage => stage.operationId)?.operationId ?? resource.deletionOperationId ?? resource.operationId
      if (operation) openOperation(operation)
    }}>Open latest logs</button></div>{run && <ExecutionProgressGraph nodes={progressNodes} label={`${resource.name} lifecycle progress`} openOperation={openOperation} />}</section>}
    </>}
    {tab === 'configuration' && (cluster ? <ClusterConfiguration spec={resource.spec} references={references} /> : <section className="details-section"><div className="details-heading"><strong>Resource settings</strong></div><InputData value={resource.spec} references={references} /></section>)}
  </article></div>
}

type AccessIdentity = {name: string; role: string; username: string; password: string}
type AccessEndpoint = {name: string; url?: string; service?: string; credentials: AccessIdentity[]}

function AccessPanel({resource, session}: {resource: ManagedResource; session: Session}) {
  const [items, setItems] = useState<AccessEndpoint[] | null>(null)
  const [visible, setVisible] = useState<Record<string, boolean>>({})
  const [copied, setCopied] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const reveal = async () => {
    setPending(true)
    setError('')
    try {
      const response = await request<{items: AccessEndpoint[]}>(`/managed-resources/${resource.id}/access`, {method: 'POST', body: '{}'}, session.csrfToken)
      setItems(response.items)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not reveal managed access.')
    } finally {
      setPending(false)
    }
  }
  const hide = () => {
    setItems(null)
    setVisible({})
    setCopied('')
    setError('')
  }
  const copyValue = async (value: string, label: string) => {
    try {
      await copy(value)
      setCopied(label)
      window.setTimeout(() => setCopied(current => current === label ? '' : current), 1800)
    } catch {
      setError('Copy was blocked by the browser. Select the value and copy it manually.')
    }
  }
  return <section className="details-section"><div className="details-heading"><strong>Access and credentials</strong>{items ? <button className="text-button" onClick={hide}>Hide credentials</button> : session.user?.role === 'admin' && <button className="button secondary compact" disabled={pending} onClick={reveal}>{pending ? 'Decrypting…' : 'Reveal access'}</button>}</div>
    {!items && !error && session.user?.role !== 'admin' && <p className="details-copy">Administrator access required.</p>}
    {error && <p className="managed-error">{error}</p>}
    {items && <div className="managed-access-list">{items.length ? items.map((item, endpointIndex) => <article key={`${item.service}-${item.name}`}>
      <div className="managed-access-head"><div><strong>{item.name}</strong><span>{item.service || 'Managed endpoint'}</span></div>{item.url && <a className="button secondary compact" href={item.url} target="_blank" rel="noreferrer">Open</a>}</div>
      {item.url && <button className="copy-value" onClick={() => void copyValue(item.url!, `${item.name} URL`)}><span>URL</span><code>{item.url}</code><b>{copied === `${item.name} URL` ? 'Copied' : 'Copy'}</b></button>}
      {item.credentials.length ? <div className="managed-identities">{item.credentials.map((identity, identityIndex) => {
        const key = `${endpointIndex}-${identityIndex}`
        const usernameLabel = `${item.name} ${identity.name} username`
        const passwordLabel = `${item.name} ${identity.name} password`
        return <div key={key}><div className="managed-identity-title"><strong>{identity.name}</strong><span>{identity.role || 'access'}</span></div><button className="copy-value" onClick={() => void copyValue(identity.username, usernameLabel)}><span>Username</span><code>{identity.username}</code><b>{copied === usernameLabel ? 'Copied' : 'Copy'}</b></button><div className="secret-value"><span>Password</span><input aria-label={passwordLabel} type={visible[key] ? 'text' : 'password'} value={identity.password} readOnly /><button onClick={() => setVisible(current => ({...current, [key]: !current[key]}))}>{visible[key] ? 'Hide' : 'Show'}</button><button onClick={() => void copyValue(identity.password, passwordLabel)}>{copied === passwordLabel ? 'Copied' : 'Copy'}</button></div></div>
      })}</div> : <div className="no-login"><span>✓</span><div><strong>No login required</strong><p>This managed endpoint is configured without interactive authentication.</p></div></div>}
    </article>) : <p className="details-copy">No dashboard or managed credential has been produced by this lifecycle yet.</p>}</div>}
  </section>
}

async function copy(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const input = document.createElement('textarea')
  input.value = value
  input.style.position = 'fixed'
  input.style.opacity = '0'
  document.body.appendChild(input)
  input.select()
  const copied = document.execCommand('copy')
  input.remove()
  if (!copied) throw new Error('copy unavailable')
}

function dependencyEntries(resource: ManagedResource): Array<{relation: string; id: string}> {
  const fields: Array<[string, string]> = [['templateId', 'VM template'], ['harborId', 'Harbor'], ['nfsId', 'NFS']]
  return fields.flatMap(([field, relation]) => typeof resource.spec?.[field] === 'string' ? [{relation, id: resource.spec[field] as string}] : [])
}

function managedEndpoints(resource: ManagedResource): Array<{name: string; label: string; url: string}> {
  if (resource.status !== 'ready' || resource.kind !== 'kubernetes-cluster' || typeof resource.spec?.managedProfile !== 'string') return []
  const address = String(resource.spec.addressStart ?? '')
  if (!address) return []
  return [
    ['Grafana', 32000, ''],
    ['Prometheus', 32090, ''],
    ['Cluster Lens', 32088, ''],
    ['Chaos dashboard', 32300, ''],
    ['Loki API', 32099, ''],
    ['Jaeger', 30002, ''],
    ['Kiali', 30001, '/kiali']
  ].map(([name, port, path]) => ({name: String(name), label: `${address}:${port}`, url: `http://${address}:${port}${path}`}))
}

function Topology({resource}: {resource: ManagedResource}) {
  const controlPlane: Record<string, unknown> = {name: 'control-plane', role: 'Control plane', count: Number(resource.spec.controlPlanes ?? 0), zones: resource.spec.controlPlaneZones, capacity: resource.spec.controlPlaneCapacity}
  const management: Record<string, unknown> | null = resource.spec.managementPool && typeof resource.spec.managementPool === 'object' ? {...resource.spec.managementPool as Record<string, unknown>, role: 'Management'} : null
  const applications: Array<Record<string, unknown>> = Array.isArray(resource.spec.applicationPools) ? resource.spec.applicationPools.map(pool => ({...(pool as Record<string, unknown>), role: 'Application'})) : []
  const pools: Array<Record<string, unknown>> = [controlPlane, ...(management ? [management] : []), ...applications]
  return <div className="details-topology">{pools.map((pool, index) => {
    const capacity = pool.capacity && typeof pool.capacity === 'object' ? pool.capacity as Record<string, unknown> : {}
    const zones = Array.isArray(pool.zones) ? pool.zones.join(', ') : '—'
    return <article key={`${String(pool.name)}-${index}`}><div><strong>{String(pool.name)}</strong><span>{String(pool.role)} · {String(pool.count)} node(s)</span></div><dl><div><dt>Zones</dt><dd>{zones}</dd></div><div><dt>CPU</dt><dd>{String(capacity.cores ?? '—')}</dd></div><div><dt>Memory</dt><dd>{capacity.memoryMiB ? `${String(capacity.memoryMiB)} MiB` : '—'}</dd></div><div><dt>Disk</dt><dd>{capacity.diskGiB ? `${String(capacity.diskGiB)} GiB` : '—'}</dd></div></dl></article>
  })}</div>
}

function ClusterConfiguration({spec, references}: {spec: Record<string, unknown>; references: Record<string, string>}) {
  const grouped = new Set(['controlPlaneZones', 'controlPlaneCapacity', 'managementPool', 'applicationPools'])
  const settings = Object.fromEntries(Object.entries(spec).filter(([key]) => !grouped.has(key)))
  const controlPlane = Object.fromEntries(['controlPlaneZones', 'controlPlaneCapacity'].filter(key => key in spec).map(key => [key, spec[key]]))
  const pools = Array.isArray(spec.applicationPools) ? spec.applicationPools : []
  return <>
    <section className="details-section"><div className="details-heading"><strong>Cluster settings</strong></div><InputData value={settings} references={references} /></section>
    {Object.keys(controlPlane).length > 0 && <section className="details-section"><div className="details-heading"><strong>Control plane</strong></div><InputData value={controlPlane} references={references} /></section>}
    {'managementPool' in spec && <section className="details-section"><div className="details-heading"><strong>Management pool</strong></div><InputData value={spec.managementPool} references={references} /></section>}
    {'applicationPools' in spec && <section className="details-section"><div className="details-heading"><strong>Application pools</strong></div>{pools.length ? <div className="cluster-configuration-pools">{pools.map((pool, index) => <article key={index}><strong>{typeof pool === 'object' && pool !== null && 'name' in pool ? String(pool.name) : `Pool ${index + 1}`}</strong><InputData value={pool} references={references} /></article>)}</div> : <p className="details-copy">None</p>}</section>}
  </>
}

function resourceLabel(kind: string): string {
  return ({'machine-template': 'Proxmox VM template', harbor: 'Harbor registry', nfs: 'NFS server', 'kubernetes-cluster': 'Kubernetes cluster'} as Record<string, string>)[kind] ?? kind
}
