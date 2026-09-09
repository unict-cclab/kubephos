import {formatDate, shortID} from '../lib'
import type {Application, Artifact, AuditEvent, Connection, Credential, Experiment, InfrastructureResource, Operation, Plugin, Session, SystemStatus, View, Workspace} from '../types'
import {ResultsView} from './ResultsView'

interface ViewProps {
  view: View
  system: SystemStatus | null
  workspaces: Workspace[]
  operations: Operation[]
  artifacts: Artifact[]
  experiments: Experiment[]
  plugins: Plugin[]
  applications: Application[]
  credentials: Credential[]
  connections: Connection[]
  resources: InfrastructureResource[]
  audit: AuditEvent[]
  session: Session
  changed: () => Promise<void>
  isAdmin: boolean
  navigate: (view: View) => void
  createWorkspace: () => void
  createOperation: (workspace: Workspace) => void
  openWorkspace: (workspace: Workspace) => void
  openOperation: (id: string) => void
  importApplication: () => void
  addCredential: () => void
  addConnection: () => void
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
    <section className={`view ${props.view === 'results' ? 'active' : ''}`}>
      <ResultsView artifacts={props.artifacts} experiments={props.experiments} operations={props.operations} workspaces={props.workspaces} session={props.session} changed={props.changed} />
    </section>
    <section className={`view ${props.view === 'catalog' ? 'active' : ''}`}>
      <Heading eyebrow="APPLICATION INTERFACE" title="Application catalog" action={props.isAdmin && <button className="button primary" onClick={props.importApplication}>Import application</button>} />
      <p className="section-copy catalog-copy">Versioned packages expose workloads and endpoints through one validated contract.</p>
      <ApplicationGrid items={props.applications} />
    </section>
    <section className={`view ${props.view === 'plugins' ? 'active' : ''}`}>
      <Heading eyebrow="CAPABILITIES" title="Installed plugins" copy="Every external capability follows the same versioned contract." />
      <PluginGrid items={props.plugins} />
    </section>
    <section className={`view ${props.view === 'infrastructure' ? 'active' : ''}`}>
      <Infrastructure {...props} />
    </section>
  </>
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
    return <article className="feature-card" key={plugin.id}>
      <span className="feature-icon">⌘</span><div><h3>{plugin.name}</h3><p>{plugin.description}</p><p>{plugin.id} · {plugin.version}</p>{!!(inputs.length || outputs.length) && <small>{inputs.length} typed inputs · {outputs.length} typed outputs</small>}</div><span className="planned">Installed</span>
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
    <Heading eyebrow="ONE ACCESS POINT" title="Infrastructure Console" action={<div className="topbar-actions">{props.isAdmin && <><button className="button secondary" onClick={props.addCredential}>Add credential</button><button className="button primary" onClick={props.addConnection}>Add connection</button></>}</div>} />
    <p className="section-copy infrastructure-copy">Managed access without remembering addresses or opening another terminal.</p>
    <div className="workspace-grid">
      <Feature icon="▣" title="Container Registry">Browse Harbor projects, images, tags and immutable digests.</Feature>
      <Feature icon="▤" title="Shared Storage">Explore NFS shares, upload data and protect destructive actions.</Feature>
      <Feature icon="›_" title="Secure Shell">Open audited, short-lived SSH sessions directly in the browser.</Feature>
    </div>
    <Heading eyebrow="REUSABLE ACCESS" title="Provider connections" copy="Validate infrastructure access once, then select it in every compatible operation." />
    <div className="credential-list">{props.connections.length ? props.connections.map(item => <InfoRow key={item.id} icon="↗" title={item.name} detail={`${item.provider} · ${item.pluginId}`} status="Validated" />) : <Empty title="No provider connections">Add and validate a reusable infrastructure connection.</Empty>}</div>
    <Heading eyebrow="OWNERSHIP BOUNDARY" title="Resource inventory" copy="Discovered resources remain protected until KubePhos can prove ownership." />
    <div className="credential-list">{props.resources.length ? props.resources.map(item => <article className="resource-row" key={item.id}><span className="feature-icon">□</span><div><h3>{item.name}</h3><p>{item.kind} · {item.externalId} · {item.state}</p></div><div className="resource-badges"><Status value={item.ownership} /><span className="protection">{item.protection}</span></div></article>) : <Empty title="No resources discovered">Run an infrastructure discovery operation to populate the ledger.</Empty>}</div>
    <Heading eyebrow="SERVER-SIDE VAULT" title="Credentials" copy="Values are encrypted and never returned to the browser." />
    <div className="credential-list">{props.credentials.length ? props.credentials.map(item => <InfoRow key={item.id} icon="⌁" title={item.name} detail={`${item.kind} · fingerprint ${item.fingerprint}`} status="Encrypted" />) : <Empty title="No credentials stored">Add a credential declared by an installed plugin.</Empty>}</div>
    <Heading eyebrow="TRACEABILITY" title="Audit trail" copy="Every API mutation is recorded without request payloads." />
    <div className="audit-list">{props.audit.length ? props.audit.map(item => <article className="audit-row" key={item.sequence}><span className="audit-sequence">#{item.sequence}</span><div><h3>{item.action}</h3><p>{item.actor} · {item.targetType}{item.targetId ? ` · ${shortID(item.targetId)}` : ''}</p></div><Status value={item.outcome} /><time>{formatDate(item.createdAt)}</time></article>) : <Empty title="No audit events">Mutating actions will appear here.</Empty>}</div>
    <div className="notice"><strong>Safety boundary</strong><p>Imported infrastructure is read-only by default. Credentials remain server-side and every write produces an audit event.</p></div>
  </>
}

function Feature({icon, title, children}: {icon: string; title: string; children: React.ReactNode}) {
  return <article className="feature-card"><span className="feature-icon">{icon}</span><div><h3>{title}</h3><p>{children}</p></div><span className="planned">Planned</span></article>
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
