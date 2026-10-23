import {ReadoutTable} from './ReadoutTable'
import {ResourceAttribution} from './ResourceAttribution'
import {ResourceAction} from './ResourceAction'
import {useEffect, useState} from 'react'
import {request} from '../api'
import type {Application} from '../types'
import {ApplicationTopologyGraph} from './ApplicationTopologyGraph'
import {DetailBreadcrumb} from './DetailBreadcrumb'

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

export function ApplicationDetailsPage({application, close, canDelete, remove}: Props) {
  const [manifest, setManifest] = useState('')
  const [manifestLoading, setManifestLoading] = useState(false)
  const [manifestError, setManifestError] = useState('')
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
  return <div className="detail-page"><DetailBreadcrumb parent="Catalog" current={application.name} back={close} /><div className="detail-page-heading"><div><h2>{application.name}</h2></div><div className="detail-page-actions">{canDelete && <ResourceAction action="delete" label={`Delete ${application.name} from catalog`} onClick={() => remove(application)} />}</div></div><ResourceAttribution resource={application} editable /><article className="detail-page-card">

    <div className="details-summary">
      {application.description && <div><span>Description</span><strong>{application.description}</strong></div>}
      <div><span>Source</span><strong>{application.origin === 'built-in' ? 'Included' : 'Imported'}</strong></div>
      {!dynamicTopology && <div><span>Services</span><strong>{components.length}</strong></div>}


      <div><span>Load scenarios</span><strong>{scenarios.length}</strong></div>
    </div>
    {!dynamicTopology && <section className="details-section"><div className="details-heading"><strong>Topology</strong></div><ApplicationTopologyGraph components={components} /></section>}
    {!dynamicTopology && <section className="details-section"><div className="details-heading"><strong>Services</strong></div><ReadoutTable label="Services" data={{headers: ['Name', 'Workload type', 'Workload name', 'Traits'], rows: orderedComponents.map(component => [component.id, component.workload?.kind ?? '—', component.workload?.name ?? '—', component.traits?.join(', ') || '—'])}} /></section>}
    <div className="application-file-selectors">
      <details className="technical-disclosure application-file-disclosure" onToggle={event => openDeploymentFiles(event.currentTarget.open)}>
        <summary><strong>{dynamicTopology ? 'Example deployment files' : 'Deployment files'}</strong></summary>
        <div className="application-file-content">{dynamicTopology && <p className="details-copy">Example deployment manifests generated using the application defaults. Experiment manifests are generated from the selected configuration.</p>}<div className="details-heading"><strong>Manifest YAML</strong><div className="chart-export-actions">{manifest && <button className="button secondary compact" type="button" onClick={downloadManifest}>Download .yml</button>}<button className="button secondary compact" type="button" disabled={manifestLoading} onClick={() => void loadPreview(undefined, Boolean(manifest))}>{manifestLoading ? 'Preparing…' : manifest ? 'Refresh' : 'Load'}</button></div></div>{manifestError && <p className="form-error">{manifestError}</p>}{manifest && <pre aria-label="Deployment manifest YAML">{manifest}</pre>}</div>
      </details>
      <details className="technical-disclosure application-file-disclosure" onToggle={event => openLocustFile(event.currentTarget.open)}>
        <summary><strong>{dynamicTopology ? 'Example Locust file' : 'Locust file'}</strong></summary>
        <div className="application-file-content">{dynamicTopology && <p className="details-copy">Example Locust script generated using the application defaults. The experiment script is generated from the selected configuration.</p>}{scenarios.length ? <>{scenarios.length > 1 && <label>Scenario<select value={selectedScenarioID} onChange={event => void showScenario(event.target.value)}>{scenarios.map((scenario, index) => <option key={String(scenario.id ?? index)} value={String(scenario.id ?? index)}>{scenarioLabel(index, scenarios.length)}</option>)}</select></label>}{manifestError && <p className="form-error">{manifestError}</p>}{selectedScenario && <><div className="details-heading"><strong>{selectedScenarioLabel}.py</strong><div className="chart-export-actions"><button className="button secondary compact" type="button" onClick={downloadScenario}>Download .py</button></div></div><pre aria-label={`Locust file for ${selectedScenarioLabel}`}>{selectedScenario.script}</pre></>}</> : <p className="details-copy">No Locust file available.</p>}</div>
      </details>
    </div>
  </article></div>
}

function scenarioLabel(index: number, total: number): string {
  if (total <= 1 || index <= 0) return 'default'
  return `scenario ${index + 1}`
}
