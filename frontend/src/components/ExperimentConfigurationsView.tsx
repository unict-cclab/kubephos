import {ResourceListHeading} from './ResourceListHeading'
import {ConfigurationReadout} from './ConfigurationReadout'
import {FormDisclosure} from './FormDisclosure'
import {FormTabs} from './FormTabs'
import {WizardForm, WizardStep, WizardReview} from './WizardForm'
import {ResourceSharing, canManageResource} from './ResourceSharing'
import {ResourceAttribution} from './ResourceAttribution'
import {ActionIcon} from './ActionIcon'
import {ComparativeConfigurationDialog} from './ComparativeConfigurationDialog'
import {ResourceListTable} from './ResourceListTable'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {useEffect, useMemo, useRef, useState, type FormEvent} from 'react'
import {request} from '../api'
import {readSchemaValues} from '../lib'
import type {Application, ApplicationComponent, Artifact, CatalogStrategy, Experiment, ExperimentConfiguration, JsonSchema, ManagedResource, Operation, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {EditorPage} from './EditorPage'
import {AutoscalerTargetEditor} from './AutoscalerTargetEditor'
import {StrategyTargetEditor} from './StrategyTargetEditor'
import {LoadProfileEditor} from './LoadProfileEditor'
import {NetworkInjectionEditor} from './NetworkInjectionEditor'
import {SchemaFields, hasConfigurableFields} from './SchemaFields'
import {ResultsView} from './LazyResultsView'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {useDetailRoute} from '../detailRoute'
import {SectionIcon} from './SectionIcon'
import {ResourceAction} from './ResourceAction'
import {ExperimentExecutionDialog} from './ExperimentExecutionDialog'
import {ApplicationTopologyGraph} from './ApplicationTopologyGraph'
import {InitialReplicaEditor} from './InitialReplicaEditor'
import {OwnerFilter, ownerLabel} from './OwnerFilter'

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
type ApplicationTopologyPreview = {workloadTargets: Array<{id: string; apiVersion: string; kind: string; name: string; traits?: string[]; index?: number; dependencies?: string[]}>}
type DestinationMode = 'application' | 'external'

const defaultExternalLocustScript = `from locust import HttpUser, task

class ExternalUser(HttpUser):
    @task
    def request(self):
        self.client.get("/")
`

function previewComponents(preview: ApplicationTopologyPreview): ApplicationComponent[] {
	return preview.workloadTargets.map(target => ({id: target.id, traits: target.traits, workload: {apiVersion: target.apiVersion, kind: target.kind, name: target.name}, index: target.index, dependencies: target.dependencies}))
}

export function ExperimentConfigurationsView(props: Props) {
  const {confirmDelete, confirmationDialog} = useDeleteConfirmation()
  const [creating, setCreating] = useState<'choose' | 'single' | 'suite' | null>(null)
  const [editing, setEditing] = useState(false)
  const [viewing, setViewing] = useState(false)
  const [running, setRunning] = useState<ExperimentConfiguration | null>(null)
  const detail = useDetailRoute('experiments')
  const [error, setError] = useState('')
  const [owner, setOwner] = useState('')
	const experimentCounts = useMemo(() => {
		const counts = new Map<string, number>()
		for (const experiment of props.experiments) {
			if (!experiment.configurationId) continue
			counts.set(experiment.configurationId, (counts.get(experiment.configurationId) ?? 0) + 1)
		}
		return counts
	}, [props.experiments])
	const ready = props.clusters.some(item => item.status === 'ready')
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
    if (!await confirmDelete(`Delete experiment configuration ${item.name}? Existing experiment instances, runs, logs and results will be retained.`)) return
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
  const visibleItems = props.items.filter(item => !owner || item.ownerUserId === owner)
  if (selectedExperiment) return <ResultsView artifacts={props.artifacts ?? []} experiments={props.experiments} operations={props.operations ?? []} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} focusedExperiment={selectedExperiment} onBack={detail.close} />
  if (selectedConfiguration?.kind === 'suite' && (viewing || editing)) return <ComparativeConfigurationDialog {...props} configurations={props.items} item={selectedConfiguration} readOnly={viewing} open close={() => {setViewing(false); setEditing(false)}} />
  if (selectedConfiguration && viewing) return <ExperimentConfigurationDialog {...props} item={selectedConfiguration} readOnly open close={() => setViewing(false)} />
  if (selectedConfiguration && editing) return <ExperimentConfigurationDialog {...props} item={selectedConfiguration} open close={() => setEditing(false)} />
  if (selectedConfiguration) return <><ExperimentConfigurationDetailsPage item={selectedConfiguration} clusters={props.clusters} applications={props.applications} plugins={props.plugins} strategies={props.strategies} session={props.session} close={detail.close} canRun={props.isAdmin || canManageResource(props.session, selectedConfiguration.ownerUserId)} configurations={props.items} changed={props.changed} canClone={props.isAdmin || !selectedConfiguration.ownerUserId || selectedConfiguration.ownerUserId === props.session.user?.id} run={() => setRunning(selectedConfiguration)} view={() => setViewing(true)} edit={() => setEditing(true)} clone={() => void clone(selectedConfiguration)} />{error && <p className="form-error page-error">{error}</p>}<ExperimentExecutionDialog open={Boolean(running)} configurations={running ? [running] : []} initialConfigurationId={running?.id} lockConfiguration close={() => setRunning(null)} session={props.session} changed={props.changed} onCreated={props.openExperiment ?? (() => {})} /></>
  return <>
    {confirmationDialog}
    <ResourceListHeading title="Configurations" icon="configurations" action={<>{(props.isAdmin || Boolean(props.session.user)) && <ResourceAction action="create" label="New configuration" disabled={!ready} onClick={() => setCreating('choose')} />}</>} />
    {error && <p className="form-error page-error">{error}</p>}
    <ResourceListTable editable filters={<OwnerFilter items={props.items} value={owner} onChange={setOwner} />} empty={<><strong>{props.items.length ? 'No matching configurations' : 'No configurations yet'}</strong><p>{props.items.length ? 'Choose another owner filter to find your configuration.' : 'Start with a ready Kubernetes cluster, then choose an application or an external endpoint.'}</p></>} searchText={item => `${item.name} ${ownerLabel(item)} ${item.kind} ${item.validation.valid ? 'validated' : 'needs review'}`} items={visibleItems} label="Configurations" columns={[
      {label: 'Name', render: item => <h3>{item.name}</h3>},
      {label: 'Type', render: item => item.kind === 'suite' ? 'Comparative' : 'Single'},
      {label: 'Experiments', render: item => experimentCounts.get(item.id) ?? 0}
    ]} actions={item => <><ResourceSharing kind="experiment-configurations" id={item.id} name={item.name} ownerUserId={item.ownerUserId} session={props.session} changed={props.changed} /><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('configuration', item.id)} />{(props.isAdmin || canManageResource(props.session, item.ownerUserId)) && <ResourceAction action="delete" label={`Delete ${item.name}`} onClick={() => void remove(item)} />}</>} />
    <Dialog open={creating === 'choose'} onClose={() => setCreating(null)} title="New configuration"><div className="configuration-kind-options"><button className="configuration-kind" type="button" onClick={() => setCreating('single')}><SectionIcon kind="experiments" /><span>Single experiment</span><ActionIcon name="right" /></button><button className="configuration-kind" type="button" onClick={() => setCreating('suite')}><SectionIcon kind="experiments" /><span>Comparison</span><ActionIcon name="right" /></button></div></Dialog>
    <ExperimentConfigurationDialog {...props} open={creating === 'single'} close={() => setCreating(null)} />
    <ComparativeConfigurationDialog {...props} configurations={props.items.filter(item => props.isAdmin || canManageResource(props.session,item.ownerUserId))} open={creating === 'suite'} close={() => setCreating(null)} />
    <ExperimentExecutionDialog open={Boolean(running)} configurations={running ? [running] : []} initialConfigurationId={running?.id} lockConfiguration close={() => setRunning(null)} session={props.session} changed={props.changed} onCreated={props.openExperiment ?? (() => {})} />
  </>
}

