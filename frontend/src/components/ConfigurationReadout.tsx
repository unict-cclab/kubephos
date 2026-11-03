import type {ReactNode} from 'react'
import {WorkloadReadout, TrafficDistributionReadout} from './PhaseReadout'
import type {Application, CatalogStrategy, ExperimentConfiguration, ExperimentConfigurationComponent, ManagedResource, Plugin} from '../types'
import {ReadoutTable} from './ReadoutTable'
import {FormTabs} from './FormTabs'
import {FormDisclosure} from './FormDisclosure'
import {InputData} from './InputData'
import {LoadProfilePreview, loadProfileSteps} from './LoadProfileEditor'

export function ConfigurationReadout({item, clusters, applications, plugins, strategies, showHeading = true, applicationTopology}: {showHeading?: boolean; applicationTopology?: ReactNode; item: ExperimentConfiguration; clusters: ManagedResource[]; applications: Application[]; plugins: Plugin[]; strategies: CatalogStrategy[]}) {
  const application = applications.find(value => value.reference === item.applicationRef)
  const cluster = clusters.find(value => value.id === item.clusterResourceId)
  const endpoints = application?.descriptor.spec?.interface?.endpoints ?? []
  const defaultIstio = endpoints.length === 0 || !endpoints.every(endpoint => endpoint.protocol === 'tcp')
  const external = item.definition.destinationMode === 'external'
  const components = item.definition.components ?? []
  const network = components.filter(value => value.capability.startsWith('chaos.'))
  const monitoring = components.filter(value => /^(metrics\.|monitoring\.)/.test(value.capability))
  const load = components.filter(value => value.capability.startsWith('load.'))
  const scheduling = components.filter(value => !/^(chaos\.|metrics\.|monitoring\.|load\.)/.test(value.capability))
  const references = Object.fromEntries([...clusters.map(value => [value.id, value.name]), ...applications.map(value => [value.reference, value.name]), ...strategies.map(value => [value.id, value.name])])
  const componentReadout = (component: ExperimentConfigurationComponent) => {
    const plugin = plugins.find(value => value.id === component.pluginId)
    const strategy = strategies.find(value => value.id === component.strategyId)
    const {configFile, parameters, ...settings} = component.configuration
    const configuration = configFile ?? parameters
    return <section className="readout-section" key={component.id}><h3>{strategy?.name ?? plugin?.name ?? component.capability}</h3><InputData value={settings} schema={plugin?.schema} references={references} />{typeof configuration === 'string' && configuration.trim() && <FormDisclosure title={'Configuration'}><InputData value={configuration} /></FormDisclosure>}{(component.targets?.include?.length > 0 || component.targets?.exclude?.length > 0) && <InputData value={{includedServices: component.targets.include, excludedServices: component.targets.exclude}} />}</section>
  }
  return <div className="configuration-readout">{showHeading && <div className="readout-heading"><h3>{item.name}</h3></div>}<FormTabs label="Configuration sections" items={[
    {id: 'general', title: 'General', content: <InputData value={{clusterResourceId: item.clusterResourceId, namespace:external?undefined:item.definition.namespace||'Automatic',startMode:item.definition.twoPhaseStart?'Manual':'Automatic', ...(external ? {targetUrl: item.definition.externalDestination?.targetUrl} : {applicationRef: item.applicationRef}), podDisruptionBudgetMinAvailable: item.definition.podDisruptionBudgetMinAvailable}} references={references} />},
    {id: 'application', title: external ? 'External destination' : 'Application', content: external ? <InputData value={item.definition.externalDestination} /> : <div className="form-stack"><InputData value={{istioInjection: (cluster?.spec.mode !== 'multi' && (item.definition.istioInjection ?? defaultIstio)) ? 'Enabled' : 'Disabled', ...(Object.keys(item.definition.istioInjectionOverrides ?? {}).length ? {istioInjectionOverrides:item.definition.istioInjectionOverrides} : {})}} /><InputData value={{...(application?.descriptor.spec?.defaults ?? {}), ...item.definition.applicationValues}} schema={application?.descriptor.spec?.valuesSchema} />{applicationTopology && <FormDisclosure title="Topology">{applicationTopology}</FormDisclosure>}</div>},
    ...(load.length ? [{id: 'load', title: 'Load profile', content: <div className="form-stack">{load.map(component => {
      const steps = loadProfileSteps(component.configuration.steps)
      const {steps: _steps, geographicSteps, action: _action, interactive: _interactive, scenarioId, ...settings} = component.configuration
      if ((application?.descriptor.spec?.interface?.loadScenarios?.length ?? 0) > 1) settings.scenarioId = scenarioId
      return <section className="readout-section" key={component.id}><InputData value={settings} schema={plugins.find(value => value.id === component.pluginId)?.schema} /><FormDisclosure title="Workload phases" defaultOpen><WorkloadReadout phases={steps} /></FormDisclosure>{Array.isArray(geographicSteps) && geographicSteps.length > 0 && <FormDisclosure title={cluster?.spec.mode === 'multi' ? 'Traffic by cluster' : 'Traffic by zone'}><TrafficDistributionReadout phases={geographicSteps as Record<string, unknown>[]} clusterMode={cluster?.spec.mode === 'multi'} /></FormDisclosure>}<FormDisclosure title="Preview"><LoadProfilePreview steps={steps} /></FormDisclosure></section>
    })}</div>}] : []),
    ...(monitoring.length ? [{id: 'monitoring', title: 'Monitoring', content: <div className="form-stack">{monitoring.map(componentReadout)}</div>}] : []),
    ...(network.length ? [{id: 'network', title: 'Network', content: <div className="form-stack">{network.map(component => <NetworkReadout key={component.id} component={component} clusterMode={cluster?.spec.mode === 'multi'} />)}</div>}] : []),
    ...([['scheduler.', 'Scheduling'], ['descheduler.', 'Descheduling'], ['autoscaler.', 'Scaling'], ['', 'Other']] as const).flatMap(([prefix, title]) => {
      const group = scheduling.filter(component => prefix ? component.capability.startsWith(prefix) : !/^(scheduler\.|descheduler\.|autoscaler\.)/.test(component.capability))
      return group.length ? [{id: prefix || 'other', title, content: <div className="form-stack">{group.map(componentReadout)}</div>}] : []
    })
  ]} /></div>
}

