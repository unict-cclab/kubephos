import {useEffect, useMemo, useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatDate, readSchemaValues} from '../lib'
import type {Application, Artifact, CatalogStrategy, Experiment, ExperimentConfiguration, JsonSchema, ManagedResource, Operation, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {AutoscalerTargetEditor} from './AutoscalerTargetEditor'
import {LoadProfileEditor, LoadProfilePreview, loadProfileSteps, type LoadProfileStep} from './LoadProfileEditor'
import {NetworkInjectionEditor} from './NetworkInjectionEditor'
import {SchemaFields} from './SchemaFields'
import {ResultsView} from './ResultsView'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {useDetailRoute} from '../detailRoute'
import {InputData} from './InputData'
import {SectionIcon} from './SectionIcon'
import {ResourceAction} from './ResourceAction'

interface Props {
  items: ExperimentConfiguration[]
  experiments: Experiment[]
  artifacts?: Artifact[]
  operations?: Operation[]
  clusters: ManagedResource[]
  workspaces: Workspace[]
  applications: Application[]
  plugins: Plugin[]
  strategies: CatalogStrategy[]
  session: Session
  isAdmin: boolean
  changed: () => Promise<void>
  openOperation: (id: string) => void
  openExperiment?: (id: string) => void
}

type ComponentDraft = {key: number; pluginId: string; capability: string; strategyId?: string; configuration?: Record<string, unknown>; targets?: {include: string[]; exclude: string[]}}

export function ExperimentConfigurationsView(props: Props) {
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState(false)
  const [viewing, setViewing] = useState(false)
  const [running, setRunning] = useState<ExperimentConfiguration | null>(null)
  const detail = useDetailRoute('experiments')
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
    if (!window.confirm(`Delete experiment configuration ${item.name}? Existing experiment instances, runs, logs and results will be retained.`)) return
    try {
      await request(`/experiment-configurations/${item.id}`, {method: 'DELETE'}, props.session.csrfToken)
      setError('')
      await props.changed()
    } catch (cause) {
      setError(message(cause))
    }
  }
  const selectedExperiment = detail.route?.kind === 'instance' ? props.experiments.find(item => item.id === detail.route?.id) : undefined
  const selectedConfiguration = detail.route?.kind === 'configuration' ? props.items.find(item => item.id === detail.route?.id) : undefined
  if (selectedExperiment) return <ResultsView artifacts={props.artifacts ?? []} experiments={props.experiments} operations={props.operations ?? []} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} focusedExperiment={selectedExperiment} onBack={detail.close} />
  if (selectedConfiguration && viewing) return <ExperimentConfigurationDialog {...props} item={selectedConfiguration} readOnly open close={() => setViewing(false)} />
  if (selectedConfiguration && editing) return <ExperimentConfigurationDialog {...props} item={selectedConfiguration} open close={() => setEditing(false)} />
  if (selectedConfiguration) return <><ExperimentConfigurationDetailsPage item={selectedConfiguration} clusters={props.clusters} applications={props.applications} plugins={props.plugins} close={detail.close} canRun={props.isAdmin} run={() => setRunning(selectedConfiguration)} view={() => setViewing(true)} edit={() => setEditing(true)} clone={() => void clone(selectedConfiguration)} />{error && <p className="form-error page-error">{error}</p>}<ExperimentInstanceDialog item={running} close={() => setRunning(null)} session={props.session} changed={props.changed} onCreated={props.openExperiment ?? (() => {})} /></>
  return <>
    <div className="section-heading"><div><SectionIcon kind="configurations" /><h2>Configurations</h2><p className="section-copy">{props.items.length} reusable setups</p></div>{props.isAdmin && <ResourceAction action="create" label="New configuration" disabled={!ready} onClick={() => setCreating(true)} />}</div>
    {error && <p className="form-error page-error">{error}</p>}
    <div className="managed-grid experiment-config-grid">{props.items.length ? props.items.map(item => {
      const cluster = props.clusters.find(clusterItem => clusterItem.id === item.clusterResourceId)
      const application = props.applications.find(applicationItem => applicationItem.reference === item.applicationRef)
      const components = item.definition.components ?? []
      const instances = props.experiments.filter(experiment => experiment.configurationId === item.id)
      return <article className="managed-card experiment-config-card" key={item.id}>
        <div className="managed-card-head"><span className="feature-icon">▶</span><Status value={item.validation.valid ? 'validated' : 'invalid'} /></div>
        <h3>{item.name}</h3><p>{cluster?.name ?? 'Unavailable'} · {application?.name ?? item.applicationRef}</p>
        <div className="config-card-meta"><span>{instances.length} experiments</span><span>Updated {formatDate(item.updatedAt)}</span></div>
        <div className="managed-actions"><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('configuration', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} onClick={() => void remove(item)} />}</div>
      </article>
    }) : <div className="empty-state"><div><strong>No experiment configuration</strong>Create one from a ready cluster and application.</div></div>}</div>
    <ExperimentConfigurationDialog {...props} open={creating} close={() => setCreating(false)} />
    <ExperimentInstanceDialog item={running} close={() => setRunning(null)} session={props.session} changed={props.changed} onCreated={props.openExperiment ?? (() => {})} />
  </>
}

