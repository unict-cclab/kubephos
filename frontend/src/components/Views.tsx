import {Status, statusLabel} from './Status'
export {Status} from './Status'
import {ResourceStatus} from './ResourceConnectivity'
import {resourceStatus} from '../connectivity'
import {ResourceListAction, ResourceListHeading, ResourceListPage} from './ResourceListHeading'
import {ActionIcon} from './ActionIcon'
import {useEffect, useState} from 'react'
import {formatDate} from '../lib'
import {useDetailRoute} from '../detailRoute'
import type {Application, Artifact, AuditEvent, CatalogStrategy, ClusterUserAssignment, Connection, Credential, Experiment, ExperimentConfiguration, InfrastructureResource, ManagedResource, Operation, Pipeline, PipelineRun, Plugin, PluginImportJob, PluginPackage, PluginRuntimeStatus, Session, SystemStatus, User, View, Workspace} from '../types'
import {ExperimentConfigurationsView} from './ExperimentConfigurationsView'
import {ExperimentsHistoryView} from './ExperimentsHistoryView'
import {ResourceDetailsPage} from './ResourceDetailsDialog'
import {StrategyCatalog} from './StrategyCatalog'
import {ModelCatalogContent, useCatalogModels} from './ModelCatalog'
import {ResourceListTable} from './ResourceListTable'
import {ApplicationDetailsPage} from './ApplicationDetailsDialog'
import {ConnectionDetailsPage} from './ConnectionDetailsPage'
import {SectionIcon, type SectionIconKind} from './SectionIcon'
import {ResourceAction} from './ResourceAction'
import {UsersView} from './UsersView'
import {AdministrationView} from './AdministrationView'

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
  users: User[]
  clusterAssignments: ClusterUserAssignment[]
  session: Session
  changed: () => Promise<void>
  isAdmin: boolean
  navigate: (view: View) => void
  openOperation: (id: string) => void
  openPipelineRun?: (id: string) => void
  importApplication: () => void
  deleteApplication: (application: Application) => Promise<boolean>
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
}

export function Views(props: ViewProps) {
  return <>
    {props.view === 'overview' && <section className="view active">
      <Overview {...props} />
    </section>}
    {props.view === 'results' && <section className="view active">
      <ExperimentsHistoryView experiments={props.experiments} configurations={props.experimentConfigurations} artifacts={props.artifacts} operations={props.operations} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} openPipelineRun={props.openPipelineRun} />
    </section>}
    {props.view === 'catalog' && <section className="view active">
      <CatalogView {...props} />
    </section>}
    {props.view === 'infrastructure' && <section className="view active">
      <Infrastructure {...props} />
    </section>}
    {props.view === 'kubernetes' && <section className="view active">
      <Kubernetes {...props} />
    </section>}
    {props.view === 'experiments' && <section className="view active">
      <ExperimentConfigurationsView items={props.experimentConfigurations} experiments={props.experiments} artifacts={props.artifacts} operations={props.operations} clusters={props.kubernetesClusters} workspaces={props.workspaces} applications={props.applications} plugins={props.plugins} strategies={props.strategies} session={props.session} isAdmin={props.isAdmin} changed={props.changed} openOperation={props.openOperation} openExperiment={id => {window.location.hash = `results/instance/${encodeURIComponent(id)}`} } />
    </section>}
    {props.view === 'administration' && props.isAdmin && <section className="view active"><AdministrationView session={props.session} changed={props.changed} /></section>}
    {props.view === 'users' && <section className="view active">
      {props.isAdmin && <UsersView users={props.users} assignments={props.clusterAssignments} clusters={props.kubernetesClusters} session={props.session} changed={props.changed} />}
    </section>}
  </>
}

function Overview(props: ViewProps) {
  const clusters = props.kubernetesClusters.filter(item => resourceStatus(item) === 'ready').length
  const active = props.experiments.filter(item => ['queued', 'running'].includes(item.status)).length
  const completed = props.experiments.filter(item => item.status === 'succeeded').length
  const activity = buildRecentActivity({experiments: props.experiments, configurations: props.experimentConfigurations, connections: props.connections, resources: [...props.machineTemplates, ...props.infrastructureServices, ...props.kubernetesClusters], applications: props.applications})
  return <>
    <div className="section-heading overview-heading"><div><h2>Overview</h2></div></div>
    <div className="stats-grid">
      <Stat label="Ready clusters" value={clusters} />
      <Stat label="Configurations" value={props.experimentConfigurations.length} />
      <Stat label="In progress" value={active} />
      <Stat label="Completed" value={completed} />
    </div>
    <div className="section-heading"><div><SectionIcon kind="activity" /><h2>Recent activity</h2></div></div>
    <RecentActivity items={activity} />
  </>
}

