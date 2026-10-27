import {ReadoutTable} from './ReadoutTable'
import {ClusterMaintenance} from './ClusterMaintenance'
import {ResourceAttribution} from './ResourceAttribution'
import {ActionIcon} from './ActionIcon'
import {useState} from 'react'
import {request} from '../api'
import type {Connection, ManagedResource, Pipeline, PipelineRun, Session, Workspace} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {InputData, hasInputData} from './InputData'
import {ExecutionProgressGraph} from './ExecutionProgressGraph'
import {KubernetesLogo} from './KubernetesLogo'
import {ClusterDashboards} from './ClusterDashboards'

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
  openPipelineRun?: (id: string) => void
  recreate?: () => Promise<void>
  recreation?: {pending: boolean; message: string; error: boolean}
}

export function ResourceDetailsPage({resource, close, connections, workspaces, resources, runs, pipelines, session, openPipelineRun, recreate, recreation}: Props) {
  const [tab, setTab] = useState<'overview' | 'access' | 'activity' | 'configuration'>(['pending', 'provisioning', 'recreating', 'maintaining', 'deleting'].includes(resource?.status ?? '') ? 'activity' : 'overview')
  if (!resource) return null
  if (['machine-template', 'harbor', 'nfs'].includes(resource.kind)) return <SimpleResourceDetails resource={resource} connections={connections} resources={resources} close={close} />
  const connection = connections.find(item => item.id === resource.connectionId)?.name ?? resource.connectionId ?? '—'
  const dependencies = dependencyEntries(resource).map(item => ({...item, name: resources.find(candidate => candidate.id === item.id)?.name ?? item.id}))
  const references = Object.fromEntries([...resources.map(item => [item.id, item.name]), ...connections.map(item => [item.id, item.name]), ...workspaces.map(item => [item.id, item.name])])
  const run = runs.find(item => item.id === (resource.recreationPipelineRunId ?? resource.deletionPipelineRunId ?? resource.pipelineRunId))
  const pipeline = pipelines?.find(item => item.id === run?.pipelineId)
  const progressNodes = run?.stages.map((stage, index) => {
    const definition = pipeline?.definition.stages.find(item => item.id === stage.stageId)
    const dependencies = [...new Set(definition?.bindings?.map(binding => pipeline?.definition.stages.find(item => item.id === binding.fromStage)?.title ?? binding.fromStage) ?? (index ? [run.stages[index - 1].title] : []))]
    return {id: stage.id, title: stage.title, status: stage.status, detail: stage.error || stage.cleanupError || undefined, dependencies, operationId: stage.operationId}
  }) ?? []
  const cluster = resource.kind === 'kubernetes-cluster'
  const retry = ['failed', 'recreation-failed', 'maintenance-failed'].includes(resource.status)
  const completedStages = run?.stages.filter(stage => stage.status === 'succeeded').length ?? 0
  return <div className="detail-page"><DetailBreadcrumb parent={cluster ? 'Kubernetes' : 'Infrastructure'} current={resource.name} back={close} /><div className="detail-page-heading"><div className={cluster ? 'cluster-detail-title' : undefined}>{cluster && <KubernetesLogo />}<div><h2>{resource.name}</h2>{!cluster && <p>{resourceLabel(resource.kind)}</p>}</div></div><div className="detail-page-actions">{cluster && resource.spec.mode !== 'multi' && <ClusterMaintenance key={resource.id} resource={resource} runs={runs} session={session} />}{recreate && session.user?.role === 'admin' && <button className="button secondary" disabled={recreation?.pending || resource.status !== 'ready' && !retry} onClick={() => void recreate()}>{recreation?.pending ? (retry ? 'Retrying…' : 'Recreating…') : retry ? 'Retry' : 'Recreate cluster'}</button>}<span className={`status-pill ${resource.status}`}>{resource.status}</span></div></div>{recreation?.message && <p className={recreation.error ? 'managed-error' : 'managed-feedback'}>{recreation.message}</p>}<ResourceAttribution resource={resource} editable={cluster} />{dependencies.length > 0 && <div className="cluster-resource-summary"><ReadoutTable label="Cluster resources" data={{headers: dependencies.map(item => item.relation), rows: [dependencies.map(item => item.name)]}} /></div>}<nav className="detail-tabs" aria-label="Resource details"><button className={tab === 'overview' ? 'active' : ''} aria-current={tab === 'overview' ? 'page' : undefined} onClick={() => setTab('overview')}>Overview</button><button className={tab === 'access' ? 'active' : ''} aria-current={tab === 'access' ? 'page' : undefined} onClick={() => setTab('access')}>Access</button><button className={tab === 'activity' ? 'active' : ''} aria-current={tab === 'activity' ? 'page' : undefined} onClick={() => setTab('activity')}>Activity</button><button className={tab === 'configuration' ? 'active' : ''} aria-current={tab === 'configuration' ? 'page' : undefined} onClick={() => setTab('configuration')}>Configuration</button></nav><article className={`detail-page-card ${cluster ? 'cluster-detail-card' : ''}`}>
    {tab === 'overview' && <><div className={`details-summary${cluster ? ' cluster-summary' : ''}`}>
      {!cluster && <div><span>Type</span><strong>{resourceLabel(resource.kind)}</strong></div>}
      {cluster && <><div><span>Prometheus scraping period</span><strong>{String(resource.spec.scrapeInterval ?? '—')}</strong></div><div><span>Metrics retention</span><strong>{String(resource.spec.retention ?? '—')}</strong></div></>}
      <div><span>Proxmox connection</span><strong>{connection}</strong></div>


    </div>

    {cluster && <section className="details-section"><div className="details-heading"><strong>Topology</strong></div><Topology resource={resource} /></section>}
    <ClusterDashboards key={resource.id} resource={resource} /></>}
    {tab === 'access' && <>{resource.kind === 'kubernetes-cluster' && resource.status === 'ready' && <section className="details-section"><div className="details-heading"><strong>Kubernetes access</strong></div><a className="button secondary compact" href={`/api/v1/kubernetes-clusters/${resource.id}/kubeconfig`}>Download kubeconfig</a></section>}<AccessPanel key={resource.id} resource={resource} session={session} /></>}
    {tab === 'activity' && <>
    <section className="details-section activity-status"><div><span>Status</span><strong className={`status-pill ${resource.status}`}>{resource.status}</strong></div>{run && <div><span>Progress</span><strong>{completedStages}/{run.stages.length}</strong></div>}</section>
    {run && <section className="details-section"><ExecutionProgressGraph nodes={progressNodes} label={`${resource.name} lifecycle progress`} showLogs={openPipelineRun ? () => openPipelineRun(run.id) : undefined} /></section>}
    </>}
    {tab === 'configuration' && (cluster ? <ClusterConfiguration spec={resource.spec} references={references} /> : hasInputData(resource.spec) && <section className="details-section"><div className="details-heading"><strong>Resource settings</strong></div><InputData value={resource.spec} references={references} /></section>)}
  </article></div>
}

