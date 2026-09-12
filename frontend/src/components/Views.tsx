import {formatDate, shortID} from '../lib'
import type {Application, Artifact, AuditEvent, Connection, Credential, Experiment, InfrastructureResource, ManagedResource, Operation, Pipeline, PipelineRun, Plugin, PluginImportJob, PluginPackage, PluginRuntimeStatus, Session, SystemStatus, View, Workspace} from '../types'
import {PipelinesView} from './PipelinesView'
import {ResultsView} from './ResultsView'

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
  plugins: Plugin[]
  pluginPackages: PluginPackage[]
  pluginImports: PluginImportJob[]
  applications: Application[]
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
  importPlugin: () => void
  configureRuntime: () => void
  activatePlugin: (pluginPackage: PluginPackage) => Promise<void>
  deactivatePlugin: (pluginPackage: PluginPackage) => Promise<void>
  addCredential: () => void
  addConnection: () => void
  addMachineTemplate: () => void
  addInfrastructureService: () => void
  deleteConnection: (id: string, name: string) => Promise<void>
  deleteMachineTemplate: (id: string, name: string) => Promise<void>
  deleteInfrastructureService: (id: string, name: string) => Promise<void>
  addKubernetesCluster: () => void
  deleteKubernetesCluster: (id: string, name: string) => Promise<void>
  openInfrastructureCapability: (workspace: Workspace, pluginID: string) => void
  openTerminal: (workspace: Workspace) => void
}

