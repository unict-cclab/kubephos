import {ErrorFeedback} from './ToastRegion'
import {CodeBlock} from './CodeBlock'
import {useErrorState} from './ToastRegion'
import {FormTabs} from './FormTabs'
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
  return <div className="detail-page"><header className="page-header"><DetailBreadcrumb parent="Catalog" current={application.name} back={close} /><div className="detail-page-heading"><div><h2>{application.name}</h2></div><div className="detail-page-actions">{canDelete && <ResourceAction action="delete" label={`Delete ${application.name} from catalog`} onClick={() => remove(application)} />}</div></div></header><ResourceAttribution resource={application} editable /><article className="detail-page-card">
    <ErrorFeedback message={manifestError}/>
    <FormTabs key={application.reference} label="Application details" onSelect={id=>{
      if(id==='deployment'&&!manifest&&!manifestLoading)void loadPreview()
      if(id==='locust'&&!selectedScenario&&!manifestLoading&&scenarios[0]?.id)void showScenario(String(scenarios[0].id))
    }} items={[
      {id:'general',title:'General',content:<ReadoutTable label="Application information" data={{headers:['Field','Value'],rows:[
        ...(application.description?[['Description',application.description]]:[]),
        ['Source',application.origin==='built-in'?'Included':'Imported'],
        ['Services',dynamicTopology?'Configurable':String(components.length)],['Load scenarios',String(scenarios.length)]
      ]}}/>},
      ...(!dynamicTopology?[
        {id:'topology',title:'Topology',content:<ApplicationTopologyGraph components={components}/>},
        {id:'services',title:'Services',content:<ReadoutTable label="Services" data={{headers:['Name','Workload type','Workload name','Traits'],rows:orderedComponents.map(component=>[component.id,component.workload?.kind??'—',component.workload?.name??'—',component.traits?.join(', ')||'—'])}}/>}
      ]:[]),
      {id:'deployment',title:dynamicTopology?'Example deployment files':'Deployment files',content:<CodeBlock label="Deployment manifest YAML" actions={<><button className="button secondary compact" type="button" disabled={!manifest} onClick={downloadManifest}>Download</button><button className="button secondary compact" type="button" disabled={manifestLoading} onClick={()=>void loadPreview(undefined,Boolean(manifest))}>{manifestLoading?'Preparing…':'Refresh'}</button></>}>{manifest}</CodeBlock>},
      {id:'locust',title:dynamicTopology?'Example Locust file':'Locust file',content:scenarios.length?<CodeBlock label={`Locust file for ${selectedScenarioLabel}`} actions={<>{scenarios.length>1&&<select aria-label="Scenario" value={selectedScenarioID||String(scenarios[0]?.id??'')} onChange={event=>void showScenario(event.target.value)}>{scenarios.map((scenario,index)=><option key={String(scenario.id??index)} value={String(scenario.id??index)}>{scenarioLabel(index,scenarios.length)}</option>)}</select>}<button className="button secondary compact" type="button" disabled={!selectedScenario} onClick={downloadScenario}>Download</button><button className="button secondary compact" type="button" disabled={manifestLoading} onClick={()=>void loadPreview(selectedScenarioID||String(scenarios[0]?.id??''),Boolean(manifest))}>{manifestLoading?'Preparing…':'Refresh'}</button></>}>{selectedScenario?.script??''}</CodeBlock>:<p className="details-copy">No Locust file available.</p>}

    ]}/>
  </article></div>
}

function scenarioLabel(index: number, total: number): string {
  if (total <= 1 || index <= 0) return 'default'
  return `scenario ${index + 1}`
}