function Stat({label, value}: {label: string; value?: number}) {
  return <article className="stat-card"><span>{label}</span><strong>{value ?? '—'}</strong></article>
}

function Heading({title, action, icon}: {title: string; action?: React.ReactNode; icon: SectionIconKind}) {
  return <ResourceListHeading title={title} icon={icon} action={action} />
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
    maintaining: label => `Maintaining ${label.toLowerCase()}`,
    'maintenance-failed': label => `${label} maintenance failed`,
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
    ...applications.filter(item => item.updatedAt || item.createdAt).map(item => ({id: `application-${item.reference}`, title: 'Application added to catalog', detail: item.name, status: 'ready', createdAt: item.updatedAt || item.createdAt || '', href: `catalog/application/${encodeURIComponent(item.reference)}`}))
  ]
  return values.filter(item => item.createdAt).sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt)).slice(0, 6)
}

function RecentActivity({items}: {items: RecentActivityItem[]}) {
  if (!items.length) return <Empty title="No activity yet">Create a configuration or prepare a cluster to see your first activity here.</Empty>
  return <div className="operation-list">{items.map(item => {
    return <button className="operation-row" key={item.id} onClick={() => {window.location.hash = item.href}}>
      <div><h3>{item.title}</h3><p>{item.detail}</p></div>
      <Status value={item.status} />
      <span className="date">{formatDate(item.createdAt)}</span>
      <span className="operation-arrow"><ActionIcon name="right" /></span>
    </button>
  })}</div>
}

function CatalogView(props: ViewProps) {
  const modelCatalog = useCatalogModels()
  const detail = useDetailRoute('catalog')
  const [activeTab, setActiveTab] = useState<'application' | 'model' | CatalogStrategy['kind']>('application')
  const visibleApplications = props.applications.filter(item => item.origin !== 'generated').filter((item, index, items) => items.findIndex(candidate => candidate.id === item.id) === index)
  const application = detail.route?.kind === 'application' ? props.applications.find(item => item.reference === detail.route?.id) : undefined
  const selectedStrategy = detail.route?.kind === 'strategy' ? props.strategies.find(item => item.id === detail.route?.id) : undefined
  useEffect(() => {
    if (application) setActiveTab('application')
    if (detail.route?.kind === 'model') setActiveTab('model')
    if (selectedStrategy) setActiveTab(selectedStrategy.kind)
  }, [application?.reference, selectedStrategy?.id, selectedStrategy?.kind, detail.route?.kind])
  if (application) return <ApplicationDetailsPage application={application} close={detail.close} canDelete={props.isAdmin} remove={async item => {const removed = await props.deleteApplication(item); if (removed) detail.close(); return removed}} />
  const strategies = <StrategyCatalog kind={selectedStrategy?.kind ?? (activeTab === 'application' || activeTab === 'model' ? 'scheduler' : activeTab)} items={props.strategies} workspaces={props.workspaces} session={props.session} isAdmin={props.isAdmin} changed={props.changed} openOperation={props.openOperation} />
  if (detail.route?.kind === 'strategy') return strategies
  if (detail.route?.kind === 'model') return <ModelCatalogContent catalog={modelCatalog} session={props.session} isAdmin={props.isAdmin} changed={props.changed} />
  return <ResourceListPage title="Catalog" icon="catalog"><nav className="resource-tabs" aria-label="Catalog items">{([['application', 'Applications', visibleApplications.length], ['scheduler', 'Schedulers', props.strategies.filter(item => item.kind === 'scheduler').length], ['descheduler', 'Deschedulers', props.strategies.filter(item => item.kind === 'descheduler').length], ['autoscaler', 'Autoscalers', props.strategies.filter(item => item.kind === 'autoscaler').length], ['model', 'AI models', modelCatalog.loading ? '…' : modelCatalog.error ? '?' : modelCatalog.models.length]] as const).map(([tab, label, count]) => <button key={tab} className={activeTab === tab ? 'active' : ''} aria-current={activeTab === tab ? 'page' : undefined} onClick={() => setActiveTab(tab)}>{label}{count !== null && <span>{count}</span>}</button>)}</nav>{activeTab === 'application' && props.isAdmin && <ResourceListAction><ResourceAction action="create" label="Import application" onClick={props.importApplication} /></ResourceListAction>}{activeTab === 'application' ? <ApplicationTable items={visibleApplications} isAdmin={props.isAdmin} remove={props.deleteApplication} open={item => detail.open('application', item.reference)} /> : activeTab === 'model' ? <ModelCatalogContent catalog={modelCatalog} session={props.session} isAdmin={props.isAdmin} changed={props.changed} /> : strategies}</ResourceListPage>
}