function ExperimentConfigurationDetailsPage({item, clusters, applications, plugins, strategies, session, close, canRun, canClone, run, view, edit, clone, configurations, changed}: {plugins: Plugin[]; strategies: CatalogStrategy[]; changed: () => Promise<void>; configurations: ExperimentConfiguration[]; item: ExperimentConfiguration; clusters: ManagedResource[]; applications: Application[]; session: Session; close: () => void; canRun: boolean; canClone: boolean; run: () => void; view: () => void; edit: () => void; clone: () => void}) {
  if (item.kind === 'suite') return <div className="detail-page">
    <DetailBreadcrumb parent="Configurations" current={item.name} back={close} />
    <div className="detail-page-heading"><h2>{item.name}</h2><div className="detail-page-actions"><ResourceSharing kind="experiment-configurations" id={item.id} name={item.name} ownerUserId={item.ownerUserId} session={session} changed={changed} />{canRun && <button className="button primary" disabled={!item.validation.valid} onClick={run}>Run experiment</button>}<button className="button secondary" onClick={view}>View configuration</button>{canRun && <button className="button secondary" onClick={edit}>Edit configuration</button>}{canClone && <button className="button secondary" onClick={clone}>Clone configuration</button>}</div></div>
    <ResourceAttribution resource={item} editable /><article className="detail-page-card comparative-details">
      <div className="details-summary"><div><span>Runs per configuration</span><strong>{item.definition.runs ?? 3}</strong></div><div><span>Total runs</span><strong>{(item.definition.variants?.length ?? 0) * (item.definition.runs ?? 3)}</strong></div></div>
      <div className="comparative-members-heading"><h3>Execution order</h3><span>Plot color</span></div>
      <ol className="comparative-member-list">{item.definition.variants?.map((member, index) => <li className="comparative-member" key={`${member.configurationId}-${index}`}>
        <span className="comparative-member-position" aria-hidden="true">{String(index + 1).padStart(2, '0')}</span>
        <div className="comparative-member-copy"><strong>{member.alias}</strong>{configurations.find(configuration => configuration.id === member.configurationId)?.name !== member.alias && <span>{configurations.find(configuration => configuration.id === member.configurationId)?.name ?? 'Unavailable single configuration'}</span>}</div>
        <span className="configuration-color-swatch" style={{backgroundColor: member.color}} role="img" aria-label={`Plot color for ${member.alias}: ${member.color}`} title={member.color} />
      </li>)}</ol>
      {!item.validation.valid && <p className="form-error">Replace unavailable source configurations before running this suite.</p>}
    </article>
  </div>
  const application = applications.find(value => value.reference === item.applicationRef)
  const applicationSettings = {...(application?.descriptor.spec?.defaults ?? {}), ...(item.definition.applicationValues ?? {})}
  return <div className="detail-page"><DetailBreadcrumb parent="Configurations" current={item.name} back={close} /><div className="detail-page-heading"><h2>{item.name}</h2><div className="detail-page-actions"><ResourceSharing kind="experiment-configurations" id={item.id} name={item.name} ownerUserId={item.ownerUserId} session={session} changed={changed} />{canRun && <button className="button primary" disabled={!item.validation.valid} onClick={run}>Run experiment</button>}{canRun && <button className="button secondary" onClick={edit}>Edit configuration</button>}{canClone && <button className="button secondary" onClick={clone}>Clone configuration</button>}</div></div><ResourceAttribution resource={item} editable /><article className="detail-page-card configuration-detail-card">
    <ConfigurationReadout item={item} clusters={clusters} applications={applications} plugins={plugins} strategies={strategies} showHeading={false} applicationTopology={item.definition.destinationMode !== 'external' && <ConfigurationApplicationTopology application={application} values={applicationSettings} session={session} />} />
  </article></div>
}

