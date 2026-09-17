import {useEffect, useState} from 'react'
import {formatDate, shortID} from '../lib'
import {useDetailRoute} from '../detailRoute'
import type {Application, Artifact, AuditEvent, CatalogStrategy, Connection, Credential, Experiment, ExperimentConfiguration, InfrastructureResource, ManagedResource, Operation, Pipeline, PipelineRun, Plugin, PluginImportJob, PluginPackage, PluginRuntimeStatus, Session, SystemStatus, View, Workspace} from '../types'
import {ExperimentConfigurationsView} from './ExperimentConfigurationsView'
import {ExperimentsHistoryView} from './ExperimentsHistoryView'
import {SuitesView} from './SuitesView'
import {ResourceDetailsPage} from './ResourceDetailsDialog'
import {StrategyCatalog} from './StrategyCatalog'
import {ApplicationDetailsPage} from './ApplicationDetailsDialog'
import {ConnectionDetailsPage} from './ConnectionDetailsPage'
import {SectionIcon, type SectionIconKind} from './SectionIcon'
import {ResourceAction} from './ResourceAction'

interface ViewProps {
  view: View
  system: SystemStatus | null
  pluginRuntime: PluginRuntimeStatus | null
  workspaces: Workspace[]
  pipelines: Pipeline[]
  pipelineRuns: PipelineRun[]
  operations: Operation[]
  artifacts: Artifact[]
  experiments: Experiment[]
  experimentConfigurations: ExperimentConfiguration[]
  plugins: Plugin[]
  pluginPackages: PluginPackage[]
  pluginImports: PluginImportJob[]
  applications: Application[]
  strategies: CatalogStrategy[]
  credentials: Credential[]
  connections: Connection[]
  machineTemplates: ManagedResource[]
  infrastructureServices: ManagedResource[]
  kubernetesClusters: ManagedResource[]
  resources: InfrastructureResource[]
  audit: AuditEvent[]
  session: Session
  changed: () => Promise<void>
  isAdmin: boolean
  navigate: (view: View) => void
  createWorkspace: () => void
  createPipeline: () => void
  createOperation: (workspace: Workspace) => void
  openWorkspace: (workspace: Workspace) => void
  openOperation: (id: string) => void
  importApplication: () => void
  deleteApplication: (application: Application) => Promise<boolean>
  importPlugin: () => void
  configureRuntime: () => void
  activatePlugin: (pluginPackage: PluginPackage) => Promise<void>
  deactivatePlugin: (pluginPackage: PluginPackage) => Promise<void>
  addCredential: () => void
  addConnection: () => void
  addMachineTemplate: () => void
  addInfrastructureService: (kind: 'harbor' | 'nfs') => void
  deleteConnection: (id: string, name: string) => Promise<void>
  deleteMachineTemplate: (id: string, name: string) => Promise<void>
  deleteInfrastructureService: (id: string, name: string) => Promise<void>
  addKubernetesCluster: () => void
  deleteKubernetesCluster: (id: string, name: string) => Promise<void>
  recreateKubernetesCluster: (id: string, name: string) => Promise<void>
  clusterActions: Record<string, {pending: boolean; message: string; error: boolean}>
  openInfrastructureCapability: (workspace: Workspace, pluginID: string) => void
  openTerminal: (workspace: Workspace) => void
}

