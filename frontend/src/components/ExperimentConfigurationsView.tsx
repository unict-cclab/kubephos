import {useEffect, useMemo, useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatDate, readSchemaValues} from '../lib'
import type {Application, Experiment, ExperimentConfiguration, JsonSchema, ManagedResource, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {SchemaFields} from './SchemaFields'

interface Props {
  items: ExperimentConfiguration[]
  experiments: Experiment[]
  clusters: ManagedResource[]
  workspaces: Workspace[]
  applications: Application[]
  plugins: Plugin[]
  session: Session
  isAdmin: boolean
  changed: () => Promise<void>
  openOperation: (id: string) => void
}

type ComponentDraft = {key: number; pluginId: string; capability: string}

export function ExperimentConfigurationsView(props: Props) {
  const [creating, setCreating] = useState(false)
  const [running, setRunning] = useState<ExperimentConfiguration | null>(null)
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
    <p className="section-copy infrastructure-copy">Choose the cluster, application, network conditions, monitoring and load profile. Advanced components are optional.</p>
    <div className="stats-grid infrastructure-stats"><article className="stat-card"><span>Configurations</span><strong>{props.items.length}</strong><small>reusable definitions</small></article><article className="stat-card"><span>Ready clusters</span><strong>{props.clusters.filter(item => item.status === 'ready').length}</strong><small>validated targets</small></article><article className="stat-card"><span>Experiment tools</span><strong>{experimentPlugins(props.plugins).length}</strong><small>available implementations</small></article></div>
    {error && <p className="form-error page-error">{error}</p>}
    <div className="managed-grid experiment-config-grid">{props.items.length ? props.items.map(item => {
      const cluster = props.clusters.find(clusterItem => clusterItem.id === item.clusterResourceId)
      const application = props.applications.find(applicationItem => applicationItem.reference === item.applicationRef)
      const components = item.definition.components ?? []
      const instances = props.experiments.filter(experiment => experiment.configurationId === item.id)
      return <article className="managed-card experiment-config-card" key={item.id}>
        <div className="managed-card-head"><span className="feature-icon">▶</span><Status value={item.validation.valid ? 'validated' : 'invalid'} /></div>
        <h3>{item.name}</h3><p>{item.description || 'Reusable experiment configuration'}</p>
        <dl><div><dt>Cluster</dt><dd>{cluster?.name ?? 'Unavailable'}</dd></div><div><dt>Application</dt><dd>{application?.name ?? item.applicationRef}</dd></div><div><dt>Components</dt><dd>{components.length || 'Kubernetes defaults'}</dd></div><div><dt>Validated</dt><dd>{formatDate(item.validation.checkedAt ?? item.updatedAt)}</dd></div></dl>
        {!!components.length && <div className="trait-list">{components.slice(0, 5).map(component => <span key={component.id}>{componentTitle(component.capability)}</span>)}</div>}
        {!!instances.length && <details className="advanced-fields"><summary>{instances.length} instance{instances.length === 1 ? '' : 's'}</summary><div className="instance-list">{instances.slice(0, 5).map(instance => <div className="instance-row" key={instance.id}><span><strong>{instance.name}</strong><small>{instance.variants[0]?.trials.length ?? 0} sequential runs · {formatDate(instance.createdAt)}</small></span><Status value={instance.status} />{instance.variants[0]?.trials.find(trial => trial.operationId)?.operationId && <button className="button secondary compact" onClick={() => props.openOperation(instance.variants[0].trials.find(trial => trial.operationId)!.operationId)}>Logs</button>}</div>)}</div></details>}
        <div className="managed-actions">{props.isAdmin && <button className="button primary compact" onClick={() => setRunning(item)}>Run</button>}<button className="button secondary compact" onClick={() => clone(item)}>Clone</button>{props.isAdmin && <button className="button danger compact" disabled={instances.length > 0} title={instances.length ? 'Used configurations are retained for reproducibility' : ''} onClick={() => remove(item)}>Delete</button>}</div>
      </article>
    }) : <div className="empty-state"><div><strong>No experiment configuration</strong>Create one from a ready cluster. Plugin inputs and application settings are validated before it is saved.</div></div>}</div>
    <ExperimentConfigurationDialog {...props} open={creating} close={() => setCreating(false)} />
    <ExperimentInstanceDialog item={running} close={() => setRunning(null)} session={props.session} changed={props.changed} />
  </>
}

export function ExperimentConfigurationDialog({open, close, clusters, workspaces, applications, plugins, session, changed}: Props & {open: boolean; close: () => void}) {
  const compatiblePlugins = useMemo(() => experimentPlugins(plugins), [plugins])
  const advancedPlugins = compatiblePlugins.filter(item => primaryCapabilities(item).some(capability => !['load.', 'metrics.', 'monitoring.', 'chaos.'].some(prefix => capability.startsWith(prefix))))
  const [workspaceId, setWorkspaceId] = useState(workspaces[0]?.id ?? '')
  const availableClusters = clusters.filter(item => item.status === 'ready' && item.workspaceId === workspaceId)
  const [clusterResourceId, setClusterResourceId] = useState('')
  const [applicationRef, setApplicationRef] = useState(applications[0]?.reference ?? '')
  const [components, setComponents] = useState<ComponentDraft[]>([])
  const [sequence, setSequence] = useState(1)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const application = applications.find(item => item.reference === applicationRef)
  const selectedCluster = clusters.find(item => item.id === clusterResourceId)
  const zones = applicationZones(selectedCluster)
  const loadComponent = components.find(item => item.capability.startsWith('load.'))
  const metricsComponent = components.find(item => item.capability.startsWith('metrics.'))
  const networkComponent = components.find(item => item.capability.startsWith('chaos.'))
  const networkPlugin = compatiblePlugins.find(item => primaryCapabilities(item).some(capability => capability.startsWith('chaos.')))
  const monAgentComponent = components.find(item => item.capability.startsWith('monitoring.'))
  const monAgentPlugin = compatiblePlugins.find(item => primaryCapabilities(item).some(capability => capability.startsWith('monitoring.')))
  const managedNetworkAvailable = typeof selectedCluster?.spec?.managedProfile === 'string' && selectedCluster.spec.managedProfile.length > 0
  const advancedComponents = components.filter(item => !['load.', 'metrics.', 'monitoring.', 'chaos.'].some(prefix => item.capability.startsWith(prefix)))
  useEffect(() => {
	if (!open) return
	setWorkspaceId(current => workspaces.some(item => item.id === current) ? current : workspaces[0]?.id ?? '')
	setApplicationRef(current => applications.some(item => item.reference === current) ? current : applications[0]?.reference ?? '')
  }, [open, workspaces, applications])
  useEffect(() => {
	if (!open) return
	setClusterResourceId(current => availableClusters.some(item => item.id === current) ? current : availableClusters[0]?.id ?? '')
  }, [open, workspaceId, clusters])
  useEffect(() => {
	if (!open) return
	setComponents(items => items.filter(item => !(item.capability.startsWith('chaos.') && (zones.length < 2 || !managedNetworkAvailable)) && !(item.capability.startsWith('monitoring.') && !managedNetworkAvailable)))
  }, [open, clusterResourceId, zones.length, managedNetworkAvailable])
  useEffect(() => {
	if (!open || components.length) return
	const defaults = ['load.', 'metrics.'].flatMap(prefix => {
	  const plugin = compatiblePlugins.find(item => primaryCapabilities(item).some(capability => capability.startsWith(prefix)))
	  const capability = plugin && primaryCapabilities(plugin).find(value => value.startsWith(prefix))
	  return plugin && capability ? [{key: sequence + (prefix === 'metrics.' ? 1 : 0), pluginId: plugin.id, capability}] : []
	})
	setComponents(defaults)
	setSequence(value => value + defaults.length)
  }, [open, compatiblePlugins, components.length, sequence])
  const addAdvancedComponent = () => {
	const plugin = advancedPlugins.find(item => !components.some(component => component.pluginId === item.id)) ?? advancedPlugins[0]
    if (!plugin) return
    const capabilities = primaryCapabilities(plugin)
    setComponents(items => {
      const next = {key: sequence, pluginId: plugin.id, capability: capabilities[0] ?? ''}
	  const load = items.findIndex(item => item.capability.startsWith('load.'))
	  return load < 0 ? [...items, next] : [...items.slice(0, load), next, ...items.slice(load)]
    })
    setSequence(value => value + 1)
  }
  const setNetworkEnabled = (enabled: boolean) => {
    if (!enabled) {
      setComponents(items => items.filter(item => !item.capability.startsWith('chaos.')))
      return
    }
    if (!networkPlugin || networkComponent) return
    const capability = primaryCapabilities(networkPlugin).find(item => item.startsWith('chaos.')) ?? ''
    setComponents(items => {
      const next = {key: sequence, pluginId: networkPlugin.id, capability}
      const load = items.findIndex(item => item.capability.startsWith('load.'))
      return load < 0 ? [...items, next] : [...items.slice(0, load), next, ...items.slice(load)]
    })
    setSequence(value => value + 1)
  }
  const setMonAgentEnabled = (enabled: boolean) => {
    if (!enabled) {
      setComponents(items => items.filter(item => !item.capability.startsWith('monitoring.')))
      return
    }
    if (!monAgentPlugin || monAgentComponent) return
    const capability = primaryCapabilities(monAgentPlugin).find(item => item.startsWith('monitoring.')) ?? ''
    setComponents(items => {
      const next = {key: sequence, pluginId: monAgentPlugin.id, capability}
      const load = items.findIndex(item => item.capability.startsWith('load.'))
      return load < 0 ? [...items, next] : [...items.slice(0, load), next, ...items.slice(load)]
    })
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
        clusterResourceId,
        name: values.get('name'),
        description: values.get('description'),
        applicationRef,
        definition: {
          applicationValues: readSchemaValues(form, application?.descriptor.spec?.valuesSchema ?? emptySchema, 'applicationValues', applications),
          components: components.map((component, index) => {
            const plugin = compatiblePlugins.find(item => item.id === component.pluginId)!
            const configuration = readSchemaValues(form, experimentSchema(plugin.schema, component.capability), `component-${component.key}`, applications)
			if (component.capability.startsWith('load.')) configuration.action = 'start'
			if (component.capability.startsWith('load.')) configuration.zoneWeights = Object.fromEntries(zones.map(zone => [zone, Number(values.get(`zoneWeight-${component.key}-${zone}`) ?? 1)]))
			return {id: `component-${index + 1}`, pluginId: component.pluginId, capability: component.capability, configuration, targets: {include: split(values.get(`include-${component.key}`)), exclude: split(values.get(`exclude-${component.key}`))}}
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
      <div className="form-section"><strong>1. Cluster</strong><p>Select a ready Kubernetes cluster. Its health is checked again before every run and every step.</p></div>
      {workspaces.length > 1 ? <label>Environment<select value={workspaceId} onChange={event => setWorkspaceId(event.target.value)} required>{workspaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label> : <input type="hidden" value={workspaceId} readOnly />}
      <label>Kubernetes cluster<select name="clusterResourceId" value={clusterResourceId} onChange={event => setClusterResourceId(event.target.value)} required disabled={!availableClusters.length}>{availableClusters.length ? availableClusters.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">No ready cluster in this environment</option>}</select></label>
      <label>Name<input name="name" maxLength={120} placeholder="Scheduler baseline" required autoFocus /></label>
      <label>Description<textarea name="description" rows={2} maxLength={1000} placeholder="What this configuration is intended to measure" /></label>
      <div className="form-section"><strong>2. Application</strong><p>Select the application and change only the values exposed by its catalog contract.</p></div>
      <label>Application<select value={applicationRef} onChange={event => setApplicationRef(event.target.value)} required>{applications.map(item => <option key={item.reference} value={item.reference}>{item.name} · {item.version}</option>)}</select></label>
      {application && <SchemaFields schema={application.descriptor.spec?.valuesSchema ?? emptySchema} prefix="applicationValues" values={application.descriptor.spec?.defaults ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} />}
      <div className="form-section pool-section"><div><strong>3. Network between zones</strong><p>{networkComponent ? 'Traffic crossing application zones will use this network profile.' : !managedNetworkAvailable ? 'This cluster predates the managed network runtime.' : zones.length < 2 ? 'This cluster has fewer than two application zones.' : 'No network conditions will be injected.'}</p></div><label className="checkbox-field"><input type="checkbox" checked={Boolean(networkComponent)} disabled={!networkPlugin || !managedNetworkAvailable || zones.length < 2} onChange={event => setNetworkEnabled(event.target.checked)} /><span>Enable injection</span></label></div>
      {networkComponent && networkPlugin && <section className="experiment-component"><SchemaFields schema={experimentSchema(networkPlugin.schema, networkComponent.capability)} prefix={`component-${networkComponent.key}`} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></section>}
      <div className="form-section"><strong>4. Monitoring</strong><p>Choose the query window and resolution used to collect the run results.</p></div>
      {metricsComponent && (() => {const plugin = compatiblePlugins.find(item => item.id === metricsComponent.pluginId)!; return <section className="experiment-component"><SchemaFields schema={experimentSchema(plugin.schema, metricsComponent.capability)} prefix={`component-${metricsComponent.key}`} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></section>})()}
      <label className="checkbox-field"><input type="checkbox" checked={Boolean(monAgentComponent)} disabled={!monAgentPlugin || !managedNetworkAvailable} onChange={event => setMonAgentEnabled(event.target.checked)} /><span>Customize mon-agent for this experiment</span></label>
      {monAgentComponent && monAgentPlugin && <section className="experiment-component"><SchemaFields schema={experimentSchema(monAgentPlugin.schema, monAgentComponent.capability)} prefix={`component-${monAgentComponent.key}`} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></section>}
      <div className="form-section"><strong>5. Load profile</strong><p>Define the traffic intensity, temporal pattern and distribution across application zones.</p></div>
      {loadComponent && (() => {const plugin = compatiblePlugins.find(item => item.id === loadComponent.pluginId)!; return <section className="experiment-component"><SchemaFields schema={experimentSchema(plugin.schema, loadComponent.capability)} prefix={`component-${loadComponent.key}`} applications={applications} artifacts={[]} connections={[]} credentials={[]} />{zones.length > 0 && <fieldset className="zone-weights"><legend>Geographic distribution</legend><p>Relative traffic weight for each application zone.</p><div className="capacity-grid">{zones.map(zone => <label key={zone}>{zone}<input name={`zoneWeight-${loadComponent.key}-${zone}`} type="number" min={0} max={1000} defaultValue={1} required /></label>)}</div></fieldset>}</section>})()}
      <details className="advanced-fields"><summary>Advanced scheduling and scaling</summary>
        <div className="form-section pool-section"><div><strong>Optional components</strong><p>Add a scheduler, descheduler or autoscaler only when the experiment needs one.</p></div><button className="button secondary compact" type="button" disabled={!advancedPlugins.length || components.length >= 15} onClick={addAdvancedComponent}>Add component</button></div>
      <div className="experiment-component-list">{advancedComponents.map(component => {
        const plugin = compatiblePlugins.find(item => item.id === component.pluginId) ?? compatiblePlugins[0]
        const componentIds = application?.descriptor.spec?.interface?.components?.map(item => item.id).join(', ') ?? ''
        return <section className="experiment-component" key={component.key}>
		  <div className="experiment-component-head"><strong>{componentTitle(component.capability)}</strong><button type="button" className="icon-button" aria-label="Remove component" onClick={() => setComponents(items => items.filter(item => item.key !== component.key))}>×</button></div>
          {plugin && <SchemaFields schema={experimentSchema(plugin.schema, component.capability)} prefix={`component-${component.key}`} applications={applications} artifacts={[]} connections={[]} credentials={[]} />}
          <details className="advanced-fields"><summary>Implementation</summary><label>Component<select value={plugin?.id ?? ''} onChange={event => {const selected = advancedPlugins.find(item => item.id === event.target.value)!; setComponents(items => items.map(item => item.key === component.key ? {...item, pluginId: selected.id, capability: primaryCapabilities(selected)[0] ?? ''} : item))}}>{advancedPlugins.map(item => <option key={item.id} value={item.id}>{item.name} · {item.version}</option>)}</select></label></details>
          <details className="advanced-fields"><summary>Workload targets</summary><p>Leave both fields empty to use every compatible component. Available: {componentIds || 'declared by the application at runtime'}.</p><div className="field-row"><label>Only these components<input name={`include-${component.key}`} placeholder="frontend, checkoutservice" /></label><label>Exclude components<input name={`exclude-${component.key}`} placeholder="emailservice" /></label></div></details>
        </section>
      })}</div></details>
      <div className="validation-callout"><span>✓</span><p>Cluster, application, network, monitoring, load and optional components are validated now and checked again before every run.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !workspaceId || !clusterResourceId || !application}>{pending ? 'Validating…' : 'Validate and save'}</button></div>
    </form>
  </Dialog>
}

function ExperimentInstanceDialog({item, close, session, changed}: {item: ExperimentConfiguration | null; close: () => void; session: Session; changed: () => Promise<void>}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!item) return
    const values = new FormData(event.currentTarget)
    const scheduled = String(values.get('scheduledFor') ?? '')
    setPending(true)
    setError('')
    try {
      await request('/experiment-instances', {method: 'POST', body: JSON.stringify({configurationId: item.id, name: values.get('name'), runs: Number(values.get('runs')), scheduledFor: scheduled ? new Date(scheduled).toISOString() : undefined})}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      setError(message(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={Boolean(item)} onClose={close} title="Run experiment" eyebrow="SEQUENTIAL AND REPEATABLE" className="template-modal">
    <form onSubmit={submit}>
      <div className="form-section"><strong>{item?.name}</strong><p>Each run starts only after the previous run and its verified reset have completed.</p></div>
      <label>Instance name<input name="name" maxLength={120} defaultValue={item ? `${item.name} run` : ''} required autoFocus /></label>
      <div className="field-row"><label>Number of runs<input name="runs" type="number" min={1} max={20} defaultValue={3} required /></label><label>Start later (optional)<input name="scheduledFor" type="datetime-local" /></label></div>
      <div className="validation-callout"><span>✓</span><p>The cluster, application digest, plugin versions, complete pipeline and every runtime dependency are checked again before execution.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Validating…' : 'Validate and run'}</button></div>
    </form>
  </Dialog>
}

const emptySchema: JsonSchema = {type: 'object', properties: {}}

export function configurableSchema(schema: JsonSchema): JsonSchema {
  const properties = Object.fromEntries(Object.entries(schema.properties ?? {}).filter(([, property]) => property.format !== 'kubephos-artifact-ref' && property.format !== 'kubephos-application-ref'))
  return {type: 'object', properties, required: (schema.required ?? []).filter(name => name in properties)}
}

function experimentSchema(schema: JsonSchema, capability: string): JsonSchema {
  const result = configurableSchema(schema)
  if (!capability.startsWith('load.')) return result
  const hidden = new Set(['action', 'zoneWeights'])
  const properties = Object.fromEntries(Object.entries(result.properties ?? {}).filter(([name]) => !hidden.has(name)))
  return {...result, properties, required: (result.required ?? []).filter(name => !hidden.has(name))}
}

export function applicationZones(cluster?: ManagedResource): string[] {
  const pools = cluster?.spec?.applicationPools
  if (!Array.isArray(pools)) return []
  const values = pools.flatMap(pool => pool && typeof pool === 'object' && 'zones' in pool && Array.isArray(pool.zones) ? pool.zones : [])
  return [...new Set(values.filter((zone): zone is string => typeof zone === 'string' && zone.length > 0))].sort()
}

function componentTitle(capability: string): string {
  if (capability.startsWith('load.')) return 'Load profile'
  if (capability.startsWith('metrics.')) return 'Monitoring'
  if (capability.startsWith('monitoring.')) return 'Mon-agent profile'
  if (capability.startsWith('chaos.')) return 'Network injection between zones'
  if (capability.startsWith('scheduler.')) return 'Scheduler'
  if (capability.startsWith('descheduler.')) return 'Descheduler'
  if (capability.startsWith('autoscaler.')) return 'Autoscaler'
  return 'Optional component'
}

export function primaryCapabilities(plugin: Plugin): string[] {
  return (plugin.capabilities ?? []).filter(capability => capability !== 'lifecycle.cleanup' && !capability.endsWith('.preflight') && !capability.endsWith('.cleanup'))
}

export function experimentPlugins(plugins: Plugin[]): Plugin[] {
  const prefixes = ['scheduler.', 'descheduler.', 'autoscaler.', 'chaos.', 'load.', 'metrics.', 'monitoring.']
  return plugins.filter(plugin => {
    const capabilities = primaryCapabilities(plugin)
	return capabilities.some(capability => prefixes.some(prefix => capability.startsWith(prefix)))
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