function ApplicationTable({items, isAdmin, remove, open}: {items: Application[]; isAdmin: boolean; remove: (application: Application) => Promise<boolean>; open: (application: Application) => void}) {
  return <ResourceListTable editable items={items.map(item => ({...item, createdAt: item.createdAt ?? '', updatedAt: item.updatedAt ?? ''}))} label="Applications" searchText={item => `${item.name} ${item.description}`} columns={[
    {label: 'Name', render: item => <h3>{item.name}</h3>},
    {label: 'Source', render: item => item.origin === 'built-in' ? 'Included' : 'Imported'},
    {label: 'Services', render: item => item.descriptor.spec?.valuesSchema?.['x-kubephos-dynamic-topology'] ? 'Configurable' : item.descriptor.spec?.interface?.components?.length ?? 0}
  ]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => open(item)} />{isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} onClick={() => void remove(item)} />}</>} />
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
  if (selectedConnection) return <ConnectionDetailsPage connection={selectedConnection} session={props.session} close={detail.close} />
  if (resource) return <ResourceDetailsPage resource={resource} close={detail.close} connections={props.connections} workspaces={props.workspaces} resources={managedResources} runs={props.pipelineRuns} pipelines={props.pipelines} session={props.session} openOperation={props.openOperation} openPipelineRun={props.openPipelineRun} />
  return <>
    <Heading title="Infrastructure" icon="infrastructure" action={props.isAdmin && (activeTab === 'connections' ? <ResourceAction action="create" label="New Proxmox connection" onClick={props.addConnection} /> : activeTab === 'templates' ? <ResourceAction action="create" label="New VM template" disabled={!props.connections.length} onClick={props.addMachineTemplate} /> : <ResourceAction action="create" label={activeTab === 'harbor' ? 'New Harbor' : 'New NFS'} disabled={!props.machineTemplates.some(item => item.status === 'ready')} onClick={() => props.addInfrastructureService(activeTab)} />)} />
    <nav className="resource-tabs" aria-label="Infrastructure resources">{([['connections', 'Proxmox connections', props.connections.length], ['templates', 'VM templates', props.machineTemplates.length], ['harbor', 'Harbor', props.infrastructureServices.filter(item => item.kind === 'harbor').length], ['nfs', 'NFS', props.infrastructureServices.filter(item => item.kind === 'nfs').length]] as const).map(([tab, label, count]) => <button key={tab} className={activeTab === tab ? 'active' : ''} onClick={() => setActiveTab(tab)} aria-current={activeTab === tab ? 'page' : undefined}>{label}<span>{count}</span></button>)}</nav>
    {activeTab === 'connections' && (props.connections.length ? <ResourceListTable editable items={props.connections} label="Proxmox connections" searchText={item => `${item.name} ${statusLabel(resourceStatus(item))}`} columns={[{label: 'Name', render: item => <h3>{item.name}</h3>}, {label: 'Status', render: item => <ResourceStatus resource={item} />}]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('connection', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} onClick={() => void props.deleteConnection(item.id,item.name)} />}</>} /> : <Empty title="No Proxmox connections">Add a connection to provision machines.</Empty>)}
    {activeTab === 'templates' && (props.machineTemplates.length ? <ResourceListTable editable items={props.machineTemplates} label="VM templates" searchText={item => `${item.name} ${statusLabel(resourceStatus(item))}`} columns={[{label: 'Name', render: item => <h3>{item.name}</h3>}, {label: 'Proxmox connection', render: item => props.connections.find(connection => connection.id === item.connectionId)?.name ?? '—'}, {label: 'Status', render: item => <ResourceStatus resource={item} />}]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('resource',item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={item.status !== 'ready'} onClick={() => void props.deleteMachineTemplate(item.id,item.name)} />}</>} /> : <Empty title="No VM templates">Create a template to provision clusters.</Empty>)}
    {(activeTab === 'harbor' || activeTab === 'nfs') && (selectedServices.length ? <ResourceListTable editable items={selectedServices} label={activeTab === 'harbor' ? 'Harbor registries' : 'NFS servers'} searchText={item => `${item.name} ${statusLabel(resourceStatus(item))}`} columns={[{label: 'Name', render: item => <h3>{item.name}</h3>}, {label: 'Proxmox connection', render: item => props.connections.find(connection => connection.id === item.connectionId)?.name ?? '—'}, {label: 'Status', render: item => <ResourceStatus resource={item} />}]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('resource',item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={!['ready','failed'].includes(item.status)} onClick={() => void props.deleteInfrastructureService(item.id,item.name)} />}</>} /> : <Empty title={`No ${activeTab === 'harbor' ? 'Harbor registries' : 'NFS servers'}`}>Create one from a VM template.</Empty>)}

  </>
}