function NetworkReadout({component, clusterMode, showTarget=true}: {component: ExperimentConfigurationComponent; clusterMode: boolean; showTarget?: boolean}) {
  const value = component.configuration
  if (Array.isArray(value.phases) && value.phases.length) return <div className="form-stack"><ReadoutTable label="Network phase durations" data={{headers:['Phase','Duration (min)'],rows:(value.phases as Record<string,unknown>[]).map((phase,index)=>[String(index+1),String(Number(phase.durationSeconds)/60)])}}/><FormTabs label="Network phase details" items={(value.phases as Record<string,unknown>[]).map((phase,index)=>({id:String(index),title:`Phase ${index+1}`,content:<NetworkReadout component={{...component,configuration:phase}} clusterMode={clusterMode} showTarget={false}/>}))}/><FormDisclosure title="Network target"><InputData value={{nodeGroupLabel:value.nodeGroupLabel,nodeSelector:value.nodeSelector,networkInterface:value.networkInterface,hostNetwork:value.hostNetwork}}/></FormDisclosure></div>
  const effects = {...(value.enableLatency ? {latencyMilliseconds: value.latencyMilliseconds, jitterMilliseconds: value.jitterMilliseconds} : {}), ...(value.enableBandwidth ? {bandwidthBytesPerSecond: value.bandwidthBytesPerSecond} : {}), ...(value.enablePacketLoss ? {packetLossPercent: value.packetLossPercent} : {})}
  const links = Array.isArray(value.linkOverrides) ? value.linkOverrides as Record<string, unknown>[] : []
  const metrics = [{key: 'latencyMilliseconds', title: 'One-way latency (ms)'}, {key: 'jitterMilliseconds', title: 'Jitter (ms)'}, {key: 'bandwidthBytesPerSecond', title: 'Bandwidth (B/s)'}, {key: 'packetLossPercent', title: 'Packet loss (%)'}]
  return <div className="form-stack">
    {Object.keys(effects).length===0&&links.length===0&&<InputData value={{networkEffects:"None"}}/>}
    {Object.keys(effects).length > 0 && <section className="readout-section"><h3>Shared profile</h3><ReadoutTable label="Shared profile" data={{headers:['Field','Value'],rows:Object.entries(effects).map(([key,value])=>[metrics.find(metric=>metric.key===key)?.title??key,String(value)])}} /></section>}
    {links.length > 0 && <section className="readout-section"><h3>{clusterMode ? 'Cluster links' : 'Zone links'}</h3><ReadoutTable label={clusterMode ? 'Cluster links' : 'Zone links'} data={{headers: ['Source', 'Destination', 'Direction', ...metrics.map(metric => metric.title)], rows: links.map(link => [String(link.sourceZone), String(link.targetZone), link.bidirectional===true?'Bidirectional':'One-way', ...metrics.map(metric => link[metric.key] === undefined && effects[metric.key as keyof typeof effects] === undefined ? '—' : String(link[metric.key] ?? effects[metric.key as keyof typeof effects]))])}} /></section>}
    {showTarget && <FormDisclosure title="Network target"><InputData value={{nodeGroupLabel: value.nodeGroupLabel, nodeSelector: value.nodeSelector, networkInterface: value.networkInterface, hostNetwork: value.hostNetwork}} /></FormDisclosure>}
  </div>
}