function SimpleResourceDetails({resource, connections, resources, close}: {resource: ManagedResource; connections: Connection[]; resources: ManagedResource[]; close: () => void}) {
  const references = Object.fromEntries([...connections.map(item => [item.id, item.name]), ...resources.map(item => [item.id, item.name])])
  const connectionId = resource.connectionId ?? resource.spec.connectionId
  const fields = resource.kind === 'machine-template'
    ? Object.fromEntries([['name', resource.name], ['connectionId', connectionId], ...Object.entries(resource.spec).filter(([name]) => !['name', 'connectionRef'].includes(name))])
    : Object.fromEntries(['name', 'connectionId', 'templateId', 'vmid', 'address', 'prefixLength', 'gateway', 'dnsServer', 'cores', 'memoryMiB', 'diskGiB'].flatMap(name => {
        if (name === 'name') return [[name, resource.name]]
        if (name === 'connectionId') return [[name, connectionId]]
        return name in resource.spec ? [[name, resource.spec[name]]] : []
      }))
  return <div className="detail-page"><DetailBreadcrumb parent="Infrastructure" current={resource.name} back={close} /><div className="detail-page-heading"><div><h2>{resource.name}</h2></div><span className={`status-pill ${resource.status}`}>{resource.status}</span></div><ResourceAttribution resource={resource} editable /><article className="detail-page-card simple-resource-detail"><InputData value={fields} references={references} /></article></div>
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
    {!items && !error && session.user?.role !== 'admin' && <p className="details-copy">Administrator access required</p>}
    {error && <p className="managed-error">{error}</p>}
    {items && <div className="managed-access-list">{items.length ? items.map((item, endpointIndex) => {
      const credentials = Array.isArray(item.credentials) ? item.credentials : []
      return <article key={`${item.service}-${item.name}`}>
      <div className="managed-access-head"><div><strong>{item.name}</strong><span>{item.service || 'Managed endpoint'}</span></div>{item.url && <a className="button secondary compact" href={item.url} target="_blank" rel="noreferrer">Open</a>}</div>
      {item.url && <button className="copy-value" onClick={() => void copyValue(item.url!, `${item.name} URL`)}><span>URL</span><code>{item.url}</code><b>{copied === `${item.name} URL` ? 'Copied' : 'Copy'}</b></button>}
      {credentials.length ? <div className="managed-identities">{credentials.map((identity, identityIndex) => {
        const key = `${endpointIndex}-${identityIndex}`
        const usernameLabel = `${item.name} ${identity.name} username`
        const passwordLabel = `${item.name} ${identity.name} password`
        return <div key={key}><div className="managed-identity-title"><strong>{identity.name}</strong><span>{identity.role || 'access'}</span></div><button className="copy-value" onClick={() => void copyValue(identity.username, usernameLabel)}><span>Username</span><code>{identity.username}</code><b>{copied === usernameLabel ? 'Copied' : 'Copy'}</b></button><div className="secret-value"><span>Password</span><input aria-label={passwordLabel} type={visible[key] ? 'text' : 'password'} value={identity.password} readOnly /><button className="text-button" onClick={() => setVisible(current => ({...current, [key]: !current[key]}))}>{visible[key] ? 'Hide' : 'Show'}</button><button className="text-button" onClick={() => void copyValue(identity.password, passwordLabel)}>{copied === passwordLabel ? 'Copied' : 'Copy'}</button></div></div>
      })}</div> : <div className="no-login"><span><ActionIcon name="check" /></span><strong>No login required</strong></div>}
    </article>}) : <p className="details-copy">No access available</p>}</div>}
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

function Topology({resource}: {resource: ManagedResource}) {
 if (resource.spec.mode === 'multi' && Array.isArray(resource.spec.members)) return <>{resource.spec.members.map((member,index) => member && typeof member === 'object' && <section className="details-section" key={index}><div className="details-heading"><strong>{String(member.name)}</strong></div><Topology resource={{...resource,spec:member}} /></section>)}</>
  const controlPlane: Record<string, unknown> = {name: 'control-plane', role: 'Control plane', count: Number(resource.spec.controlPlanes ?? 0), zones: resource.spec.controlPlaneZones, capacity: resource.spec.controlPlaneCapacity}
  const management: Record<string, unknown> | null = resource.spec.managementPool && typeof resource.spec.managementPool === 'object' ? {...resource.spec.managementPool as Record<string, unknown>, role: 'Management'} : null
  const applications: Array<Record<string, unknown>> = Array.isArray(resource.spec.applicationPools) ? resource.spec.applicationPools.map(pool => ({...(pool as Record<string, unknown>), role: 'Application'})) : []
  const pools: Array<Record<string, unknown>> = [controlPlane, ...(management ? [management] : []), ...applications]
  const showZones = resource.spec.networking !== 'cilium'
  return <ReadoutTable label="Cluster topology" data={{headers: ['Pool', 'Role', 'Nodes', ...(showZones ? ['Zones'] : []), 'CPU (cores)', 'Memory (MiB)', 'Disk (GiB)'], rows: pools.map(pool => {
    const capacity = pool.capacity && typeof pool.capacity === 'object' ? pool.capacity as Record<string, unknown> : {}
    const zones = Array.isArray(pool.zones) && pool.zones.length ? pool.zones.join(', ') : '—'
    return [String(pool.name), String(pool.role), String(pool.count), ...(showZones ? [zones] : []), ...['cores', 'memoryMiB', 'diskGiB'].map(key => String(capacity[key] ?? '—'))]
  })}} />
}

function ClusterConfiguration({spec, references}: {spec: Record<string, unknown>; references: Record<string, string>}) {
 if (spec.mode === 'multi' && Array.isArray(spec.members)) return <>{spec.members.map((member,index) => member && typeof member === 'object' && <section className="details-section" key={index}><div className="details-heading"><strong>{String(member.name)}</strong></div><ClusterConfiguration spec={member} references={references} /></section>)}</>
  const grouped = new Set(['machineNodes', 'controlPlaneZones', 'controlPlaneCapacity', 'managementPool', 'applicationPools', 'scrapeInterval', 'retention', 'mode', 'members'])
  const settings = Object.fromEntries(Object.entries(spec).filter(([key]) => !grouped.has(key)))
  const controlPlane = Object.fromEntries((spec.networking === 'cilium' ? ['controlPlaneCapacity'] : ['controlPlaneZones', 'controlPlaneCapacity']).filter(key => key in spec).map(key => [key, spec[key]]))
  const poolSettings = (value: unknown) => spec.networking === 'cilium' && value && typeof value === 'object' ? Object.fromEntries(Object.entries(value).filter(([key]) => key !== 'zones')) : value
  const pools = Array.isArray(spec.applicationPools) ? spec.applicationPools.map(poolSettings) : []
  return <>
    {hasInputData(settings) && <section className="details-section"><div className="details-heading"><strong>Cluster settings</strong></div><InputData value={settings} references={references} /></section>}
    {hasInputData([spec.scrapeInterval, spec.retention]) && <section className="details-section"><div className="details-heading"><strong>Prometheus</strong></div><InputData value={{scrapeInterval: spec.scrapeInterval, retention: spec.retention}} /></section>}
    {hasInputData(controlPlane) && <section className="details-section"><div className="details-heading"><strong>Control plane</strong></div><InputData value={controlPlane} references={references} /></section>}
    {hasInputData(spec.machineNodes) && <section className="details-section"><div className="details-heading"><strong>VM placement</strong></div><div className="resource-list-table-wrap"><table className="resource-list-table" aria-label="VM placement"><thead><tr><th scope="col">VM name</th><th scope="col">Proxmox server</th></tr></thead><tbody>{Object.entries(spec.machineNodes as Record<string, unknown>).sort(([left], [right]) => left.localeCompare(right, undefined, {numeric: true})).map(([name, node]) => <tr key={name}><td><code>{name}</code></td><td>{typeof node === 'string' ? node : '—'}</td></tr>)}</tbody></table></div></section>}
    {hasInputData(spec.managementPool) && <section className="details-section"><div className="details-heading"><strong>Management pool</strong></div><InputData value={poolSettings(spec.managementPool)} references={references} /></section>}
    {hasInputData(spec.applicationPools) && <section className="details-section"><div className="details-heading"><strong>Pools</strong></div>{pools.length ? <div className="cluster-configuration-pools">{pools.map((pool, index) => <article key={index}><strong>{typeof pool === 'object' && pool !== null && 'name' in pool ? String(pool.name) : `Pool ${index + 1}`}</strong><InputData value={pool} references={references} /></article>)}</div> : <p className="details-copy">None</p>}</section>}
  </>
}

function resourceLabel(kind: string): string {
  return ({'machine-template': 'Proxmox VM template', harbor: 'Harbor registry', nfs: 'NFS server', 'kubernetes-cluster': 'Kubernetes cluster'} as Record<string, string>)[kind] ?? kind
}