export function Views(props: ViewProps) {
  return <>
    <section className={`view ${props.view === 'overview' ? 'active' : ''}`}>
      <Overview {...props} />
    </section>
    <section className={`view ${props.view === 'workspaces' ? 'active' : ''}`}>
      <Heading eyebrow="ENVIRONMENTS" title="Your workspaces" copy="Each workspace keeps its operations and resources independent." />
      <WorkspaceGrid items={props.workspaces} createOperation={props.createOperation} openWorkspace={props.openWorkspace} />
    </section>
    <section className={`view ${props.view === 'operations' ? 'active' : ''}`}>
      <Heading eyebrow="BACKGROUND WORK" title="Operation history" copy="Validated plans, live progress and diagnostic evidence." />
      <OperationList operations={props.operations} workspaces={props.workspaces} open={props.openOperation} />
    </section>
    <section className={`view ${props.view === 'pipelines' ? 'active' : ''}`}>
      <PipelinesView pipelines={props.pipelines} runs={props.pipelineRuns} workspaces={props.workspaces} plugins={props.plugins} session={props.session} create={props.createPipeline} changed={props.changed} openOperation={props.openOperation} />
    </section>
    <section className={`view ${props.view === 'results' ? 'active' : ''}`}>
      <ResultsView artifacts={props.artifacts} experiments={props.experiments} operations={props.operations} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} />
    </section>
    <section className={`view ${props.view === 'catalog' ? 'active' : ''}`}>
      <Heading eyebrow="APPLICATION INTERFACE" title="Application catalog" action={props.isAdmin && <button className="button primary" onClick={props.importApplication}>Import application</button>} />
      <p className="section-copy catalog-copy">Versioned packages expose workloads and endpoints through one validated contract.</p>
      <ApplicationGrid items={props.applications} />
    </section>
    <section className={`view ${props.view === 'plugins' ? 'active' : ''}`}>
      <Heading eyebrow="CAPABILITIES" title="Installed plugins" action={props.isAdmin && <button className="button primary" onClick={props.importPlugin}>Import plugin</button>} />
      <p className="section-copy catalog-copy">{props.system?.features?.ociPluginImport ? 'Every external capability is validated and activated by immutable image digest.' : 'External packages can be reviewed now; activation becomes available when the dedicated OCI executor is healthy.'}</p>
      <div className={`runtime-card ${props.pluginRuntime?.status ?? 'disabled'}`}><span className="feature-icon">⬡</span><div><p className="eyebrow">ISOLATED EXECUTION</p><h3>{props.pluginRuntime?.configured ? 'Managed OCI runtime' : 'OCI runtime not configured'}</h3><p>{props.pluginRuntime?.message ?? 'Select verified executor and registry artifacts to enable external plugins.'}</p></div><div className="card-actions"><Status value={props.pluginRuntime?.status ?? 'disabled'} />{props.isAdmin && <button className="button secondary compact" onClick={props.configureRuntime}>{props.pluginRuntime?.configured ? 'Manage' : 'Configure'}</button>}</div></div>
      <PluginGrid items={props.plugins} />
      {!!props.pluginImports.length && <><Heading eyebrow="IMPORT QUEUE" title="Package validation" copy="Imports run in the background and activate only after every gate passes." /><div className="credential-list">{props.pluginImports.slice(0, 10).map(item => <PluginImportRow key={item.id} item={item} />)}</div></>}
      {!!props.pluginPackages.length && <><Heading eyebrow="PACKAGE HISTORY" title="Imported versions" copy="Every activation remains traceable and a prior version can be restored only after all checks pass again." /><div className="credential-list">{props.pluginPackages.map(item => <article className="credential-row" key={item.sequence}><span className="feature-icon">◇</span><div><h3>{item.pluginId} · {item.version}</h3><p>{item.digest.slice(0, 19)} · descriptor {item.descriptorDigest.slice(0, 19)}</p></div>{item.active ? <div className="card-actions"><Status value="Active" /><button className="button secondary compact" onClick={() => props.deactivatePlugin(item)}>Deactivate</button></div> : <button className="button secondary compact" disabled={!props.system?.features?.ociPluginImport} title={props.system?.features?.ociPluginImport ? '' : 'Configure the dedicated OCI executor first'} onClick={() => props.activatePlugin(item)}>Restore</button>}</article>)}</div></>}
    </section>
    <section className={`view ${props.view === 'infrastructure' ? 'active' : ''}`}>
      <Infrastructure {...props} />
    </section>
    <section className={`view ${props.view === 'kubernetes' ? 'active' : ''}`}>
      <Kubernetes {...props} />
    </section>
    <section className={`view ${props.view === 'experiments' ? 'active' : ''}`}>
      <ProductNext eyebrow="CONFIGURE ONCE" title="Experiment configurations" copy="Choose an application, load, chaos and scheduling strategies, then create isolated repeatable runs." />
    </section>
    <section className={`view ${props.view === 'suites' ? 'active' : ''}`}>
      <ProductNext eyebrow="COMPARE" title="Experiment suites" copy="Run multiple configurations with controlled ordering and compare aggregate scientific results." />
    </section>
    <section className={`view ${props.view === 'advanced' ? 'active' : ''}`}>
      <Advanced props={props} />
    </section>
  </>
}

