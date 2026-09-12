import {useMemo, useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatDate, readSchemaValues} from '../lib'
import type {Application, ExperimentConfiguration, JsonSchema, ManagedResource, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {SchemaFields} from './SchemaFields'

interface Props {
  items: ExperimentConfiguration[]
  clusters: ManagedResource[]
  workspaces: Workspace[]
  applications: Application[]
  plugins: Plugin[]
  session: Session
  isAdmin: boolean
  changed: () => Promise<void>
}

type ComponentDraft = {key: number; pluginId: string; capability: string}

export function ExperimentConfigurationsView(props: Props) {
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const ready = props.clusters.some(item => item.status === 'ready') && props.applications.length > 0
  const clone = async (item: ExperimentConfiguration) => {
    const name = window.prompt('Name for the cloned configuration', `${item.name} copy`)
    if (!name) return
    try {
      await request(`/experiment-configurations/${item.id}/clone`, {method: 'POST', body: JSON.stringify({name})}, props.session.csrfToken)
      setError('')
      await props.changed()
    } catch (cause) {
      setError(message(cause))
    }
  }
  const remove = async (item: ExperimentConfiguration) => {
    if (!window.confirm(`Delete experiment configuration ${item.name}?`)) return
    try {
      await request(`/experiment-configurations/${item.id}`, {method: 'DELETE'}, props.session.csrfToken)
      setError('')
      await props.changed()
    } catch (cause) {
      setError(message(cause))
    }
  }
  return <>
    <div className="section-heading"><div><p className="eyebrow">CONFIGURE ONCE</p><h2>Experiment configurations</h2></div>{props.isAdmin && <button className="button primary" disabled={!ready} onClick={() => setCreating(true)}>New configuration</button>}</div>
    <p className="section-copy infrastructure-copy">Select a cluster and an application, then compose only the capabilities you want to test. Empty capability groups use the Kubernetes defaults.</p>
    <div className="stats-grid infrastructure-stats"><article className="stat-card"><span>Configurations</span><strong>{props.items.length}</strong><small>reusable definitions</small></article><article className="stat-card"><span>Ready clusters</span><strong>{props.clusters.filter(item => item.status === 'ready').length}</strong><small>validated targets</small></article><article className="stat-card"><span>Capabilities</span><strong>{experimentPlugins(props.plugins).length}</strong><small>compatible plugins</small></article></div>
    {error && <p className="form-error page-error">{error}</p>}
    <div className="managed-grid experiment-config-grid">{props.items.length ? props.items.map(item => {
      const cluster = props.clusters.find(clusterItem => clusterItem.id === item.clusterResourceId)
      const application = props.applications.find(applicationItem => applicationItem.reference === item.applicationRef)
      const components = item.definition.components ?? []
      return <article className="managed-card experiment-config-card" key={item.id}>
        <div className="managed-card-head"><span className="feature-icon">▶</span><Status value={item.validation.valid ? 'validated' : 'invalid'} /></div>
        <h3>{item.name}</h3><p>{item.description || 'Reusable experiment configuration'}</p>
        <dl><div><dt>Cluster</dt><dd>{cluster?.name ?? 'Unavailable'}</dd></div><div><dt>Application</dt><dd>{application?.name ?? item.applicationRef}</dd></div><div><dt>Components</dt><dd>{components.length || 'Kubernetes defaults'}</dd></div><div><dt>Validated</dt><dd>{formatDate(item.validation.checkedAt ?? item.updatedAt)}</dd></div></dl>
        {!!components.length && <div className="trait-list">{components.slice(0, 5).map(component => <span key={component.id}>{component.capability}</span>)}</div>}
        <div className="managed-actions"><button className="button secondary compact" onClick={() => clone(item)}>Clone</button>{props.isAdmin && <button className="button danger compact" onClick={() => remove(item)}>Delete</button>}</div>
      </article>
    }) : <div className="empty-state"><div><strong>No experiment configuration</strong>Create one from a ready cluster. Plugin inputs and application settings are validated before it is saved.</div></div>}</div>
    <ExperimentConfigurationDialog {...props} open={creating} close={() => setCreating(false)} />
  </>
}

function ExperimentConfigurationDialog({open, close, clusters, workspaces, applications, plugins, session, changed}: Props & {open: boolean; close: () => void}) {
  const compatiblePlugins = useMemo(() => experimentPlugins(plugins), [plugins])
  const [workspaceId, setWorkspaceId] = useState(workspaces[0]?.id ?? '')
  const availableClusters = clusters.filter(item => item.status === 'ready' && item.workspaceId === workspaceId)
  const [applicationRef, setApplicationRef] = useState(applications[0]?.reference ?? '')
  const [components, setComponents] = useState<ComponentDraft[]>([])
  const [sequence, setSequence] = useState(1)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const application = applications.find(item => item.reference === applicationRef)
  const addComponent = () => {
    const plugin = compatiblePlugins[0]
    if (!plugin) return
    const capabilities = primaryCapabilities(plugin)
    setComponents(items => [...items, {key: sequence, pluginId: plugin.id, capability: capabilities[0] ?? ''}])
    setSequence(value => value + 1)
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const form = event.currentTarget
    const values = new FormData(form)
    try {
      await request('/experiment-configurations', {method: 'POST', body: JSON.stringify({
        workspaceId,
        clusterResourceId: values.get('clusterResourceId'),
        name: values.get('name'),
        description: values.get('description'),
        applicationRef,
        definition: {
          applicationValues: readSchemaValues(form, application?.descriptor.spec?.valuesSchema ?? emptySchema, 'applicationValues', applications),
          components: components.map((component, index) => {
            const plugin = compatiblePlugins.find(item => item.id === component.pluginId)!
            return {id: `component-${index + 1}`, pluginId: component.pluginId, capability: component.capability, configuration: readSchemaValues(form, configurableSchema(plugin.schema), `component-${component.key}`, applications), targets: {include: split(values.get(`include-${component.key}`)), exclude: split(values.get(`exclude-${component.key}`))}}
          })
        }
      })}, session.csrfToken)
      close()
      setComponents([])
      await changed()
    } catch (cause) {
      setError(message(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Create experiment configuration" eyebrow="VALIDATE BEFORE RUN" className="template-modal experiment-modal">
    <form onSubmit={submit}>
      <div className="form-section"><strong>Environment</strong><p>The cluster must be fully ready. Health is checked again before every run and every step.</p></div>
      {workspaces.length > 1 ? <label>Environment<select value={workspaceId} onChange={event => setWorkspaceId(event.target.value)} required>{workspaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label> : <input type="hidden" value={workspaceId} readOnly />}
      <label>Kubernetes cluster<select name="clusterResourceId" required>{availableClusters.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
      <div className="field-row"><label>Name<input name="name" maxLength={120} placeholder="Scheduler baseline" required autoFocus /></label><label>Application<select value={applicationRef} onChange={event => setApplicationRef(event.target.value)} required>{applications.map(item => <option key={item.reference} value={item.reference}>{item.name} · {item.version}</option>)}</select></label></div>
      <label>Description<textarea name="description" rows={2} maxLength={1000} placeholder="What this configuration is intended to measure" /></label>
      <div className="form-section"><strong>Application settings</strong><p>The catalog contract supplies these fields; importing another application requires no KubePhos code change.</p></div>
      {application && <SchemaFields schema={application.descriptor.spec?.valuesSchema ?? emptySchema} prefix="applicationValues" values={application.descriptor.spec?.defaults ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} />}
      <div className="form-section pool-section"><div><strong>Experiment capabilities</strong><p>Add scheduler, autoscaler, descheduler, chaos, load or metrics plugins as needed. No scheduler means the default scheduler.</p></div><button className="button secondary compact" type="button" disabled={!compatiblePlugins.length || components.length >= 24} onClick={addComponent}>Add capability</button></div>
      <div className="experiment-component-list">{components.map(component => {
        const plugin = compatiblePlugins.find(item => item.id === component.pluginId) ?? compatiblePlugins[0]
        const capabilities = plugin ? primaryCapabilities(plugin) : []
        const componentIds = application?.descriptor.spec?.interface?.components?.map(item => item.id).join(', ') ?? ''
        return <section className="experiment-component" key={component.key}>
          <div className="experiment-component-head"><strong>Capability {components.indexOf(component) + 1}</strong><button type="button" className="icon-button" aria-label="Remove capability" onClick={() => setComponents(items => items.filter(item => item.key !== component.key))}>×</button></div>
          <div className="field-row"><label>Plugin<select value={plugin?.id ?? ''} onChange={event => {const selected = compatiblePlugins.find(item => item.id === event.target.value)!; setComponents(items => items.map(item => item.key === component.key ? {...item, pluginId: selected.id, capability: primaryCapabilities(selected)[0] ?? ''} : item))}}>{compatiblePlugins.map(item => <option key={item.id} value={item.id}>{item.name} · {item.version}</option>)}</select></label><label>Capability<select value={component.capability} onChange={event => setComponents(items => items.map(item => item.key === component.key ? {...item, capability: event.target.value} : item))}>{capabilities.map(capability => <option key={capability}>{capability}</option>)}</select></label></div>
          {plugin && <SchemaFields schema={configurableSchema(plugin.schema)} prefix={`component-${component.key}`} applications={applications} artifacts={[]} connections={[]} credentials={[]} />}
          <details className="advanced-fields"><summary>Workload targets</summary><p>Leave both fields empty to use every compatible component. Available: {componentIds || 'declared by the application at runtime'}.</p><div className="field-row"><label>Only these components<input name={`include-${component.key}`} placeholder="frontend, checkoutservice" /></label><label>Exclude components<input name={`exclude-${component.key}`} placeholder="emailservice" /></label></div></details>
        </section>
      })}</div>
      <div className="validation-callout"><span>✓</span><p>Application schema, plugin versions, capabilities, parameters, targets, cluster ownership and readiness are validated now and snapshotted.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !workspaceId || !availableClusters.length || !application}>{pending ? 'Validating…' : 'Validate and save'}</button></div>
    </form>
  </Dialog>
}

const emptySchema: JsonSchema = {type: 'object', properties: {}}

export function configurableSchema(schema: JsonSchema): JsonSchema {
  const properties = Object.fromEntries(Object.entries(schema.properties ?? {}).filter(([, property]) => property.format !== 'kubephos-artifact-ref' && property.format !== 'kubephos-application-ref'))
  return {type: 'object', properties, required: (schema.required ?? []).filter(name => name in properties)}
}

export function primaryCapabilities(plugin: Plugin): string[] {
  return (plugin.capabilities ?? []).filter(capability => capability !== 'lifecycle.cleanup' && !capability.endsWith('.preflight') && !capability.endsWith('.cleanup'))
}

export function experimentPlugins(plugins: Plugin[]): Plugin[] {
  const prefixes = ['scheduler.', 'descheduler.', 'autoscaler.', 'chaos.', 'load.', 'metrics.']
  return plugins.filter(plugin => {
    const capabilities = primaryCapabilities(plugin)
    const relevantInput = (plugin.artifactInputs ?? []).some(input => ['ApplicationDeployment', 'TargetBinding', 'WorkloadTargets', 'LoadProfileSet'].includes(input.type))
    return capabilities.some(capability => prefixes.some(prefix => capability.startsWith(prefix))) || relevantInput
  })
}

function split(value: FormDataEntryValue | null): string[] {
  return [...new Set(String(value ?? '').split(',').map(item => item.trim()).filter(Boolean))]
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : 'The request could not be completed.'
}

function Status({value}: {value: string}) {
  return <span className={`status ${value.toLowerCase()}`}>{value}</span>
}