export function Views(props: ViewProps) {
  return <>
    <section className={`view ${props.view === 'overview' ? 'active' : ''}`}>
      <Overview {...props} />
    </section>
    <section className={`view ${props.view === 'results' ? 'active' : ''}`}>
      <ExperimentsHistoryView experiments={props.experiments} configurations={props.experimentConfigurations} artifacts={props.artifacts} operations={props.operations} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} />
    </section>
    <section className={`view ${props.view === 'catalog' ? 'active' : ''}`}>
      <CatalogView {...props} />
    </section>
    <section className={`view ${props.view === 'infrastructure' ? 'active' : ''}`}>
      <Infrastructure {...props} />
    </section>
    <section className={`view ${props.view === 'kubernetes' ? 'active' : ''}`}>
      <Kubernetes {...props} />
    </section>
    <section className={`view ${props.view === 'experiments' ? 'active' : ''}`}>
      <ExperimentConfigurationsView items={props.experimentConfigurations} experiments={props.experiments} artifacts={props.artifacts} operations={props.operations} clusters={props.kubernetesClusters} workspaces={props.workspaces} applications={props.applications} plugins={props.plugins} strategies={props.strategies} session={props.session} isAdmin={props.isAdmin} changed={props.changed} openOperation={props.openOperation} openExperiment={id => {window.location.hash = `results/instance/${encodeURIComponent(id)}`} } />
    </section>
    <section className={`view ${props.view === 'suites' ? 'active' : ''}`}>
      <SuitesView configurations={props.experimentConfigurations} applications={props.applications} experiments={props.experiments} clusters={props.kubernetesClusters} session={props.session} isAdmin={props.isAdmin} changed={props.changed} navigate={id => {window.location.hash = `results/instance/${encodeURIComponent(id)}`} } />
    </section>
  </>
}