function ConfigurationApplicationTopology({application, values, session}: {application?: Application; values: Record<string, unknown>; session: Session}) {
	const dynamic = application?.descriptor.spec?.valuesSchema?.['x-kubephos-dynamic-topology'] === true
	const [components, setComponents] = useState<ApplicationComponent[]>(dynamic ? [] : application?.descriptor.spec?.interface?.components ?? [])
	const [pending, setPending] = useState(dynamic)
	const [error, setError] = useState('')
	const valuesKey = JSON.stringify(values)
	useEffect(() => {
		if (!application) {
			setComponents([])
			setPending(false)
			setError('')
			return
		}
		if (!dynamic) {
			setComponents(application.descriptor.spec?.interface?.components ?? [])
			setPending(false)
			setError('')
			return
		}
		let current = true
		setComponents([])
		setPending(true)
		setError('')
		request<ApplicationTopologyPreview>(`/catalog/applications/${encodeURIComponent(application.id)}/resolve`, {method: 'POST', body: valuesKey}, session.csrfToken).then(preview => {
			if (current) setComponents(previewComponents(preview))
		}).catch(cause => {
			if (current) setError(message(cause))
		}).finally(() => {
			if (current) setPending(false)
		})
		return () => { current = false }
	}, [application?.reference, dynamic, session.csrfToken, valuesKey])
	if (!application) return null
	if (pending) return <section className="details-section application-topology-state" role="status"><div className="details-heading"><strong>Application topology</strong></div><span>Preparing topology…</span></section>
	if (error) return <section className="details-section application-topology-state invalid" role="status"><div className="details-heading"><strong>Application topology</strong></div><span>{error}</span></section>
	return <ApplicationTopologyGraph components={components} />
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

export function ExperimentConfigurationDialog({open, close, clusters, workspaces, applications, plugins, strategies, session, changed, item, readOnly = false}: Props & {open: boolean; close: () => void; item?: ExperimentConfiguration; readOnly?: boolean}) {
	const formRef = useRef<HTMLFormElement>(null)
	const topologyTimer = useRef<number | undefined>(undefined)
	const topologyRequest = useRef(0)
	const availableApplications = applications.filter((application, index, items) => application.reference === item?.applicationRef || application.origin !== 'generated' && items.findIndex(candidate => candidate.id === application.id && candidate.origin !== 'generated') === index)
  const compatiblePlugins = useMemo(() => experimentPlugins(plugins), [plugins])
  const advancedPlugins = compatiblePlugins.filter(item => primaryCapabilities(item).some(capability => !['load.', 'metrics.', 'monitoring.', 'chaos.'].some(prefix => capability.startsWith(prefix))))
  const initialComponents = () => (item?.definition.components ?? []).map((component, index) => ({key: index + 1, pluginId: component.pluginId, capability: component.capability, strategyId: component.strategyId, configuration: component.configuration ?? {}, targets: component.targets ?? {include: [], exclude: []}}))
  const availableClusters = clusters.filter(cluster => cluster.status === 'ready' || cluster.id === item?.clusterResourceId)
  const [clusterResourceId, setClusterResourceId] = useState(item?.clusterResourceId ?? '')
  const workspaceId = clusters.find(cluster => cluster.id === clusterResourceId)?.workspaceId ?? item?.workspaceId ?? workspaces[0]?.id ?? ''
	const [applicationRef, setApplicationRef] = useState(item?.applicationRef ?? availableApplications[0]?.reference ?? '')
  const [destinationMode, setDestinationMode] = useState<DestinationMode>(item?.definition.destinationMode === 'external' ? 'external' : 'application')
  const [externalTargetUrl, setExternalTargetUrl] = useState(item?.definition.externalDestination?.targetUrl ?? '')
  const [externalRuntimeImage, setExternalRuntimeImage] = useState(item?.definition.externalDestination?.runtimeImage ?? 'locustio/locust:2.42.6')
  const [externalScript, setExternalScript] = useState(item?.definition.externalDestination?.script ?? defaultExternalLocustScript)
  const [externalParameters, setExternalParameters] = useState(JSON.stringify(item?.definition.externalDestination?.parameters ?? {}, null, 2))
  const savedLoad = item?.definition.components?.find(component => component.capability.startsWith('load.'))
  const [loadScenarioId, setLoadScenarioId] = useState(typeof savedLoad?.configuration.scenarioId === 'string' ? savedLoad.configuration.scenarioId : applications[0]?.descriptor.spec?.interface?.loadScenarios?.[0]?.id ?? '')
  const [components, setComponents] = useState<ComponentDraft[]>(initialComponents)
  const [sequence, setSequence] = useState((item?.definition.components?.length ?? 0) + 1)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
	const [resolvedComponents, setResolvedComponents] = useState<ApplicationComponent[] | null>(null)
	const [topologyPending, setTopologyPending] = useState(false)
	const [topologyError, setTopologyError] = useState('')
	const application = availableApplications.find(item => item.reference === applicationRef)
	const dynamicTopology = destinationMode === 'application' && application?.descriptor.spec?.valuesSchema?.['x-kubephos-dynamic-topology'] === true
	const effectiveApplicationComponents = dynamicTopology ? resolvedComponents ?? application?.descriptor.spec?.interface?.components ?? [] : application?.descriptor.spec?.interface?.components ?? []
  const applicationValues = normalizeApplicationValues(application, {...(application?.descriptor.spec?.defaults ?? {}), ...(item?.definition.applicationValues ?? {})})
  const nativeTCP = Boolean(application?.descriptor.spec?.interface?.endpoints?.length) && application!.descriptor.spec!.interface!.endpoints!.every(endpoint => endpoint.protocol === 'tcp')
  const configurableReplicas = Boolean(application?.descriptor.spec?.valuesSchema?.properties?.initialReplicas)
  const parametersSchema = applicationSettingsSchema(application?.descriptor.spec?.valuesSchema ?? emptySchema, false)
  const hasParameters = hasConfigurableFields(parametersSchema, applicationValues) || !configurableReplicas
  const resourcesSchema = applicationSettingsSchema(application?.descriptor.spec?.valuesSchema ?? emptySchema, true)
  const topologyStatus = dynamicTopology && (topologyPending || topologyError) ? {message: topologyError || 'Checking application topology…', error: Boolean(topologyError)} : undefined
  const replicaAvailability = <label key={applicationRef} className="field-short">Minimum available replicas<input type="number" name="podDisruptionBudgetMinAvailable" min={nativeTCP ? 0 : 1} max={20} step={1} defaultValue={item?.definition.podDisruptionBudgetMinAvailable ?? (nativeTCP ? 0 : 1)} required /></label>
  const selectedCluster = clusters.find(item => item.id === clusterResourceId)
  const zones = destinationMode === 'external' ? [] : applicationZones(selectedCluster)
  const loadComponent = components.find(item => item.capability.startsWith('load.'))
  const metricsComponent = components.find(item => item.capability.startsWith('metrics.'))
  const networkComponent = components.find(item => item.capability.startsWith('chaos.'))
  const networkPlugin = compatiblePlugins.find(item => primaryCapabilities(item).some(capability => capability.startsWith('chaos.')))
  const monAgentComponent = components.find(item => item.capability.startsWith('monitoring.'))
  const monAgentPlugin = compatiblePlugins.find(item => primaryCapabilities(item).some(capability => capability.startsWith('monitoring.')))
  const managedNetworkAvailable = typeof selectedCluster?.spec?.managedProfile === 'string' && selectedCluster.spec.managedProfile.length > 0
  const advancedComponents = components.filter(item => !['load.', 'metrics.', 'monitoring.', 'chaos.'].some(prefix => item.capability.startsWith(prefix)))
  const schedulingGroups = ['scheduler', 'descheduler', 'autoscaler', ...(advancedComponents.some(component => schedulingGroup(component.capability) === 'other') || advancedPlugins.some(plugin => schedulingGroup(primaryCapabilities(plugin)[0] ?? '') === 'other') ? ['other'] : [])]
	const changeDestinationMode = (mode: DestinationMode) => {
		setDestinationMode(mode)
		if (mode === 'external') {
			setComponents(items => items.filter(component => component.capability.startsWith('load.') || component.capability.startsWith('metrics.')))
			setLoadScenarioId('external-load')
		}
	}
	const resolveApplicationTopology = (form: HTMLFormElement) => {
		if (!application || !dynamicTopology) return
		if (topologyTimer.current !== undefined) window.clearTimeout(topologyTimer.current)
		const requestID = ++topologyRequest.current
		setTopologyPending(true)
		setTopologyError('')
		topologyTimer.current = window.setTimeout(async () => {
			try {
				const values = pruneServiceOverrides(readSchemaValues(form, application.descriptor.spec?.valuesSchema ?? emptySchema, 'applicationValues', availableApplications), application)
				const preview = await request<ApplicationTopologyPreview>(`/catalog/applications/${encodeURIComponent(application.id)}/resolve`, {method: 'POST', body: JSON.stringify(values)}, session.csrfToken)
				if (requestID !== topologyRequest.current) return
				setResolvedComponents(previewComponents(preview))
			} catch (cause) {
				if (requestID !== topologyRequest.current) return
				setResolvedComponents(null)
				setTopologyError(message(cause))
			} finally {
				if (requestID === topologyRequest.current) setTopologyPending(false)
			}
		}, 300)
	}
  useEffect(() => {
	if (!open) return
	if (item) {
	  const drafts = (item.definition.components ?? []).map((component, index) => ({key: index + 1, pluginId: component.pluginId, capability: component.capability, strategyId: component.strategyId, configuration: component.configuration ?? {}, targets: component.targets ?? {include: [], exclude: []}}))
	  setClusterResourceId(item.clusterResourceId)
	  setApplicationRef(item.applicationRef)
	  setDestinationMode(item.definition.destinationMode === 'external' ? 'external' : 'application')
	  setExternalTargetUrl(item.definition.externalDestination?.targetUrl ?? '')
	  setExternalRuntimeImage(item.definition.externalDestination?.runtimeImage ?? 'locustio/locust:2.42.6')
	  setExternalScript(item.definition.externalDestination?.script ?? defaultExternalLocustScript)
	  setExternalParameters(JSON.stringify(item.definition.externalDestination?.parameters ?? {}, null, 2))
	  setComponents(drafts)
	  setSequence(drafts.length + 1)
	  const load = drafts.find(component => component.capability.startsWith('load.'))
	  setLoadScenarioId(typeof load?.configuration?.scenarioId === 'string' ? load.configuration.scenarioId : '')
	  return
	}
	setDestinationMode('application')
	setApplicationRef(availableApplications[0]?.reference ?? '')
	setExternalTargetUrl('')
	setExternalRuntimeImage('locustio/locust:2.42.6')
	setExternalScript(defaultExternalLocustScript)
	setExternalParameters('{}')
	setClusterResourceId('')
	setComponents([])
	setSequence(1)
  }, [open, item?.id])
  useEffect(() => {
	if (!open || item) return
	setApplicationRef(current => availableApplications.some(application => application.reference === current) ? current : availableApplications[0]?.reference ?? '')
  }, [open, item, workspaces, applications])
  useEffect(() => {
	if (!open) return
	if (destinationMode === 'external') {
		setLoadScenarioId('external-load')
		return
	}
	const scenarios = application?.descriptor.spec?.interface?.loadScenarios ?? []
	setLoadScenarioId(current => scenarios.some(item => item.id === current) ? current : scenarios[0]?.id ?? '')
	}, [open, applicationRef, application, destinationMode])
	useEffect(() => {
		if (!open) return
		if (topologyTimer.current !== undefined) window.clearTimeout(topologyTimer.current)
		topologyRequest.current++
		setTopologyPending(false)
		setResolvedComponents(null)
		setTopologyError('')
		if (!dynamicTopology) {
			setTopologyPending(false)
			return
		}
		if (!item) {
			setTopologyPending(false)
			return
		}
		const timer = window.setTimeout(() => {
			if (formRef.current) resolveApplicationTopology(formRef.current)
		}, 0)
		return () => window.clearTimeout(timer)
	}, [open, applicationRef, item?.id, dynamicTopology])
	useEffect(() => () => {
		if (topologyTimer.current !== undefined) window.clearTimeout(topologyTimer.current)
		topologyRequest.current++
	}, [])
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
	  let externalParameterValues: Record<string, string> = {}
	  if (destinationMode === 'external') {
		const parsed: unknown = JSON.parse(externalParameters || '{}')
		if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || Object.values(parsed).some(value => typeof value !== 'string')) throw new Error('Locust parameters must be a JSON object containing string values.')
		externalParameterValues = parsed as Record<string, string>
	  }
	  const submittedComponents = destinationMode === 'external' ? components.filter(component => component.capability.startsWith('load.') || component.capability.startsWith('metrics.')) : components
      await request(item ? `/experiment-configurations/${item.id}` : '/experiment-configurations', {method: item ? 'PUT' : 'POST', body: JSON.stringify({
        workspaceId,
        clusterResourceId,
        name: values.get('name'),
        description: '',
		applicationRef: destinationMode === 'application' ? applicationRef : '',
        updatedAt: item?.updatedAt,
        definition: {
		  destinationMode,
		  externalDestination: destinationMode === 'external' ? {targetUrl: externalTargetUrl, runtimeImage: externalRuntimeImage, script: externalScript, parameters: externalParameterValues} : undefined,
		  applicationValues: destinationMode === 'application' ? readSchemaValues(form, application?.descriptor.spec?.valuesSchema ?? emptySchema, 'applicationValues', availableApplications) : {},
          podDisruptionBudgetMinAvailable: destinationMode === 'application' ? Number(values.get('podDisruptionBudgetMinAvailable')) : 0,
          components: submittedComponents.map((component, index) => {
            const plugin = compatiblePlugins.find(item => item.id === component.pluginId)
            if (!plugin) throw new Error(`Component ${component.pluginId} is no longer available. Remove or replace it before saving.`)
            const configuration = readSchemaValues(form, configurableSchema(plugin.schema), `component-${component.key}`, applications)
			if (component.capability.startsWith('load.')) configuration.action = 'start'
			if (component.capability.startsWith('load.')) configuration.interactive = false
			if (component.capability.startsWith('load.')) configuration.scenarioId = destinationMode === 'external' ? 'external-load' : loadScenarioId
			if (destinationMode === 'external' && component.capability.startsWith('load.')) delete configuration.geographicSteps
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
  const form = readOnly && item ? <ConfigurationReadout item={item} clusters={clusters} applications={applications} plugins={plugins} strategies={strategies} /> : <WizardForm ref={formRef} onSubmit={submit} onCancel={close} pending={pending} error={error} submitLabel={item ? 'Save changes' : 'Create configuration'} submitDisabled={topologyPending || Boolean(topologyError) || !workspaceId || !clusterResourceId || destinationMode === 'application' && !application || destinationMode === 'external' && (!externalTargetUrl || !externalRuntimeImage || !externalScript)} onChange={event => {
        const target = event.nativeEvent.target as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement
        if (target.name.startsWith('applicationValues.')) resolveApplicationTopology(event.currentTarget)
      }}>
      <WizardStep key="general" title="General" status={topologyStatus} blocked={destinationMode === 'application' && (topologyPending || Boolean(topologyError))}>
      <div className="field-row"><label>Name<input name="name" maxLength={120} placeholder="Scheduler baseline" defaultValue={item?.name} required autoFocus /></label>
      <label>Kubernetes cluster<select name="clusterResourceId" value={clusterResourceId} onChange={event => setClusterResourceId(event.target.value)} required disabled={!availableClusters.length}>{availableClusters.length ? availableClusters.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">No ready cluster available</option>}</select></label></div>
      <div className="field-row field-row-destination"><label>Destination type<select aria-label="Destination type" value={destinationMode} onChange={event => changeDestinationMode(event.target.value as DestinationMode)}><option value="application">Catalog application</option><option value="external">External destination</option></select></label>
      {destinationMode === 'application' && <label>Application<select value={applicationRef} onChange={event => setApplicationRef(event.target.value)} required disabled={!availableApplications.length}>{availableApplications.length ? availableApplications.map(item => <option key={item.reference} value={item.reference}>{item.name}</option>) : <option value="">No catalog application available</option>}</select></label>}</div>
      </WizardStep><WizardStep key="application" status={topologyStatus} title={destinationMode === 'external' ? 'External destination' : 'Application'} blocked={topologyPending || Boolean(topologyError)}>
      {destinationMode === 'application' ? <>
        {!hasParameters && application && <div hidden><SchemaFields schema={parametersSchema} prefix="applicationValues" values={applicationValues} applications={availableApplications} artifacts={[]} connections={[]} credentials={[]} /></div>}
        <FormTabs label="Application settings" items={[
          ...(hasParameters ? [{id: 'settings', title: 'Parameters', content: <>
            {application && <SchemaFields schema={parametersSchema} prefix="applicationValues" values={applicationValues} serviceTargets={effectiveApplicationComponents} applications={availableApplications} artifacts={[]} connections={[]} credentials={[]} onValuesChange={() => {if (formRef.current) resolveApplicationTopology(formRef.current)}} />}
            {application && !configurableReplicas && replicaAvailability}
          </>}] : []),
          ...(configurableReplicas ? [{id: 'replicas', title: 'Replicas', content: <>
            <InitialReplicaEditor prefix="applicationValues" components={effectiveApplicationComponents} values={applicationValues} resetKey={`${item?.id ?? 'new'}-${applicationRef}`}>{replicaAvailability}</InitialReplicaEditor>
          </>}] : []),
          ...(Object.keys(resourcesSchema.properties ?? {}).length ? [{id: 'resources', title: 'Resources', content: <div className="application-resources"><SchemaFields schema={resourcesSchema} prefix="applicationValues" values={applicationValues} serviceTargets={effectiveApplicationComponents} applications={availableApplications} artifacts={[]} connections={[]} credentials={[]} /></div>}] : []),
          {id: 'topology', title: 'Topology', content: <>
        {application && !topologyError && <ApplicationTopologyGraph components={effectiveApplicationComponents} />}
          </>}
        ]} />
      </> : <FormTabs label="External destination settings" items={[
        {id: 'target', title: 'Target', content: <div className="form-stack"><label>External endpoint<input type="url" value={externalTargetUrl} onChange={event => setExternalTargetUrl(event.target.value)} placeholder="https://api.example.com" maxLength={2048} required /></label><label>Locust runtime image<input value={externalRuntimeImage} onChange={event => setExternalRuntimeImage(event.target.value)} maxLength={512} required /></label></div>},
        {id: 'script', title: 'Locustfile', content: <label>Locust file template<textarea value={externalScript} onChange={event => setExternalScript(event.target.value)} rows={14} maxLength={262144} spellCheck={false} required /></label>},
        {id: 'variables', title: 'Variables', content: <label>Locustfile variables (JSON)<textarea value={externalParameters} onChange={event => setExternalParameters(event.target.value)} rows={5} spellCheck={false} placeholder={'{"tenant":"demo"}'} /></label>}
      ]} />}
      </WizardStep><WizardStep key="monitoring" title="Monitoring">
      {metricsComponent && (() => {const plugin = compatiblePlugins.find(item => item.id === metricsComponent.pluginId)!; return <section className="experiment-component"><SchemaFields schema={experimentSchema(plugin.schema, metricsComponent.capability)} prefix={`component-${metricsComponent.key}`} values={metricsComponent.configuration ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></section>})()}
      {destinationMode === 'application' && (Boolean(networkComponent) || Boolean(networkPlugin) && managedNetworkAvailable && zones.length > 1) && <FormDisclosure className="network-injection-settings" title={selectedCluster?.spec.mode === 'multi' ? 'Network between clusters' : 'Network between zones'} defaultOpen={Boolean(networkComponent)}><label className="checkbox-field"><input type="checkbox" checked={Boolean(networkComponent)} disabled={!networkPlugin || !managedNetworkAvailable || zones.length < 2} onChange={event => setNetworkEnabled(event.target.checked)} /><span>Enable injection</span></label>
      {networkComponent && networkPlugin && <section className="experiment-component"><NetworkInjectionEditor key={`${item?.id ?? 'new'}-${networkComponent.key}`} prefix={`component-${networkComponent.key}`} zones={zones} distribution={selectedCluster?.spec.mode === 'multi' ? 'cluster' : 'zone'} values={selectedCluster?.spec.mode === 'multi' ? {...networkComponent.configuration, nodeGroupLabel: 'kubephos.dev/cluster', networkInterface: 'cilium_vxlan', hostNetwork: false} : networkComponent.configuration} /></section>}</FormDisclosure>}
      {destinationMode === 'application' && <><label className="checkbox-field"><input type="checkbox" checked={Boolean(monAgentComponent)} disabled={!monAgentPlugin || !managedNetworkAvailable} onChange={event => setMonAgentEnabled(event.target.checked)} /><span>Customize mon-agent for this experiment</span></label>
      {monAgentComponent && monAgentPlugin && <section className="experiment-component"><SchemaFields schema={experimentSchema(monAgentPlugin.schema, monAgentComponent.capability)} prefix={`component-${monAgentComponent.key}`} values={monAgentComponent.configuration ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></section>}</>}
      </WizardStep><WizardStep key="load" title="Load profile">
      {destinationMode === 'application' && (application?.descriptor.spec?.interface?.loadScenarios?.length ?? 0) > 1 && <label>Load scenario<select value={loadScenarioId} onChange={event => setLoadScenarioId(event.target.value)} required>{application?.descriptor.spec?.interface?.loadScenarios?.map(item => <option key={item.id} value={item.id}>{item.id} · {item.engine ?? 'load'}</option>)}</select></label>}
      {loadComponent && (() => {const plugin = compatiblePlugins.find(item => item.id === loadComponent.pluginId)!; return <section className="experiment-component"><LoadProfileEditor settings={<SchemaFields schema={experimentSchema(plugin.schema, loadComponent.capability)} prefix={`component-${loadComponent.key}`} values={loadComponent.configuration ?? {}} applications={applications} artifacts={[]} connections={[]} credentials={[]} />} key={`${item?.id ?? 'new'}-${loadComponent.key}`} prefix={`component-${loadComponent.key}`} zones={nativeTCP && selectedCluster?.spec.mode !== 'multi' ? [] : zones} distribution={selectedCluster?.spec.mode === 'multi' ? 'cluster' : 'zone'} values={loadComponent.configuration} /></section>})()}
      </WizardStep>{destinationMode === 'application' && <WizardStep key="scheduling" title="Scheduling and scaling">
      <FormTabs label="Scheduling and scaling settings" items={schedulingGroups.map(kind => ({id: kind, title: kind === 'other' ? 'Other' : componentTitle(`${kind}.`), content: <><div className="component-add-options">{advancedPlugins.filter(plugin => schedulingGroup(primaryCapabilities(plugin)[0] ?? '') === kind && !components.some(component => component.pluginId === plugin.id)).map(plugin => <button className="button secondary compact" type="button" key={plugin.id} disabled={components.length >= 15} onClick={() => addAdvancedComponent(plugin.id)}><ActionIcon name="create" />Add {componentTitle(primaryCapabilities(plugin)[0] ?? '', selectedCluster?.spec.mode === 'multi').toLowerCase()}</button>)}</div>
      <div className="experiment-component-list">{advancedComponents.filter(component => schedulingGroup(component.capability) === kind).map(component => {
        const plugin = compatiblePlugins.find(item => item.id === component.pluginId)
        const strategyKind = ['scheduler', 'descheduler', 'autoscaler'].find(kind => component.capability.startsWith(`${kind}.`))
        const readyStrategies = strategyKind ? strategies.filter(item => item.workspaceId === workspaceId && item.kind === strategyKind && (item.status === 'succeeded' || item.id === component.strategyId)) : []
        const selectedStrategy = readyStrategies.find(item => item.id === component.strategyId)
        const missingStrategy = component.strategyId && !selectedStrategy ? component.strategyId : ''
        return <section className="experiment-component" key={component.key}>
		  <div className="experiment-component-head"><strong>{componentTitle(component.capability, selectedCluster?.spec.mode === 'multi')}</strong><button type="button" className="icon-button" aria-label="Remove component" title="Remove component" onClick={() => setComponents(items => items.filter(item => item.key !== component.key))}><ActionIcon name="close" /></button></div>
          {strategyKind && <label>Image<select value={component.strategyId ?? ''} required onChange={event => setComponents(items => items.map(item => item.key === component.key ? {...item, strategyId: event.target.value} : item))}><option value="">Select a ready {strategyKind}</option>{missingStrategy && <option value={missingStrategy} disabled>{missingStrategy} · unavailable</option>}{readyStrategies.map(item => <option key={item.id} value={item.id} disabled={item.status !== 'succeeded'}>{item.name}{item.status === 'succeeded' ? '' : ` · ${item.status}`}</option>)}</select></label>}
          {!plugin && <p className="form-error">This component is no longer available. Remove or replace it before saving.</p>}
          {plugin && <SchemaFields key={`${plugin.id}-${component.strategyId ?? ''}`} schema={experimentSchema(plugin.schema, component.capability)} prefix={`component-${component.key}`} values={{...(selectedStrategy?.defaultConfiguration ?? {}), ...(component.configuration ?? {})}} applications={applications} artifacts={[]} connections={[]} credentials={[]} />}
	          {strategyKind === 'autoscaler' && <AutoscalerTargetEditor prefix={`component-${component.key}`} components={effectiveApplicationComponents} defaults={selectedStrategy?.defaultConfiguration ?? {}} resetKey={`${item?.id ?? 'new'}-${applicationRef}-${component.strategyId ?? ''}-${effectiveApplicationComponents.map(item => item.id).join(':')}`} excluded={component.targets?.exclude ?? []} targetOverrides={record(component.configuration?.targetOverrides)} />}
          {(strategyKind === 'scheduler' || strategyKind === 'descheduler') && <StrategyTargetEditor componentKey={component.key} components={effectiveApplicationComponents} trait={strategyKind !== 'scheduler' ? 'deschedulable' : 'schedulable'} resetKey={`${item?.id ?? 'new'}-${applicationRef}-${component.strategyId ?? ''}-${effectiveApplicationComponents.map(item => item.id).join(':')}`} included={component.targets?.include ?? []} excluded={component.targets?.exclude ?? []} />}
        </section>
      })}</div></>}))} /></WizardStep>}
      <WizardStep key="review" title="Review"><WizardReview /></WizardStep>
    </WizardForm>

  return open ? <EditorPage title={item ? `${readOnly ? 'View' : 'Edit'} configuration` : 'Create experiment configuration'} onClose={close} className="experiment-modal">{form}</EditorPage> : null
}

const emptySchema: JsonSchema = {type: 'object', properties: {}}

export function configurableSchema(schema: JsonSchema): JsonSchema {
  const properties = Object.fromEntries(Object.entries(schema.properties ?? {}).filter(([, property]) => property.format !== 'kubephos-artifact-ref' && property.format !== 'kubephos-application-ref' && property.format !== 'kubephos-strategy-image'))
  return {type: 'object', properties, required: (schema.required ?? []).filter(name => name in properties)}
}

function applicationSettingsSchema(schema: JsonSchema, resources: boolean): JsonSchema {
  const hidden = new Set(['initialReplicas', 'replicaOverrides'])
  const resourceFields = new Set(['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit', 'resourceOverrides'])
  const properties = Object.fromEntries(Object.entries(schema.properties ?? {}).filter(([name]) => !hidden.has(name) && resourceFields.has(name) === resources).map(([name, property]) => [name, resources ? {...property, 'x-kubephos-advanced': false} : property]))
  return {...schema, properties, required: (schema.required ?? []).filter(name => name in properties)}
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
 if (cluster?.spec?.mode === 'multi' && Array.isArray(cluster.spec.members)) return cluster.spec.members.flatMap(member => member && typeof member === 'object' && typeof member.name === 'string' ? [member.name] : [])
  const pools = cluster?.spec?.applicationPools
  if (!Array.isArray(pools)) return []
  const values = pools.flatMap(pool => pool && typeof pool === 'object' && 'zones' in pool && Array.isArray(pool.zones) ? pool.zones : [])
  return [...new Set(values.filter((zone): zone is string => typeof zone === 'string' && zone.length > 0))].sort()
}

function schedulingGroup(capability: string): string {
  return ['scheduler', 'descheduler', 'autoscaler'].find(kind => capability.startsWith(`${kind}.`)) ?? 'other'
}

function componentTitle(capability: string, multi = false): string {
  if (capability.startsWith('load.')) return 'Load profile'
  if (capability.startsWith('metrics.')) return 'Monitoring'
  if (capability.startsWith('monitoring.')) return 'Mon-agent profile'
  if (capability.startsWith('chaos.')) return multi ? 'Network injection between clusters' : 'Network injection between zones'
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

function pruneServiceOverrides(values: Record<string, unknown>, application: Application): Record<string, unknown> {
  const prefix = application.descriptor.spec?.valuesSchema?.['x-kubephos-service-prefix']
  const count = Array.isArray(values.models) ? values.models.length : Number(values.serviceCount)
  if (!prefix || !Number.isInteger(count)) return values
  const first = Array.isArray(values.models) ? 1 : 0
  const allowed = new Set(Array.from({length: count}, (_, index) => `${prefix}${index + first}`))
  for (const component of application.descriptor.spec?.interface?.components ?? []) {
    if (!new RegExp(`^${prefix}[0-9]+$`).test(component.id)) allowed.add(component.id)
  }
  for (const field of ['replicaOverrides', 'resourceOverrides']) {
    if (values[field]) values[field] = Object.fromEntries(Object.entries(record(values[field])).filter(([name]) => allowed.has(name)))
  }
  return values
}

function normalizeApplicationValues(application: Application | undefined, values: Record<string, unknown>): Record<string, unknown> {
  const schema = application?.descriptor.spec?.valuesSchema
  if (application?.descriptor.spec?.interface?.components?.some(component => component.id === 'inference-router') && values.routerReplicas !== undefined && schema?.properties?.initialReplicas && !schema.properties.routerReplicas) {
    const overrides = record(values.replicaOverrides)
    return {...values, replicaOverrides: {...overrides, 'inference-router': values.routerReplicas}}
  }
  return values
}
