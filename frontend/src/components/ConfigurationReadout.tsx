import type {ReactNode} from 'react'
import {workloadTable,trafficTable} from './PhaseReadout'
import type {Application,CatalogStrategy,ExperimentConfiguration,ExperimentConfigurationComponent,ManagedResource,Plugin,Session} from '../types'
import {ReadoutTable,type ReadoutTableData} from './ReadoutTable'
import {FormTabs} from './FormTabs'
import {FormDisclosure} from './FormDisclosure'
import {InputData} from './InputData'
import {LoadProfilePreview,loadProfileSteps} from './LoadProfileEditor'
import {WizardReadout} from './WizardReadout'
import {ApplicationSettingsReadout} from './ApplicationSettingsReadout'

function PhaseTables({table,label}:{table:ReadoutTableData;label:string}) {
 return <FormTabs label={label} items={table.rows.map((row,index)=>({id:String(index),title:`Phase ${index+1}`,content:<ReadoutTable label={`${label} · Phase ${index+1}`} data={{headers:table.headers,rows:[row]}}/>}))}/>
}
export function ConfigurationReadout({session,item,clusters,applications,plugins,strategies,showHeading=true,applicationTopology,deploymentMode=false}:{session?:Session;deploymentMode?:boolean;showHeading?:boolean;applicationTopology?:ReactNode;item:ExperimentConfiguration;clusters:ManagedResource[];applications:Application[];plugins:Plugin[];strategies:CatalogStrategy[]}) {
 const application=applications.find(value=>value.reference===item.applicationRef)
 const cluster=clusters.find(value=>value.id===item.clusterResourceId)
 const external=item.definition.destinationMode==='external'
 const components=item.definition.components??[]
 const network=components.filter(value=>value.capability.startsWith('chaos.'))
 const metrics=components.filter(value=>value.capability.startsWith('metrics.'))
 const agents=components.filter(value=>value.capability.startsWith('monitoring.'))
 const load=components.filter(value=>value.capability.startsWith('load.'))
 const scheduling=components.filter(value=>!/^(chaos\.|metrics\.|monitoring\.|load\.)/.test(value.capability))
 const multi=cluster?.spec.mode==='multi'
 const references=Object.fromEntries([...clusters.map(value=>[value.id,value.name]),...applications.map(value=>[value.reference,value.name]),...strategies.map(value=>[value.id,value.name])])
 const componentReadout=(component:ExperimentConfigurationComponent)=>{
  const plugin=plugins.find(value=>value.id===component.pluginId)
  const strategy=strategies.find(value=>value.id===component.strategyId)
  const {configFile,parameters,...settings}=component.configuration
  const configuration=configFile??parameters
  return <section className="readout-section" key={component.id}><h4>{strategy?.name??plugin?.name??component.capability}</h4><InputData value={settings} schema={plugin?.schema} references={references}/>{typeof configuration==='string'&&configuration.trim()&&<FormDisclosure title="Configuration"><InputData value={configuration}/></FormDisclosure>}{(component.targets?.include?.length>0||component.targets?.exclude?.length>0)&&<InputData value={{includedServices:component.targets.include,excludedServices:component.targets.exclude}}/>}</section>
 }
 const duration=load.reduce((total,component)=>Math.max(total,loadProfileSteps(component.configuration.steps).reduce((sum,step)=>sum+step.durationSeconds,0)),0)
 const general=<InputData value={{clusterResourceId:item.clusterResourceId,...(!deploymentMode?{destinationType:external?'External destination':'Catalog application'}:{}),...(external?{}:{applicationRef:item.applicationRef,namespace:item.definition.namespace||'—'}),...(!deploymentMode?{startMode:item.definition.twoPhaseStart?'Manual':'Automatic'}:{})}} references={references}/>
 return <div className="configuration-readout">{showHeading&&<div className="readout-heading"><h3>{item.name}</h3></div>}<WizardReadout sections={[
 {id:'general',title:'General',content:general},
 {id:'application',title:external?'External destination':'Application',content:external?<FormTabs label="External destination settings" items={[
 {id:'target',title:'Endpoints',content:<div className="form-stack"><ReadoutTable label="External endpoints" data={{headers:['Label','URL'],rows:(item.definition.externalDestination?.endpoints?.length ? item.definition.externalDestination.endpoints : [{label:'Endpoint 1',url:item.definition.externalDestination?.targetUrl ?? '—'}]).map(endpoint=>[endpoint.label,endpoint.url])}}/><InputData value={{runtimeImage:item.definition.externalDestination?.runtimeImage}}/></div>},
 {id:'script',title:'Locustfile',content:<InputData value={{script:item.definition.externalDestination?.script}}/>},
 {id:'variables',title:'Variables',content:<InputData value={{parameters:item.definition.externalDestination?.parameters}}/>}
 ]}/>:<ApplicationSettingsReadout deploymentId={deploymentMode?item.id:undefined} application={application} item={item} session={session} networking={multi?'cilium':String(cluster?.spec.networking??'')} topology={applicationTopology}/>},
 ...(!deploymentMode?[{id:'load',title:'Load profile',content:<div className="form-stack">{load.map(component=>{
  const steps=loadProfileSteps(component.configuration.steps)
  const {steps:_steps,geographicSteps,action:_action,interactive:_interactive,scenarioId,...settings}=component.configuration
  if((application?.descriptor.spec?.interface?.loadScenarios?.length??0)>1)settings.scenarioId=scenarioId
  return <FormTabs key={component.id} label="Load profile settings" items={[
   {id:'workload',title:'Workload',content:<div className="load-profile-workspace"><PhaseTables label="Workload phases" table={workloadTable(steps)}/><aside className="load-profile-live" aria-label="Live preview"><LoadProfilePreview steps={steps}/></aside></div>},
   ...(Array.isArray(geographicSteps)&&geographicSteps.length?[{id:'distribution',title:'Traffic distribution',content:<PhaseTables label={external?'Traffic phases by endpoint':multi?'Traffic phases by cluster':'Traffic phases by zone'} table={trafficTable(geographicSteps as Record<string,unknown>[],multi,external)}/>}]:[]),
   {id:'generator',title:'Generator',content:<InputData value={settings} schema={plugins.find(value=>value.id===component.pluginId)?.schema}/>}
  ]}/>
 })}</div>},
 {id:'monitoring',title:'Monitoring',content:<FormTabs label="Monitoring settings" items={[
 {id:'metrics',title:'Metrics',content:<div className="form-stack">{metrics.map(componentReadout)}</div>},
 ...(!external?[{id:'network',title:'Network',content:<div className="form-stack"><h4>{multi?'Network between clusters':'Network between zones'}</h4><ReadoutTable label="Network injection" data={{headers:['Field','Value'],rows:[['Enable injection',network.length?'Enabled':'Disabled']]}}/>{network.map(component=>{
  const value=component.configuration
  const phases=Array.isArray(value.phases)&&value.phases.length?value.phases as Record<string,unknown>[]:[{...value,durationSeconds:duration}]
  return <section key={component.id} className="form-stack"><FormTabs label="Network phases" items={phases.map((phase,index)=>({id:String(index),title:`Phase ${index+1}`,content:<div className="form-stack"><ReadoutTable label={`Phase ${index+1} duration`} data={{headers:['Field','Value'],rows:[['Duration (min)',String(Number(phase.durationSeconds)/60)]]}}/><NetworkReadout component={{...component,configuration:phase}} clusterMode={multi} showTarget={false}/></div>}))}/><FormDisclosure title="Network target"><InputData value={{nodeGroupLabel:value.nodeGroupLabel,nodeSelector:value.nodeSelector,networkInterface:value.networkInterface,hostNetwork:value.hostNetwork}}/></FormDisclosure></section>
 })}</div>},{id:'agent',title:'Agent',content:<div className="form-stack"><ReadoutTable label="Agent" data={{headers:['Field','Value'],rows:[['Customize mon-agent for this experiment',agents.length?'Enabled':'Disabled']]}}/>{agents.map(componentReadout)}</div>}]:[])
 ]}/>},
 ...(!external?[{id:'scheduling',title:'Scheduling and scaling',content:<FormTabs label="Scheduling and scaling settings" items={([['scheduler.','Scheduler'],['descheduler.','Descheduler'],['autoscaler.','Autoscaler'],['','Other']] as const).map(([prefix,title])=>({id:prefix||'other',title,content:<div className="form-stack">{scheduling.filter(component=>prefix?component.capability.startsWith(prefix):!/^(scheduler\.|descheduler\.|autoscaler\.)/.test(component.capability)).map(componentReadout)}</div>}))}/>}]:[])] : [])
 ]}/></div>
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