function PluginImportRow({item}: {item: PluginImportJob}) {
  const title = item.pluginId ? `${item.pluginId}${item.version ? ` · ${item.version}` : ''}` : `Package ${shortID(item.id)}`
  const detail = item.error || item.message
  return <article className="credential-row import-row"><span className="feature-icon">⇣</span><div><h3>{title}</h3><p>{detail}</p><div className="import-progress" role="progressbar" aria-label={`${title} import progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={item.progress}><span style={{width: `${item.progress}%`}} /></div></div><Status value={item.status} /></article>
}

function Overview(props: ViewProps) {
  const clusters = props.kubernetesClusters.filter(item => item.status === 'ready').length
  const active = props.experiments.filter(item => ['queued', 'running'].includes(item.status)).length
  const completed = props.experiments.filter(item => item.status === 'succeeded').length
  const activity = buildRecentActivity({experiments: props.experiments, configurations: props.experimentConfigurations, connections: props.connections, resources: [...props.machineTemplates, ...props.infrastructureServices, ...props.kubernetesClusters], applications: props.applications})
  return <>
    <div className="section-heading overview-heading"><div><SectionIcon kind="overview" /><h2>Clusters and experiments</h2></div></div>
    <div className="stats-grid">
      <Stat label="Ready clusters" value={clusters} />
      <Stat label="Configurations" value={props.experimentConfigurations.length} />
      <Stat label="In progress" value={active} />
      <Stat label="Completed" value={completed} />
    </div>
    <Heading eyebrow="RECENT ACTIVITY" title="Recent activity" icon="activity" />
    <RecentActivity items={activity} />
  </>
}

function Stat({label, value}: {label: string; value?: number}) {
  return <article className="stat-card"><span>{label}</span><strong>{value ?? '—'}</strong></article>
}

function Heading({title, copy, action, icon}: {eyebrow: string; title: string; copy?: string; action?: React.ReactNode; icon: SectionIconKind}) {
  return <div className="section-heading"><div><SectionIcon kind={icon} /><h2>{title}</h2>{copy && <p className="section-copy">{copy}</p>}</div>{action}</div>
}

export interface RecentActivityItem {
  id: string
  title: string
  detail: string
  status: string
  createdAt: string
  href: string
}

export function buildRecentActivity({experiments, configurations, connections, resources, applications}: {experiments: Experiment[]; configurations: ExperimentConfiguration[]; connections: Connection[]; resources: ManagedResource[]; applications: Application[]}): RecentActivityItem[] {
  const experimentTitles: Record<string, string> = {queued: 'Experiment queued', running: 'Experiment in progress', succeeded: 'Experiment completed', failed: 'Experiment failed', canceled: 'Experiment stopped', cancelled: 'Experiment stopped'}
  const resourceTitles: Record<string, (label: string) => string> = {
    pending: label => `Preparing ${label.toLowerCase()}`,
    provisioning: label => `Creating ${label.toLowerCase()}`,
    ready: label => `${label} ready`,
    recreating: label => `Recreating ${label.toLowerCase()}`,
    deleting: label => `Deleting ${label.toLowerCase()}`,
    failed: label => `${label} failed`,
    'recreation-failed': label => `${label} recreation failed`
  }
  const resourceLabels: Record<string, string> = {'machine-template': 'VM template', harbor: 'Harbor registry', nfs: 'NFS server', 'kubernetes-cluster': 'Kubernetes cluster'}
  const values: RecentActivityItem[] = [
    ...experiments.map(item => ({id: `experiment-${item.id}`, title: experimentTitles[item.status] ?? 'Experiment updated', detail: item.name, status: item.status, createdAt: item.updatedAt || item.createdAt, href: `results/instance/${encodeURIComponent(item.id)}`})),
    ...resources.map(item => {
      const label = resourceLabels[item.kind] ?? 'Resource'
      return {id: `resource-${item.id}`, title: (resourceTitles[item.status] ?? ((value: string) => `${value} updated`))(label), detail: item.name, status: item.status, createdAt: item.updatedAt || item.createdAt, href: `${item.kind === 'kubernetes-cluster' ? 'kubernetes' : 'infrastructure'}/resource/${encodeURIComponent(item.id)}`}
    }),
    ...configurations.map(item => ({id: `configuration-${item.id}`, title: 'Experiment configuration updated', detail: item.name, status: item.validation.valid ? 'validated' : 'invalid', createdAt: item.updatedAt || item.createdAt, href: `experiments/configuration/${encodeURIComponent(item.id)}`})),
    ...connections.map(item => ({id: `connection-${item.id}`, title: 'Proxmox connection added', detail: item.name, status: 'ready', createdAt: item.updatedAt || item.createdAt, href: `infrastructure/connection/${encodeURIComponent(item.id)}`})),
    ...applications.filter(item => item.updatedAt || item.createdAt).map(item => ({id: `application-${item.reference}`, title: 'Application added to catalog', detail: `${item.name} ${item.version}`, status: 'ready', createdAt: item.updatedAt || item.createdAt || '', href: `catalog/application/${encodeURIComponent(item.reference)}`}))
  ]
  return values.filter(item => item.createdAt).sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt)).slice(0, 6)
}

function RecentActivity({items}: {items: RecentActivityItem[]}) {
  if (!items.length) return <Empty title="No activity yet">Resources and experiments will appear here.</Empty>
  return <div className="operation-list">{items.map(item => {
    return <button className="operation-row" key={item.id} onClick={() => {window.location.hash = item.href}}>
      <div><h3>{item.title}</h3><p>{item.detail}</p></div>
      <Status value={item.status} />
      <span className="date">{formatDate(item.createdAt)}</span>
      <span className="operation-arrow">›</span>
    </button>
  })}</div>
}

function WorkspaceGrid({items, createOperation, openWorkspace}: {items: Workspace[]; createOperation: (workspace: Workspace) => void; openWorkspace: (workspace: Workspace) => void}) {
  if (!items.length) return <Empty title="No workspace created">Workspaces keep development and experiments independent.</Empty>
  return <div className="workspace-grid">{items.map(workspace => <article className="workspace-card" key={workspace.id}>
    <span className="workspace-icon">{workspace.name.slice(0, 1).toUpperCase()}</span>
    <h3>{workspace.name}</h3>
    <p>{workspace.description || 'An isolated environment ready for operations.'}</p>
    <div className="card-footer"><time>{formatDate(workspace.createdAt)}</time><div className="card-actions"><button className="text-button" onClick={() => createOperation(workspace)}>Advanced</button><button className="button primary compact" onClick={() => openWorkspace(workspace)}>Open workspace</button></div></div>
  </article>)}</div>
}

function PluginGrid({items}: {items: Plugin[]}) {
  if (!items.length) return <Empty title="No plugins installed">Add a conforming plugin to provide a capability.</Empty>
  return <div className="workspace-grid">{items.map(plugin => {
    const inputs = plugin.artifactInputs ?? []
    const outputs = plugin.artifactOutputs ?? []
    const runtime = plugin.runtime.kind === 'oci' ? `Isolated · ${plugin.runtime.digest?.slice(0, 12)}` : 'Built in'
    return <article className="feature-card" key={plugin.id}>
      <span className="feature-icon">⌘</span><div><h3>{plugin.name}</h3><p>{plugin.description}</p><p>{plugin.id} · {plugin.version}</p>{!!(inputs.length || outputs.length) && <small>{inputs.length} typed inputs · {outputs.length} typed outputs</small>}</div><span className="planned">{runtime}</span>
    </article>
  })}</div>
}

function CatalogView(props: ViewProps) {
  const detail = useDetailRoute('catalog')
  const [activeTab, setActiveTab] = useState<'application' | CatalogStrategy['kind']>('application')
  const application = detail.route?.kind === 'application' ? props.applications.find(item => item.reference === detail.route?.id) : undefined
  const selectedStrategy = detail.route?.kind === 'strategy' ? props.strategies.find(item => item.id === detail.route?.id) : undefined
  useEffect(() => {
    if (application) setActiveTab('application')
    if (selectedStrategy) setActiveTab(selectedStrategy.kind)
  }, [application?.reference, selectedStrategy?.id, selectedStrategy?.kind])
  if (application) return <ApplicationDetailsPage application={application} close={detail.close} canDelete={props.isAdmin} remove={async item => {const removed = await props.deleteApplication(item); if (removed) detail.close(); return removed}} />
  const strategies = <StrategyCatalog kind={selectedStrategy?.kind ?? (activeTab === 'application' ? 'scheduler' : activeTab)} items={props.strategies} workspaces={props.workspaces} services={props.infrastructureServices} session={props.session} isAdmin={props.isAdmin} changed={props.changed} openOperation={props.openOperation} />
  if (detail.route?.kind === 'strategy') return strategies
  return <><Heading eyebrow="CATALOG" title="Catalog" icon="catalog" /><nav className="resource-tabs" aria-label="Catalog items">{([['application', 'Applications', props.applications.length], ['scheduler', 'Schedulers', props.strategies.filter(item => item.kind === 'scheduler').length], ['descheduler', 'Deschedulers', props.strategies.filter(item => item.kind === 'descheduler').length], ['autoscaler', 'Autoscalers', props.strategies.filter(item => item.kind === 'autoscaler').length]] as const).map(([tab, label, count]) => <button key={tab} className={activeTab === tab ? 'active' : ''} aria-current={activeTab === tab ? 'page' : undefined} onClick={() => setActiveTab(tab)}>{label}<span>{count}</span></button>)}</nav>{activeTab === 'application' ? <><Heading eyebrow="APPLICATIONS" title="Applications" icon="application" action={props.isAdmin && <ResourceAction action="create" label="Import application" onClick={props.importApplication} />} /><ApplicationGrid items={props.applications} isAdmin={props.isAdmin} remove={props.deleteApplication} open={item => detail.open('application', item.reference)} /></> : strategies}</>
}

function ApplicationGrid({items, isAdmin, remove, open}: {items: Application[]; isAdmin: boolean; remove: (application: Application) => Promise<boolean>; open: (application: Application) => void}) {
  if (!items.length) return <Empty title="No applications available">Import a versioned descriptor that implements the application contract.</Empty>
  return <div className="catalog-grid">{items.map(application => {
    const specification = application.descriptor.spec ?? {}
    const components = specification.interface?.components ?? []
    return <article className="application-card" key={application.reference}>
      <div className="application-card-header"><span className="application-icon">{application.name.slice(0, 1).toUpperCase()}</span><Status value={application.origin} /></div>
      <h3>{application.name}</h3><p>{application.version} · {components.length} components{specification.interface?.group ? ` · ${specification.interface.group}` : ''}</p>
      <div className="managed-actions"><ResourceAction action="view" label={`View ${application.name} details`} onClick={() => open(application)} />{isAdmin && <ResourceAction action="delete" label={`Delete ${application.name}`} onClick={() => void remove(application)} />}</div>
    </article>
  })}</div>
}

function Infrastructure(props: ViewProps) {
  const detail = useDetailRoute('infrastructure')
  const [activeTab, setActiveTab] = useState<'connections' | 'templates' | 'harbor' | 'nfs'>('connections')
  const selectedConnection = detail.route?.kind === 'connection' ? props.connections.find(item => item.id === detail.route?.id) : undefined
  const managedResources = [...props.machineTemplates, ...props.infrastructureServices, ...props.kubernetesClusters]
  const selectedServices = props.infrastructureServices.filter(item => item.kind === activeTab)
  const resource = detail.route?.kind === 'resource' ? managedResources.find(item => item.id === detail.route?.id) : undefined
  useEffect(() => {
    if (selectedConnection) setActiveTab('connections')
    if (resource?.kind === 'machine-template') setActiveTab('templates')
    if (resource?.kind === 'harbor' || resource?.kind === 'nfs') setActiveTab(resource.kind)
  }, [selectedConnection?.id, resource?.id, resource?.kind])
  if (selectedConnection) return <ConnectionDetailsPage connection={selectedConnection} credentials={props.credentials} close={detail.close} />
  if (resource) return <ResourceDetailsPage resource={resource} close={detail.close} connections={props.connections} workspaces={props.workspaces} resources={managedResources} runs={props.pipelineRuns} pipelines={props.pipelines} session={props.session} openOperation={props.openOperation} />
  return <>
    <Heading eyebrow="INFRASTRUCTURE" title="Infrastructure" icon="infrastructure" action={props.isAdmin && (activeTab === 'connections' ? <ResourceAction action="create" label="New connection" onClick={props.addConnection} /> : activeTab === 'templates' ? <ResourceAction action="create" label="New VM template" disabled={!props.connections.length} onClick={props.addMachineTemplate} /> : <ResourceAction action="create" label={activeTab === 'harbor' ? 'New Harbor' : 'New NFS'} disabled={!props.machineTemplates.some(item => item.status === 'ready')} onClick={() => props.addInfrastructureService(activeTab)} />)} />
    <nav className="resource-tabs" aria-label="Infrastructure resources">{([['connections', 'Proxmox connections', props.connections.length], ['templates', 'VM templates', props.machineTemplates.length], ['harbor', 'Harbor', props.infrastructureServices.filter(item => item.kind === 'harbor').length], ['nfs', 'NFS', props.infrastructureServices.filter(item => item.kind === 'nfs').length]] as const).map(([tab, label, count]) => <button key={tab} className={activeTab === tab ? 'active' : ''} onClick={() => setActiveTab(tab)} aria-current={activeTab === tab ? 'page' : undefined}>{label}<span>{count}</span></button>)}</nav>
    {activeTab === 'connections' && <div className="credential-list">{props.connections.length ? props.connections.map(item => <article className="credential-row" key={item.id}><span className="feature-icon">↗</span><div><h3>{item.name}</h3><p>{item.provider}</p></div><div className="card-actions"><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('connection', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} onClick={() => void props.deleteConnection(item.id, item.name)} />}</div></article>) : <Empty title="No Proxmox connection">Add a connection to continue.</Empty>}</div>}
    {activeTab === 'templates' && <div className="managed-grid">{props.machineTemplates.length ? props.machineTemplates.map(item => <article className="managed-card" key={item.id}><div className="managed-card-head"><span className="feature-icon">▦</span><Status value={item.status} /></div><h3>{item.name}</h3><p>{specText(item, 'node')} · VMID {specText(item, 'vmid')}</p>{item.error && <p className="managed-error">{item.error}</p>}<div className="managed-actions"><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('resource', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={item.status !== 'ready'} onClick={() => void props.deleteMachineTemplate(item.id, item.name)} />}</div></article>) : <Empty title="No VM template">Create a VM template first.</Empty>}</div>}
    {(activeTab === 'harbor' || activeTab === 'nfs') && <div className="managed-grid">{selectedServices.length ? selectedServices.map(item => <article className="managed-card" key={item.id}><div className="managed-card-head"><span className="feature-icon">{item.kind === 'harbor' ? '▣' : '▤'}</span><Status value={item.status} /></div><h3>{item.name}</h3><p>VMID {specText(item, 'vmid')}</p>{item.error && <p className="managed-error">{item.error}</p>}<div className="managed-actions"><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('resource', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={item.status !== 'ready' && item.status !== 'failed'} onClick={() => void props.deleteInfrastructureService(item.id, item.name)} />}</div></article>) : <Empty title={`No ${activeTab === 'harbor' ? 'Harbor registry' : 'NFS server'}`}>Create one from a ready VM template.</Empty>}</div>}
  </>
}

function Kubernetes(props: ViewProps) {
  const detail = useDetailRoute('kubernetes')
  const readyServices = props.infrastructureServices.filter(item => item.status === 'ready')
  const canCreate = props.machineTemplates.some(item => item.status === 'ready') && readyServices.some(item => item.kind === 'harbor') && readyServices.some(item => item.kind === 'nfs')
  const resource = detail.route?.kind === 'resource' ? props.kubernetesClusters.find(item => item.id === detail.route?.id) : undefined
  if (resource) return <ResourceDetailsPage resource={resource} close={detail.close} connections={props.connections} workspaces={props.workspaces} resources={[...props.machineTemplates, ...props.infrastructureServices, ...props.kubernetesClusters]} runs={props.pipelineRuns} pipelines={props.pipelines} session={props.session} openOperation={props.openOperation} recreate={() => props.recreateKubernetesCluster(resource.id, resource.name)} recreation={props.clusterActions[resource.id]} />
  return <>
    <Heading eyebrow="MANAGED CLUSTERS" title="Kubernetes clusters" icon="kubernetes" action={props.isAdmin && <ResourceAction action="create" label="New cluster" disabled={!canCreate} onClick={props.addKubernetesCluster} />} />
    <div className="stats-grid infrastructure-stats"><Stat label="Clusters" value={props.kubernetesClusters.length} /><Stat label="Ready" value={props.kubernetesClusters.filter(item => item.status === 'ready').length} /><Stat label="Nodes" value={props.kubernetesClusters.reduce((total, item) => total + clusterNodeCount(item), 0)} /></div>
    <div className="managed-grid">{props.kubernetesClusters.length ? props.kubernetesClusters.map(item => {
      const action = props.clusterActions[item.id]
      return <article className="managed-card cluster-card" key={item.id}>
        <div className="managed-card-head"><SectionIcon kind="kubernetes" /><Status value={action?.pending ? 'validating' : item.status} /></div>
        <h3>{item.name}</h3><p>K3s · {clusterNodeCount(item)} nodes · {formatDate(item.createdAt)}</p>
        {item.error && <p className="managed-error">{item.error}</p>}
        {action && <p className={action.error ? 'managed-error' : 'managed-feedback'}>{action.message}</p>}
        <div className="managed-actions"><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('resource', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={action?.pending || item.status !== 'ready' && item.status !== 'failed' && item.status !== 'recreation-failed'} onClick={() => void props.deleteKubernetesCluster(item.id, item.name)} />}</div>
      </article>
    }) : <Empty title="No Kubernetes cluster">Create Harbor and NFS first.</Empty>}</div>
  </>
}

function poolCount(resource: ManagedResource, field: string): number {
  const pool = resource.spec?.[field]
  return pool && typeof pool === 'object' && 'count' in pool && typeof pool.count === 'number' ? pool.count : 0
}

function applicationPoolCount(resource: ManagedResource): number {
  const pools = resource.spec?.applicationPools
  return Array.isArray(pools) ? pools.reduce((total, pool) => total + (pool && typeof pool === 'object' && typeof pool.count === 'number' ? pool.count : 0), 0) : 0
}

function clusterNodeCount(resource: ManagedResource): number {
  const controlPlanes = Number(resource.spec?.controlPlanes ?? 0)
  return controlPlanes + poolCount(resource, 'managementPool') + applicationPoolCount(resource)
}

function specText(resource: ManagedResource, field: string): string {
  const value = resource.spec?.[field]
  return value === undefined || value === null || value === '' ? '—' : String(value)
}

export function infrastructureControls(plugins: Plugin[]): Plugin[] {
  return plugins.filter(plugin => (plugin.capabilities ?? []).some(capability => capability.startsWith('infrastructure.') && capability.endsWith('.control')))
}

function InfoRow({icon, title, detail, status}: {icon: string; title: string; detail: string; status: string}) {
  return <article className="credential-row"><span className="feature-icon">{icon}</span><div><h3>{title}</h3><p>{detail}</p></div><Status value={status} /></article>
}

export function Status({value}: {value: string}) {
  return <span className={`status ${value.toLowerCase()}`}>{value}</span>
}

function Empty({title, children}: {title: string; children: React.ReactNode}) {
  return <div className="empty-state"><div><strong>{title}</strong>{children}</div></div>
}