function ExperimentConfigurationDetailsPage({item, clusters, applications, plugins, close, canRun, run, view, edit, clone}: {item: ExperimentConfiguration; clusters: ManagedResource[]; applications: Application[]; plugins: Plugin[]; close: () => void; canRun: boolean; run: () => void; view: () => void; edit: () => void; clone: () => void}) {
  const cluster = clusters.find(value => value.id === item.clusterResourceId)
  const application = applications.find(value => value.reference === item.applicationRef)
  const components = item.definition.components ?? []
  const load = components.find(component => component.capability.startsWith('load.'))
  const steps = loadProfileSteps(load?.configuration.steps)
  const geographic = Array.isArray(load?.configuration.geographicSteps) ? load.configuration.geographicSteps : []
  return <div className="detail-page"><DetailBreadcrumb parent="Configurations" current={item.name} back={close} /><div className="detail-page-heading"><div><h2>{item.name}</h2>{item.description && <p>{item.description}</p>}</div><div className="detail-page-actions">{canRun && <button className="button primary" disabled={!item.validation.valid} onClick={run}>Run experiment</button>}<button className="button secondary" onClick={view}>View configuration</button>{canRun && <button className="button secondary" onClick={edit}>Edit configuration</button>}<button className="button secondary" onClick={clone}>Clone configuration</button><Status value={item.validation.valid ? 'validated' : 'invalid'} /></div></div><article className="detail-page-card">
    <div className="details-summary">
      <div><span>Cluster</span><strong>{cluster?.name ?? item.clusterResourceId}</strong></div>
      <div><span>Application</span><strong>{application?.name ?? item.applicationRef}</strong></div>
      <div><span>Validation</span><strong>{item.validation.valid ? 'Validated' : 'Invalid'}</strong></div>
      <div><span>Load phases</span><strong>{steps.length}</strong></div>
      <div><span>Created</span><strong>{formatDate(item.createdAt)}</strong></div>
      <div><span>Updated</span><strong>{formatDate(item.updatedAt)}</strong></div>
    </div>
    {item.description && <section className="details-section"><div className="details-heading"><strong>Description</strong></div><p className="details-copy">{item.description}</p></section>}
    <section className="details-section"><div className="details-heading"><strong>Input load</strong><span>{steps.length} load phase{steps.length === 1 ? '' : 's'} · {geographic.length} zone phase{geographic.length === 1 ? '' : 's'}</span></div>{steps.length ? <><LoadProfilePreview steps={steps} /><div className="configuration-phase-list"><details><summary>Load phases</summary>{steps.map((step, index) => <div key={index} className="configuration-phase"><strong>Phase {index + 1}</strong><InputData value={loadPhaseFields(step)} /></div>)}</details>{geographic.length > 0 && <details><summary>Traffic by zone</summary>{geographic.map((phase, index) => <div key={index} className="configuration-phase"><strong>Phase {index + 1}</strong><InputData value={geographicPhaseFields(phase)} /></div>)}</details>}</div></> : <p className="details-copy">No input load is configured.</p>}</section>
    <section className="details-section"><div className="details-heading"><strong>Setup</strong></div><div className="configuration-component-list">{components.map(component => {
      const plugin = plugins.find(value => value.id === component.pluginId)
      const targets = component.targets?.include?.length ? component.targets.include.join(', ') : `All compatible${component.targets?.exclude?.length ? ` except ${component.targets.exclude.join(', ')}` : ''}`
      const settings = Object.fromEntries(Object.entries(component.configuration ?? {}).filter(([name]) => !['steps', 'geographicSteps'].includes(name)))
      return <details key={component.id}><summary><span><strong>{componentTitle(component.capability)}</strong><small>{plugin?.name ?? 'Installed component'}</small></span><span>{targets}</span></summary>{Object.keys(settings).length ? <InputData value={settings} /> : <p className="details-copy">Default settings</p>}</details>
    })}</div></section>
    {!!Object.keys(item.definition.applicationValues ?? {}).length && <section className="details-section"><div className="details-heading"><strong>Application settings</strong></div><InputData value={item.definition.applicationValues} /></section>}
  </article></div>
}