function PluginImportRow({item}: {item: PluginImportJob}) {
  const title = item.pluginId ? `${item.pluginId}${item.version ? ` · ${item.version}` : ''}` : `Package ${shortID(item.id)}`
  const detail = item.error || item.message
  return <article className="credential-row import-row"><span className="feature-icon">⇣</span><div><h3>{title}</h3><p>{detail}</p><div className="import-progress" role="progressbar" aria-label={`${title} import progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={item.progress}><span style={{width: `${item.progress}%`}} /></div></div><Status value={item.status} /></article>
}

function Overview(props: ViewProps) {
  const stats = props.system?.stats
  return <>
    <div className="hero">
      <div><span className="hero-kicker">READY WHEN YOU ARE</span><h2>Build, inspect and repeat.</h2><p>Create an isolated workspace, validate the complete plan and follow every health gate from one place.</p></div>
      <button className="button light" onClick={props.createWorkspace}>Create your workspace <span>→</span></button>
    </div>
    <div className="stats-grid">
      <Stat label="Workspaces" value={stats?.workspaces} caption="isolated environments" />
      <Stat label="Active" value={stats?.activeOperations} caption="background operations" />
      <Stat label="Awaiting approval" value={stats?.readyOperations} caption="validated plans" />
      <Stat label="Needs attention" value={stats?.failedOperations} caption="failed operations" />
    </div>
    <Heading eyebrow="RECENT ACTIVITY" title="Operations" action={<button className="text-button" onClick={() => props.navigate('operations')}>View all →</button>} />
    <OperationList operations={props.operations.slice(0, 5)} workspaces={props.workspaces} open={props.openOperation} />
  </>
}

function Stat({label, value, caption}: {label: string; value?: number; caption: string}) {
  return <article className="stat-card"><span>{label}</span><strong>{value ?? '—'}</strong><small>{caption}</small></article>
}

function Heading({eyebrow, title, copy, action}: {eyebrow: string; title: string; copy?: string; action?: React.ReactNode}) {
  return <div className="section-heading"><div><p className="eyebrow">{eyebrow}</p><h2>{title}</h2></div>{copy ? <p className="section-copy">{copy}</p> : action}</div>
}

function OperationList({operations, workspaces, open}: {operations: Operation[]; workspaces: Workspace[]; open: (id: string) => void}) {
  if (!operations.length) return <Empty title="No operations yet">Create a workspace and validate your first plan.</Empty>
  return <div className="operation-list">{operations.map(operation => {
    const workspace = workspaces.find(item => item.id === operation.workspaceId)
    return <button className="operation-row" key={operation.id} onClick={() => open(operation.id)}>
      <div><h3>{operation.title}</h3><p>{shortID(operation.id)}</p></div>
      <div><h3>{workspace?.name ?? 'Workspace'}</h3><p>{operation.pluginId}</p></div>
      <Status value={operation.status} />
      <span className="date">{formatDate(operation.createdAt)}</span>
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

function ApplicationGrid({items}: {items: Application[]}) {
  if (!items.length) return <Empty title="No applications available">Import a versioned descriptor that implements the application contract.</Empty>
  return <div className="catalog-grid">{items.map(application => {
    const specification = application.descriptor.spec ?? {}
    const components = specification.interface?.components ?? []
    const endpoints = specification.interface?.endpoints ?? []
    const loadDrivers = specification.interface?.loadDrivers ?? []
    const traits = [...new Set(components.flatMap(component => component.traits ?? []))].sort()
    const source = specification.package ?? {}
    return <article className="application-card" key={application.reference}>
      <div className="application-card-header"><span className="application-icon">{application.name.slice(0, 1).toUpperCase()}</span><Status value={application.origin} /></div>
      <h3>{application.name}</h3><p>{application.description || 'A contract-compatible application package.'}</p>
      <div className="trait-list">{traits.map(trait => <span key={trait}>{trait}</span>)}</div>
      <dl><div><dt>Version</dt><dd>{application.version}</dd></div><div><dt>Components</dt><dd>{components.length}</dd></div><div><dt>Endpoints</dt><dd>{endpoints.length}</dd></div><div><dt>Load profiles</dt><dd>{loadDrivers.length}</dd></div><div><dt>Package</dt><dd>{source.type ?? 'unknown'} · {source.format ?? 'unknown'}</dd></div></dl>
      <small className="digest" title={application.digest}>{application.digest}</small>
    </article>
  })}</div>
}

function Infrastructure(props: ViewProps) {
  return <>
    <Heading eyebrow="START HERE" title="Infrastructure" action={props.isAdmin && <div className="topbar-actions"><button className="button secondary" onClick={props.addConnection}>New connection</button><button className="button primary" disabled={!props.connections.length} onClick={props.addMachineTemplate}>New VM template</button></div>} />
    <p className="section-copy infrastructure-copy">Connect Proxmox once, then create the building blocks used by every cluster.</p>
    <div className="stats-grid infrastructure-stats"><Stat label="Connections" value={props.connections.length} caption="validated providers" /><Stat label="VM templates" value={props.machineTemplates.length} caption="reusable Proxmox templates" /><Stat label="Services" value={props.infrastructureServices.length} caption="managed Harbor and NFS" /></div>
    <Heading eyebrow="1 · ACCESS" title="Proxmox connections" copy="Credentials stay encrypted. A connection cannot be removed while active resources use it." />
    <div className="credential-list">{props.connections.length ? props.connections.map(item => <article className="credential-row" key={item.id}><span className="feature-icon">↗</span><div><h3>{item.name}</h3><p>{item.provider} · validated {formatDate(item.createdAt ?? new Date().toISOString())}</p></div><div className="card-actions"><Status value="ready" />{props.isAdmin && <button className="button danger compact" onClick={() => props.deleteConnection(item.id, item.name)}>Delete</button>}</div></article>) : <Empty title="No Proxmox connection">Create a connection; KubePhos will validate endpoint, credentials and permissions.</Empty>}</div>
    <Heading eyebrow="2 · BASE IMAGE" title="Proxmox VM templates" copy="These are real Proxmox templates, prepared and verified in the background." />
    <div className="managed-grid">{props.machineTemplates.length ? props.machineTemplates.map(item => <article className="managed-card" key={item.id}><div className="managed-card-head"><span className="feature-icon">◇</span><Status value={item.status} /></div><h3>{item.name}</h3><p>{item.provider} · node {specText(item, 'node')} · VMID {specText(item, 'vmid')}</p><dl><div><dt>OS</dt><dd>{specText(item, 'os')}</dd></div><div><dt>Disk</dt><dd>{specText(item, 'diskGiB')} GiB</dd></div><div><dt>Storage</dt><dd>{specText(item, 'storage')}</dd></div><div><dt>Created</dt><dd>{formatDate(item.createdAt)}</dd></div></dl>{item.error && <p className="managed-error">{item.error}</p>}<div className="managed-actions">{(item.deletionOperationId || item.operationId) && <button className="text-button" onClick={() => props.openOperation((item.deletionOperationId || item.operationId)!)}>View activity</button>}{props.isAdmin && <button className="button danger compact" disabled={item.status !== 'ready'} onClick={() => props.deleteMachineTemplate(item.id, item.name)}>Delete</button>}</div></article>) : <Empty title="No VM template">Create the first reusable machine template from a supported cloud image.</Empty>}</div>
    <Heading eyebrow="3 · PLATFORM SERVICES" title="Harbor and NFS" action={props.isAdmin && <button className="button primary" disabled={!props.machineTemplates.some(item => item.status === 'ready')} onClick={props.addInfrastructureService}>New service</button>} />
    <div className="managed-grid">{props.infrastructureServices.length ? props.infrastructureServices.map(item => <article className="managed-card" key={item.id}><div className="managed-card-head"><span className="feature-icon">{item.kind === 'harbor' ? '▣' : '▤'}</span><Status value={item.status} /></div><h3>{item.name}</h3><p>{item.kind === 'harbor' ? 'Harbor registry' : 'NFS storage'} · VMID {specText(item, 'vmid')}</p><dl><div><dt>CPU</dt><dd>{specText(item, 'cores')} cores</dd></div><div><dt>Memory</dt><dd>{specText(item, 'memoryMiB')} MiB</dd></div><div><dt>Disk</dt><dd>{specText(item, 'diskGiB')} GiB</dd></div><div><dt>Created</dt><dd>{formatDate(item.createdAt)}</dd></div></dl>{item.error && <p className="managed-error">{item.error}</p>}<div className="managed-actions">{item.pipelineRunId && <button className="text-button" onClick={() => props.navigate('pipelines')}>View progress</button>}{props.isAdmin && <button className="button danger compact" disabled={item.status !== 'ready' && item.status !== 'failed'} onClick={() => props.deleteInfrastructureService(item.id, item.name)}>Delete</button>}</div></article>) : <Empty title="No platform service">Create Harbor or NFS from a ready VM template. Capacity and installation use managed defaults.</Empty>}</div>
  </>
}

function Kubernetes(props: ViewProps) {
  const readyServices = props.infrastructureServices.filter(item => item.status === 'ready')
  const canCreate = props.machineTemplates.some(item => item.status === 'ready') && readyServices.some(item => item.kind === 'harbor') && readyServices.some(item => item.kind === 'nfs')
  return <>
    <Heading eyebrow="MANAGED CLUSTERS" title="Kubernetes clusters" action={props.isAdmin && <button className="button primary" disabled={!canCreate} onClick={props.addKubernetesCluster}>New cluster</button>} />
    <p className="section-copy infrastructure-copy">Create a complete cluster from a VM template, Harbor, NFS and a small pool layout. KubePhos verifies every transition.</p>
    <div className="stats-grid infrastructure-stats"><Stat label="Clusters" value={props.kubernetesClusters.length} caption="managed environments" /><Stat label="Ready" value={props.kubernetesClusters.filter(item => item.status === 'ready').length} caption="all health gates passed" /><Stat label="Nodes" value={props.kubernetesClusters.reduce((total, item) => total + clusterNodeCount(item), 0)} caption="declared capacity" /></div>
    <div className="managed-grid">{props.kubernetesClusters.length ? props.kubernetesClusters.map(item => <article className="managed-card cluster-card" key={item.id}><div className="managed-card-head"><span className="feature-icon">⬡</span><Status value={item.status} /></div><h3>{item.name}</h3><p>K3s · {clusterNodeCount(item)} nodes · VMIDs from {specText(item, 'baseVMID')}</p><dl><div><dt>Control plane</dt><dd>{specText(item, 'controlPlanes')}</dd></div><div><dt>Management</dt><dd>{poolCount(item, 'managementPool')} nodes</dd></div><div><dt>Application</dt><dd>{applicationPoolCount(item)} nodes</dd></div><div><dt>Created</dt><dd>{formatDate(item.createdAt)}</dd></div></dl>{item.error && <p className="managed-error">{item.error}</p>}<div className="managed-actions">{item.pipelineRunId && <button className="text-button" onClick={() => props.navigate('pipelines')}>View progress</button>}{item.status === 'ready' && <a className="button secondary compact" href={`/api/v1/kubernetes-clusters/${item.id}/kubeconfig`}>Kubeconfig</a>}{props.isAdmin && <button className="button danger compact" disabled={item.status !== 'ready' && item.status !== 'failed'} onClick={() => props.deleteKubernetesCluster(item.id, item.name)}>Delete</button>}</div></article>) : <Empty title="No Kubernetes cluster">Create Harbor and NFS first, then KubePhos can provision the first complete cluster.</Empty>}</div>
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

function ProductNext({eyebrow, title, copy}: {eyebrow: string; title: string; copy: string}) {
  return <><Heading eyebrow={eyebrow} title={title} /><div className="product-next"><span>○</span><div><strong>Coming in the next implementation slice</strong><p>{copy}</p></div></div></>
}

function Advanced({props}: {props: ViewProps}) {
  const sections: Array<[View, string, string]> = [['workspaces', 'Workspaces', 'Isolation and development environments'], ['pipelines', 'Pipelines', 'Reusable plugin flows'], ['operations', 'Operations', 'Logs, plans and background tasks'], ['catalog', 'Catalog', 'Application contracts'], ['plugins', 'Plugins', 'Capabilities and runtime']]
  return <><Heading eyebrow="POWER TOOLS" title="Advanced" copy="Internal concepts remain available without cluttering the normal workflow." /><div className="workspace-grid">{sections.map(([view, title, copy]) => <button className="advanced-card" key={view} onClick={() => props.navigate(view)}><span>→</span><div><strong>{title}</strong><p>{copy}</p></div></button>)}</div></>
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
