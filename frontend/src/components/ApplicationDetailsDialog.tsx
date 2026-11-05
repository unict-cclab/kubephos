import {CodeBlock} from './CodeBlock'
import {useErrorState} from './ToastRegion'
import {ReadoutTable} from './ReadoutTable'
import {ResourceAttribution} from './ResourceAttribution'
import {ResourceAction} from './ResourceAction'
import {useEffect, useState, type ReactNode} from 'react'
import {request} from '../api'
import type {Application} from '../types'
import {ApplicationTopologyGraph} from './ApplicationTopologyGraph'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {ActionIcon, type ActionIconName} from './ActionIcon'
import {applicationMonogram} from './ApplicationGrid'

interface Props {
  application: Application | null
  close: () => void
  canDelete: boolean
  remove: (application: Application) => Promise<boolean>
}

interface MaterializedLoadScenario {
  id: string
  engine: string
  targetEndpoint: string
  runtimeImage: string
  script: string
  scriptDigest: string
}

interface ApplicationPreview {
  content: string
  loadScenarios: MaterializedLoadScenario[]
}

// Reads top-down: what the application is, how it is built, then the files KubePhos generates.
export function ApplicationDetailsPage({application, close, canDelete, remove}: Props) {
  const [manifest, setManifest] = useState('')
  const [manifestLoading, setManifestLoading] = useState(false)
  const [manifestError, setManifestError] = useErrorState()
  const [materializedScenarios, setMaterializedScenarios] = useState<MaterializedLoadScenario[]>([])
  const [selectedScenarioID, setSelectedScenarioID] = useState('')
  useEffect(() => { setManifest(''); setManifestError(''); setManifestLoading(false); setMaterializedScenarios([]); setSelectedScenarioID('') }, [application?.reference])
  if (!application) return null
  const specification = application.descriptor.spec ?? {}
  const contract = specification.interface ?? {}
  const components = contract.components ?? []
  const scenarios = contract.loadScenarios ?? []
  const dynamicTopology = Boolean(specification.valuesSchema?.['x-kubephos-dynamic-topology'])
  const orderedComponents = [...components].sort((left, right) => left.id.localeCompare(right.id))
  const loadPreview = async (scenarioID?: string, refresh = false) => {
    setManifestLoading(true)
    setManifestError('')
    try {
      const result = await request<ApplicationPreview>(`/catalog/applications/${encodeURIComponent(application.id)}/manifest${refresh ? '?refresh=true' : ''}`)
      setManifest(result.content)
      setMaterializedScenarios(result.loadScenarios ?? [])
      if (scenarioID) {
        if (!(result.loadScenarios ?? []).some(scenario => scenario.id === scenarioID)) throw new Error('The selected Locust file is not available.')
        setSelectedScenarioID(scenarioID)
      }
    } catch (cause) {
      setManifestError(cause instanceof Error ? cause.message : 'Could not prepare the application preview.')
    } finally {
      setManifestLoading(false)
    }
  }
  const showScenario = async (scenarioID: string) => {
    if (materializedScenarios.some(scenario => scenario.id === scenarioID)) {
      setSelectedScenarioID(scenarioID)
      return
    }
    await loadPreview(scenarioID)
  }
  const downloadManifest = () => {
    const url = URL.createObjectURL(new Blob([manifest], {type: 'application/yaml;charset=utf-8'}))
    const link = document.createElement('a')
    link.href = url
    link.download = `${application.name.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replaceAll(/(^-|-$)/g, '') || 'application'}.yml`
    link.click()
    URL.revokeObjectURL(url)
  }
  const selectedScenario = materializedScenarios.find(scenario => scenario.id === selectedScenarioID)
  const selectedScenarioLabel = scenarioLabel(materializedScenarios.findIndex(scenario => scenario.id === selectedScenarioID), materializedScenarios.length)
  const downloadScenario = () => {
    if (!selectedScenario) return
    const url = URL.createObjectURL(new Blob([selectedScenario.script], {type: 'text/x-python;charset=utf-8'}))
    const link = document.createElement('a')
    link.href = url
    link.download = `${selectedScenarioLabel.replaceAll(' ', '-')}.py`
    link.click()
    URL.revokeObjectURL(url)
  }
  const openDeploymentFiles = (open: boolean) => {
    if (open && !manifest && !manifestLoading) void loadPreview()
  }
  const openLocustFile = (open: boolean) => {
    const scenarioID = String(scenarios[0]?.id ?? '')
    if (open && scenarioID && !selectedScenario && !manifestLoading) void showScenario(scenarioID)
  }

  return <div className="detail-page application-detail">
    <DetailBreadcrumb parent="Library" current={application.name} back={close} />
    <header className="application-hero">
      <span className="application-monogram" aria-hidden="true">{applicationMonogram(application.name)}</span>
      <div className="application-hero-copy">
        <div className="application-hero-title"><h2>{application.name}</h2><span className="application-origin">{application.origin === 'built-in' ? 'Included' : 'Imported'}</span></div>
        {application.description && <p className="application-lead">{application.description}</p>}
      </div>
      {canDelete && <div className="detail-page-actions"><ResourceAction action="delete" label={`Delete ${application.name} from catalog`} onClick={() => remove(application)} /></div>}
    </header>

    <dl className="application-facts">
      {!dynamicTopology && <div><dt>Services</dt><dd className="application-fact-figure">{components.length}</dd></div>}
      <div><dt>Load scenarios</dt><dd className="application-fact-figure">{scenarios.length}</dd></div>
      <div><dt>Topology</dt><dd>{dynamicTopology ? 'Generated for each experiment' : 'Fixed by the application'}</dd></div>
    </dl>
    <ResourceAttribution resource={application} editable />

    {!dynamicTopology && <ApplicationPanel title="Topology" description="How requests travel between the services, from the entry point inward."><ApplicationTopologyGraph components={components} heading={null} /></ApplicationPanel>}
    {!dynamicTopology && <ApplicationPanel title="Services" description="Workloads KubePhos can scale, schedule and observe during an experiment."><ReadoutTable label="Services" data={{headers: ['Name', 'Workload type', 'Workload name', 'Traits'], rows: orderedComponents.map(component => [component.id, component.workload?.kind ?? '—', component.workload?.name ?? '—', component.traits?.join(', ') || '—'])}} /></ApplicationPanel>}

    <ApplicationPanel title="Files" description={dynamicTopology ? 'Examples generated with the application defaults. Each experiment generates its own files from the selected configuration.' : 'What KubePhos installs on the cluster and how it generates load.'}>
      <div className="application-file-selectors">
        <FileDisclosure icon="file" title={dynamicTopology ? 'Example deployment files' : 'Deployment files'} subtitle="Kubernetes manifests in YAML" onToggle={openDeploymentFiles}>
          <div className="details-heading"><strong>Manifest YAML</strong><div className="chart-export-actions">{manifest && <button className="button secondary compact" type="button" onClick={downloadManifest}>Download .yml</button>}<button className="button secondary compact" type="button" disabled={manifestLoading} onClick={() => void loadPreview(undefined, Boolean(manifest))}>{manifestLoading ? 'Preparing…' : manifest ? 'Refresh' : 'Load'}</button></div></div>
          {manifestError && <p className="form-error">{manifestError}</p>}
          {manifest && <CodeBlock label="Deployment manifest YAML">{manifest}</CodeBlock>}
        </FileDisclosure>
        <FileDisclosure icon="pulse" title={dynamicTopology ? 'Example Locust file' : 'Locust file'} subtitle="Python script that generates the load" onToggle={openLocustFile}>
          {scenarios.length ? <>
            {scenarios.length > 1 && <label>Scenario<select value={selectedScenarioID} onChange={event => void showScenario(event.target.value)}>{scenarios.map((scenario, index) => <option key={String(scenario.id ?? index)} value={String(scenario.id ?? index)}>{scenarioLabel(index, scenarios.length)}</option>)}</select></label>}
            {manifestError && <p className="form-error">{manifestError}</p>}
            {selectedScenario && <><div className="details-heading"><strong>{selectedScenarioLabel}.py</strong><div className="chart-export-actions"><button className="button secondary compact" type="button" onClick={downloadScenario}>Download .py</button></div></div><CodeBlock label={`Locust file for ${selectedScenarioLabel}`}>{selectedScenario.script}</CodeBlock></>}
          </> : <p className="details-copy">No Locust file available.</p>}
        </FileDisclosure>
      </div>
    </ApplicationPanel>
  </div>
}

function ApplicationPanel({title, description, children}: {title: string; description: string; children: ReactNode}) {
  return <section className="application-panel" aria-label={title}>
    <header className="application-panel-head"><h3>{title}</h3><p>{description}</p></header>
    {children}
  </section>
}

function FileDisclosure({icon, title, subtitle, onToggle, children}: {icon: ActionIconName; title: string; subtitle: string; onToggle: (open: boolean) => void; children: ReactNode}) {
  return <details className="technical-disclosure application-file-disclosure" onToggle={event => onToggle(event.currentTarget.open)}>
    <summary><span className="application-file-icon" aria-hidden="true"><ActionIcon name={icon} /></span><span className="application-file-title"><strong>{title}</strong><small>{subtitle}</small></span></summary>
    <div className="application-file-content">{children}</div>
  </details>
}

function scenarioLabel(index: number, total: number): string {
  if (total <= 1 || index <= 0) return 'default'
  return `scenario ${index + 1}`
}