function loadPhaseFields(step: LoadProfileStep): Record<string, unknown> {
  if (step.type === 'constant') return {type: step.type, durationSeconds: step.durationSeconds, rps: step.rps}
  if (step.type === 'sinusoidal') return {type: step.type, durationSeconds: step.durationSeconds, baselineRps: step.baselineRps, amplitudeRps: step.amplitudeRps, periodSeconds: step.periodSeconds, phaseSeconds: step.phaseSeconds}
  return {type: step.type, durationSeconds: step.durationSeconds, startRps: step.startRps, endRps: step.endRps, curve: step.curve}
}

function geographicPhaseFields(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object') return {phase: value}
  const phase = value as Record<string, unknown>
  return phase.type === 'linear' ? {type: phase.type, durationSeconds: phase.durationSeconds, startWeights: phase.startWeights, endWeights: phase.endWeights} : {type: phase.type, durationSeconds: phase.durationSeconds, weights: phase.weights}
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

export function ExperimentConfigurationDialog({open, close, clusters, workspaces, applications, plugins, strategies, session, changed, item, readOnly = false}: Props & {open: boolean; close: () => void; item?: ExperimentConfiguration; readOnly?: boolean}) {
  const compatiblePlugins = useMemo(() => experimentPlugins(plugins), [plugins])
  const advancedPlugins = compatiblePlugins.filter(item => primaryCapabilities(item).some(capability => !['load.', 'metrics.', 'monitoring.', 'chaos.'].some(prefix => capability.startsWith(prefix))))
  const initialComponents = () => (item?.definition.components ?? []).map((component, index) => ({key: index + 1, pluginId: component.pluginId, capability: component.capability, strategyId: component.strategyId, configuration: component.configuration ?? {}, targets: component.targets ?? {include: [], exclude: []}}))
  const [workspaceId, setWorkspaceId] = useState(item?.workspaceId ?? workspaces[0]?.id ?? '')
  const availableClusters = clusters.filter(cluster => cluster.workspaceId === workspaceId && (cluster.status === 'ready' || cluster.id === item?.clusterResourceId))
  const [clusterResourceId, setClusterResourceId] = useState(item?.clusterResourceId ?? '')
  const [applicationRef, setApplicationRef] = useState(item?.applicationRef ?? applications[0]?.reference ?? '')
  const savedLoad = item?.definition.components?.find(component => component.capability.startsWith('load.'))
  const [loadScenarioId, setLoadScenarioId] = useState(typeof savedLoad?.configuration.scenarioId === 'string' ? savedLoad.configuration.scenarioId : applications[0]?.descriptor.spec?.interface?.loadScenarios?.[0]?.id ?? '')
  const [components, setComponents] = useState<ComponentDraft[]>(initialComponents)
  const [sequence, setSequence] = useState((item?.definition.components?.length ?? 0) + 1)
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
	if (item) {
	  const drafts = (item.definition.components ?? []).map((component, index) => ({key: index + 1, pluginId: component.pluginId, capability: component.capability, strategyId: component.strategyId, configuration: component.configuration ?? {}, targets: component.targets ?? {include: [], exclude: []}}))
	  setWorkspaceId(item.workspaceId)
	  setClusterResourceId(item.clusterResourceId)
	  setApplicationRef(item.applicationRef)
	  setComponents(drafts)
	  setSequence(drafts.length + 1)
	  const load = drafts.find(component => component.capability.startsWith('load.'))
	  setLoadScenarioId(typeof load?.configuration?.scenarioId === 'string' ? load.configuration.scenarioId : '')
	  return
	}
	setWorkspaceId(workspaces[0]?.id ?? '')
	setApplicationRef(applications[0]?.reference ?? '')
	setClusterResourceId('')
	setComponents([])
	setSequence(1)
  }, [open, item?.id])
  useEffect(() => {
	if (!open || item) return
	setWorkspaceId(current => workspaces.some(workspace => workspace.id === current) ? current : workspaces[0]?.id ?? '')
	setApplicationRef(current => applications.some(application => application.reference === current) ? current : applications[0]?.reference ?? '')
  }, [open, item, workspaces, applications])
  useEffect(() => {
	if (!open) return
	const scenarios = application?.descriptor.spec?.interface?.loadScenarios ?? []
	setLoadScenarioId(current => scenarios.some(item => item.id === current) ? current : scenarios[0]?.id ?? '')
  }, [open, applicationRef, application])
  useEffect(() => {
	if (!open) return
	setClusterResourceId(current => availableClusters.some(item => item.id === current) ? current : availableClusters[0]?.id ?? '')
  }, [open, workspaceId, clusters])
  useEffect(() => {
	if (!open || item) return
	setComponents(items => items.filter(item => !(item.capability.startsWith('chaos.') && (zones.length < 2 || !managedNetworkAvailable)) && !(item.capability.startsWith('monitoring.') && !managedNetworkAvailable)))
  }, [open, item, clusterResourceId, zones.length, managedNetworkAvailable])
  useEffect(() => {
	if (!open || item || components.length) return
	const defaults = ['load.', 'metrics.'].flatMap(prefix => {
	  const plugin = compatiblePlugins.find(item => primaryCapabilities(item).some(capability => capability.startsWith(prefix)))
	  const capability = plugin && primaryCapabilities(plugin).find(value => value.startsWith(prefix))
	  return plugin && capability ? [{key: sequence + (prefix === 'metrics.' ? 1 : 0), pluginId: plugin.id, capability}] : []
	})
	setComponents(defaults)
	setSequence(value => value + defaults.length)
  }, [open, item, compatiblePlugins, components.length, sequence])
  const addAdvancedComponent = (pluginId: string) => {
	const plugin = advancedPlugins.find(item => item.id === pluginId)
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
    if (readOnly) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    const values = new FormData(form)
    try {
      await request(item ? `/experiment-configurations/${item.id}` : '/experiment-configurations', {method: item ? 'PUT' : 'POST', body: JSON.stringify({
        workspaceId,
        clusterResourceId,
        name: values.get('name'),
        description: values.get('description'),
        applicationRef,
        updatedAt: item?.updatedAt,
        definition: {
          applicationValues: readSchemaValues(form, application?.descriptor.spec?.valuesSchema ?? emptySchema, 'applicationValues', applications),
          components: components.map((component, index) => {
            const plugin = compatiblePlugins.find(item => item.id === component.pluginId)!
            const configuration = readSchemaValues(form, configurableSchema(plugin.schema), `component-${component.key}`, applications)
			if (component.capability.startsWith('load.')) configuration.action = 'start'
			if (component.capability.startsWith('load.')) configuration.interactive = false
			if (component.capability.startsWith('load.')) configuration.scenarioId = loadScenarioId
            return {id: `component-${index + 1}`, strategyId: component.strategyId, pluginId: component.pluginId, capability: component.capability, configuration, targets: {include: split(values.get(`include-${component.key}`)), exclude: split(values.get(`exclude-${component.key}`))}}
          })
        }
      })}, session.csrfToken)
      close()
      if (!item) setComponents([])
      await changed()
    } catch (cause) {
      setError(message(cause))
    } finally {
      setPending(false)
    }
  }
  const form = <form onSubmit={submit} className={readOnly ? 'configuration-readonly-form' : undefined}>
      <fieldset className="configuration-form-fields" disabled={readOnly}>
      <div className="form-section"><strong>1. Cluster</strong><p>Select a ready Kubernetes cluster. Its health is checked again before every run and every step.</p></div>
      {workspaces.length > 1 ? <label>Environment<select value={workspaceId} onChange={event => setWorkspaceId(event.target.value)} required>{workspaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label> : <input type="hidden" value={workspaceId} readOnly />}
      <label>Kubernetes cluster<select name="clusterResourceId" value={clusterResourceId} onChange={event => setClusterResourceId(event.target.value)} required disabled={!availableClusters.length}>{availableClusters.length ? availableClusters.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">No ready cluster in this environment</option>}</select></label>
      <label>Name<input name="name" maxLength={120} placeholder="Scheduler baseline" defaultValue={item?.name} required autoFocus /></label>
      <label>Description<textarea name="description" rows={2} maxLength={1000} placeholder="What this configuration is intended to measure" defaultValue={item?.description} /></label>
      <div className="form-section"><strong>2. Application</strong><p>Select the application and change only the values exposed by its catalog contract.</p></div>
      <label>Application<select value={applicationRef} onChange={event => setApplicationRef(event.target.value)} required>{applications.map(item => <option key={item.reference} value={item.reference}>{item.name} · {item.version}</option>)}</select></label>
      {application && <SchemaFields schema={application.descriptor.spec?.valuesSchema ?? emptySchema} prefix="applicationValues" values={item?.definition.applicationValues ?? application.descriptor.spec?.defaults ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} />}
      <div className="form-section pool-section"><div><strong>3. Network between zones</strong><p>{networkComponent ? 'Traffic crossing application zones will use this network profile.' : !managedNetworkAvailable ? 'This cluster predates the managed network runtime.' : zones.length < 2 ? 'This cluster has fewer than two application zones.' : 'No network conditions will be injected.'}</p></div><label className="checkbox-field"><input type="checkbox" checked={Boolean(networkComponent)} disabled={!networkPlugin || !managedNetworkAvailable || zones.length < 2} onChange={event => setNetworkEnabled(event.target.checked)} /><span>Enable injection</span></label></div>
      {networkComponent && networkPlugin && <section className="experiment-component"><NetworkInjectionEditor key={`${item?.id ?? 'new'}-${networkComponent.key}`} prefix={`component-${networkComponent.key}`} zones={zones} values={networkComponent.configuration} /></section>}
      <div className="form-section"><strong>4. Monitoring</strong><p>Choose the query window and resolution used to collect the run results.</p></div>
      {metricsComponent && (() => {const plugin = compatiblePlugins.find(item => item.id === metricsComponent.pluginId)!; return <section className="experiment-component"><SchemaFields schema={experimentSchema(plugin.schema, metricsComponent.capability)} prefix={`component-${metricsComponent.key}`} values={metricsComponent.configuration ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></section>})()}
      <label className="checkbox-field"><input type="checkbox" checked={Boolean(monAgentComponent)} disabled={!monAgentPlugin || !managedNetworkAvailable} onChange={event => setMonAgentEnabled(event.target.checked)} /><span>Customize mon-agent for this experiment</span></label>
      {monAgentComponent && monAgentPlugin && <section className="experiment-component"><SchemaFields schema={experimentSchema(monAgentPlugin.schema, monAgentComponent.capability)} prefix={`component-${monAgentComponent.key}`} values={monAgentComponent.configuration ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></section>}
      <div className="form-section"><strong>5. Load profile</strong><p>Define the traffic intensity, temporal pattern and distribution across application zones.</p></div>
      {(application?.descriptor.spec?.interface?.loadScenarios?.length ?? 0) > 1 && <label>Load scenario<select value={loadScenarioId} onChange={event => setLoadScenarioId(event.target.value)} required>{application?.descriptor.spec?.interface?.loadScenarios?.map(item => <option key={item.id} value={item.id}>{item.id} · {item.engine ?? 'load'}</option>)}</select></label>}
      {(application?.descriptor.spec?.interface?.loadScenarios?.length ?? 0) === 1 && <p className="field-hint">Scenario: {loadScenarioId} · selected automatically from the application contract.</p>}
      {loadComponent && (() => {const plugin = compatiblePlugins.find(item => item.id === loadComponent.pluginId)!; return <section className="experiment-component"><SchemaFields schema={experimentSchema(plugin.schema, loadComponent.capability)} prefix={`component-${loadComponent.key}`} values={loadComponent.configuration ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} /><LoadProfileEditor key={`${item?.id ?? 'new'}-${loadComponent.key}`} prefix={`component-${loadComponent.key}`} zones={zones} values={loadComponent.configuration} /></section>})()}
      <div className="form-section"><strong>6. Scheduling and scaling</strong><p>Optional. Add only what this experiment needs.</p></div>
      <div className="component-add-options">{advancedPlugins.filter(plugin => !components.some(component => component.pluginId === plugin.id)).map(plugin => <button className="button secondary compact" type="button" key={plugin.id} disabled={components.length >= 15} onClick={() => addAdvancedComponent(plugin.id)}>+ {componentTitle(primaryCapabilities(plugin)[0] ?? '')}</button>)}</div>
      <div className="experiment-component-list">{advancedComponents.map(component => {
        const plugin = compatiblePlugins.find(item => item.id === component.pluginId) ?? compatiblePlugins[0]
        const componentIds = application?.descriptor.spec?.interface?.components?.map(item => item.id).join(', ') ?? ''
        const strategyKind = ['scheduler', 'descheduler', 'autoscaler'].find(kind => component.capability.startsWith(`${kind}.`))
        const readyStrategies = strategyKind ? strategies.filter(item => item.workspaceId === workspaceId && item.kind === strategyKind && item.status === 'succeeded') : []
        const selectedStrategy = readyStrategies.find(item => item.id === component.strategyId)
        return <section className="experiment-component" key={component.key}>
		  <div className="experiment-component-head"><strong>{componentTitle(component.capability)}</strong><button type="button" className="icon-button" aria-label="Remove component" onClick={() => setComponents(items => items.filter(item => item.key !== component.key))}>×</button></div>
          {strategyKind && <label>{componentTitle(component.capability)} image<select value={component.strategyId ?? ''} required onChange={event => setComponents(items => items.map(item => item.key === component.key ? {...item, strategyId: event.target.value} : item))}><option value="">Select a ready {strategyKind}</option>{readyStrategies.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>}
          {plugin && <SchemaFields key={`${plugin.id}-${component.strategyId ?? ''}`} schema={experimentSchema(plugin.schema, component.capability)} prefix={`component-${component.key}`} values={{...(selectedStrategy?.defaultConfiguration ?? {}), ...(component.configuration ?? {})}} applications={applications} artifacts={[]} connections={[]} credentials={[]} />}
          {strategyKind === 'autoscaler' && <AutoscalerTargetEditor prefix={`component-${component.key}`} components={application?.descriptor.spec?.interface?.components ?? []} defaults={selectedStrategy?.defaultConfiguration ?? {}} resetKey={`${item?.id ?? 'new'}-${applicationRef}-${component.strategyId ?? ''}`} excluded={component.targets?.exclude ?? []} targetOverrides={record(component.configuration?.targetOverrides)} />}
          {strategyKind !== 'autoscaler' && <details className="advanced-fields"><summary>Apply to services</summary><p>Leave both fields empty to use all services. Available: {componentIds || 'services in the application'}.</p><div className="field-row"><label>Only these services<input name={`include-${component.key}`} placeholder="frontend, checkoutservice" defaultValue={component.targets?.include?.join(', ')} /></label><label>Exclude services<input name={`exclude-${component.key}`} placeholder="emailservice" defaultValue={component.targets?.exclude?.join(', ')} /></label></div></details>}
        </section>
      })}</div>
      <div className="validation-callout"><span>✓</span><p>Cluster, application, network, monitoring, load and optional components are validated now and checked again before every run.</p></div>
      </fieldset>
      {!readOnly && <><p className="form-error">{error}</p><div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !workspaceId || !clusterResourceId || !application}>{pending ? 'Validating…' : item ? 'Validate and update' : 'Validate and save'}</button></div></>}
    </form>
  if (item) return <div className={`detail-page configuration-edit-page${readOnly ? ' configuration-view-page' : ''}`}><DetailBreadcrumb parent="Configurations" current={`${readOnly ? 'View' : 'Edit'} ${item.name}`} back={close} /><div className="detail-page-heading"><div><h2>{readOnly ? 'View configuration' : 'Edit configuration'}</h2><p>{readOnly ? 'Complete saved configuration in read-only mode.' : 'Changes apply only to experiments created after this update.'}</p></div></div><article className="detail-page-card">{form}</article></div>
  return <Dialog open={open} onClose={close} title="Create experiment configuration" eyebrow="VALIDATE BEFORE RUN" className="template-modal experiment-modal">{form}</Dialog>
}

function ExperimentInstanceDialog({item, close, session, changed, onCreated}: {item: ExperimentConfiguration | null; close: () => void; session: Session; changed: () => Promise<void>; onCreated: (id: string) => void}) {
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
      const created = await request<Experiment>('/experiment-instances', {method: 'POST', body: JSON.stringify({configurationId: item.id, name: values.get('name'), runs: Number(values.get('runs')), scheduledFor: scheduled ? new Date(scheduled).toISOString() : undefined})}, session.csrfToken)
      close()
      await changed()
      onCreated(created.id)
    } catch (cause) {
      setError(message(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={Boolean(item)} onClose={close} title="Run experiment" className="template-modal">
    <form onSubmit={submit}>
      <div className="form-section"><strong>{item?.name}</strong><p>Runs execute in sequence with a reset between them.</p></div>
      <label>Instance name<input name="name" maxLength={120} defaultValue={item ? `${item.name} run` : ''} required autoFocus /></label>
      <div className="field-row"><label>Number of runs<input name="runs" type="number" min={1} max={20} defaultValue={3} required /></label><label>Start later (optional)<input name="scheduledFor" type="datetime-local" /></label></div>
      <div className="validation-callout"><span>✓</span><p>The configuration is validated again before starting.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Validating…' : 'Validate and run'}</button></div>
    </form>
  </Dialog>
}

const emptySchema: JsonSchema = {type: 'object', properties: {}}

export function configurableSchema(schema: JsonSchema): JsonSchema {
  const properties = Object.fromEntries(Object.entries(schema.properties ?? {}).filter(([, property]) => property.format !== 'kubephos-artifact-ref' && property.format !== 'kubephos-application-ref' && property.format !== 'kubephos-strategy-image'))
  return {type: 'object', properties, required: (schema.required ?? []).filter(name => name in properties)}
}

function experimentSchema(schema: JsonSchema, capability: string): JsonSchema {
  const result = configurableSchema(schema)
  const hidden = new Set<string>()
  if (capability.startsWith('load.')) ['action', 'interactive', 'scenarioId', 'steps', 'geographicSteps'].forEach(name => hidden.add(name))
  if (capability.startsWith('autoscaler.')) hidden.add('targetOverrides')
  if (!hidden.size) return result
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
