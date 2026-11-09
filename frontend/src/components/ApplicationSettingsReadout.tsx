import {ProxyResourceDefaults} from './ProxyResourceDefaults'
import {IstioInjectionReadout} from './IstioInjectionEditor'
import {useState} from 'react'
import type {ReactNode} from 'react'
import type {Application,ExperimentConfiguration,Session} from '../types'
import {applicationSettingsSchema} from './applicationSettings'
import {hasConfigurableFields} from './SchemaFields'
import {FormTabs} from './FormTabs'
import {InputData} from './InputData'
import {ReadoutTable} from './ReadoutTable'
import {PlacementReadout} from './ApplicationPlacementEditor'

export function ResourceSettingsReadout({value={},label='Resources',defaults}:{defaults?:ReactNode;value?:Record<string,unknown>;label?:string}) {
 const [section,setSection]=useState('common')
 const rows=(settings:Record<string,unknown>,common:Record<string,unknown>={})=>['CPU','Memory'].map(name=>[name,...['Request','Limit'].map(kind=>String(settings[`${name.toLowerCase()}${kind}`]??common[`${name.toLowerCase()}${kind}`]??'—'))])
 const overrides=value.resourceOverrides&&typeof value.resourceOverrides==='object'?value.resourceOverrides as Record<string,Record<string,unknown>>:{}
 return <FormTabs selectedId={section} onSelect={setSection} label={`${label} settings`} items={[
 {id:'common',title:'Common settings',content:<ReadoutTable label={label} data={{headers:['Resource','Request','Limit'],rows:rows(value)}}/>},
 {id:'services',title:'Service overrides',content:Object.keys(overrides).length?<FormTabs label={`${label} overrides`} items={Object.entries(overrides).map(([id,settings])=>({id,title:id,content:<ReadoutTable label={`${id} ${label.toLowerCase()}`} data={{headers:['Resource','Request','Limit'],rows:rows(settings,value)}}/>}))}/>:<ReadoutTable label="Service overrides" data={{headers:['Field','Value'],rows:[['Service overrides','None']]}}/>},...(defaults?[{id:'defaults',title:'Defaults',content:section==='defaults'?defaults:null}]:[])
 ]}/>

}

export function ApplicationSettingsReadout({application,item,session,networking,topology,deploymentId}:{deploymentId?:string;application?:Application;item:ExperimentConfiguration;session?:Session;networking?:string;topology?:ReactNode}) {
 const schema=application?.descriptor.spec?.valuesSchema??{type:'object'}
 const values={...application?.descriptor.spec?.defaults,...item.definition.applicationValues}
 const parameters=applicationSettingsSchema(schema,false)
 const resources=applicationSettingsSchema(schema,true)
 const replicaOverrides=values.replicaOverrides&&typeof values.replicaOverrides==='object'?values.replicaOverrides as Record<string,unknown>:{}
 const replicas=Boolean(schema.properties?.initialReplicas)
 const tcp=Boolean(application?.descriptor.spec?.interface?.endpoints?.length)&&application!.descriptor.spec!.interface!.endpoints!.every(endpoint=>endpoint.protocol==='tcp')
 const istio=networking!=='cilium'&&(item.definition.istioInjection??!tcp)
 const parameterValues=Object.fromEntries(Object.entries(values).filter(([name])=>!['initialReplicas','replicaOverrides','cpuRequest','cpuLimit','memoryRequest','memoryLimit','resourceOverrides'].includes(name)))
 return <FormTabs label="Application settings" items={[
 ...(hasConfigurableFields(parameters,values)||!replicas?[{id:'parameters',title:'Parameters',content:<InputData value={{...parameterValues,...(!replicas?{podDisruptionBudgetMinAvailable:item.definition.podDisruptionBudgetMinAvailable??(tcp?0:1)}:{})}} schema={parameters}/>}]:[]),
 ...(replicas?[{id:'replicas',title:'Replicas',content:<div className="form-stack"><InputData value={{initialReplicas:values.initialReplicas??1,podDisruptionBudgetMinAvailable:item.definition.podDisruptionBudgetMinAvailable??(tcp?0:1)}}/>{Object.keys(replicaOverrides).length>0&&<ReadoutTable label="Initial replicas by service" data={{headers:['Service','Initial replicas'],rows:Object.entries(replicaOverrides).map(([id,value])=>[id,String(value)])}}/>}</div>}]:[]),
 ...(Object.keys(resources.properties??{}).length?[{id:'resources',title:'Resources',content:<ResourceSettingsReadout value={values}/>}]:[]),
 ...(topology?[{id:'topology',title:'Topology',content:topology}]:[]),
 {id:'placement',title:'Placement',content:<PlacementReadout deploymentId={deploymentId} networking={networking} value={item.definition.applicationPlacement} application={application} session={session} values={item.definition.applicationValues}/>},
 {id:'istio',title:'Istio',content:item.definition.istioDataPlane==='ambient'?<ReadoutTable label="Istio settings" data={{headers:['Field','Value'],rows:[['Data plane','Ambient (whole namespace, one waypoint)']]}}/>:<FormTabs label="Istio settings" items={[{id:'injection',title:'Injection',content:<IstioInjectionReadout enabled={istio} overrides={item.definition.istioInjectionOverrides}/>},{id:'proxy-resources',title:'Proxy resources',content:<ResourceSettingsReadout defaults={<ProxyResourceDefaults clusterId={item.clusterResourceId} session={session} disabled={networking==='cilium'}/>} label="Proxy resources" value={item.definition.istioProxyResources}/>}]}/>}
 ]}/>
}