function Kubernetes(props: ViewProps) {
  const detail = useDetailRoute('kubernetes')
  const readyServices = props.infrastructureServices.filter(item => item.status === 'ready')
  const canCreate = props.machineTemplates.some(item => item.status === 'ready') && readyServices.some(item => item.kind === 'harbor') && readyServices.some(item => item.kind === 'nfs')
  const resource = detail.route?.kind === 'resource' ? props.kubernetesClusters.find(item => item.id === detail.route?.id) : undefined
  if (resource) return <ResourceDetailsPage resource={resource} close={detail.close} connections={props.connections} workspaces={props.workspaces} resources={[...props.machineTemplates, ...props.infrastructureServices, ...props.kubernetesClusters]} runs={props.pipelineRuns} pipelines={props.pipelines} session={props.session} openOperation={props.openOperation} openPipelineRun={props.openPipelineRun} recreate={() => props.recreateKubernetesCluster(resource.id, resource.name)} recreation={props.clusterActions[resource.id]} />
  return <>
    <Heading title="Kubernetes clusters" icon="kubernetes" action={props.isAdmin && <ResourceAction action="create" label="New cluster" disabled={!canCreate} onClick={props.addKubernetesCluster} />} />
    {props.kubernetesClusters.length ? <ResourceListTable editable items={props.kubernetesClusters} label="Kubernetes clusters" searchText={item => [item.name, statusLabel(resourceStatus(item)), props.connections.find(connection => connection.id === item.connectionId)?.name].filter(Boolean).join(' ')} columns={[
      {label: 'Name', render: item => <><h3>{item.name}</h3>{item.error && <p className="managed-error">{item.error}</p>}{props.clusterActions[item.id]?.message && <p className={props.clusterActions[item.id].error ? 'managed-error' : 'managed-feedback'}>{props.clusterActions[item.id].message}</p>}</>},
      {label: 'Proxmox connection', render: item => props.connections.find(connection => connection.id === item.connectionId)?.name ?? '—'},
      {label: 'Type', render: item => item.spec.mode === 'multi' ? 'Multi' : 'Single'},
      {label: 'Nodes', render: item => clusterNodeCount(item)},
      {label: 'Status', render: item => <ResourceStatus resource={item} override={props.clusterActions[item.id]?.pending ? 'validating' : undefined} />},
    ]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('resource', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={props.clusterActions[item.id]?.pending || !canDeleteKubernetesCluster(item.status)} onClick={() => void props.deleteKubernetesCluster(item.id, item.name)} />}</>} /> : <Empty title="No Kubernetes cluster">Prepare a ready Harbor registry and NFS server in Infrastructure, then create your first cluster.</Empty>}

  </>
}

export function canDeleteKubernetesCluster(status: string): boolean {
  return ['ready', 'failed', 'recreation-failed', 'maintenance-failed', 'deletion-failed'].includes(status)
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
 if (resource.spec?.mode === 'multi' && Array.isArray(resource.spec.members)) return resource.spec.members.reduce((total: number, member) => total + (member && typeof member === 'object' ? clusterNodeCount({...resource, spec: member}) : 0), 0)
  const controlPlanes = Number(resource.spec?.controlPlanes ?? 0)
  return controlPlanes + poolCount(resource, 'managementPool') + applicationPoolCount(resource)
}


function Empty({title, children}: {title: string; children: React.ReactNode}) {
  return <div className="empty-state"><div><strong>{title}</strong>{children}</div></div>
}
