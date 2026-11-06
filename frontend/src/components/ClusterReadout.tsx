import type {ReactNode} from 'react'
import {InputData} from './InputData'
import {PoolSections} from './PoolSections'
import {PoolPlacementReadout} from './PoolPlacementReadout'
import {ReadoutTable} from './ReadoutTable'
import {WizardReadout} from './WizardReadout'
import {FormTabs} from './FormTabs'
import type {NodeTaint} from '../types'
type Spec=Record<string,unknown>
const record=(value:unknown):Spec=>value&&typeof value==='object'&&!Array.isArray(value)?value as Spec:{}
export function ClusterGeneralReadout({spec,references={}}:{spec:Spec;references?:Record<string,string>}) {
 const grouped=new Set(['machineNodes','controlPlaneZones','controlPlaneCapacity','controlPlanes','managementPool','applicationPools','scrapeInterval','retention','members','sharedSSHKey','managedProfile'])
 const networkKeys=['baseVMID','addressStart','addressPrefix','prefixLength','gateway','dnsServer','networking']
 const fields=Object.fromEntries(Object.entries(spec).filter(([key])=>!grouped.has(key)))
 const generalKeys=['name','connectionId','templateId','harborId','nfsId',...Object.keys(fields).filter(key=>!['name','connectionId','templateId','harborId','nfsId',...networkKeys].includes(key))]
 const general={...Object.fromEntries(generalKeys.filter(key=>key in fields).map(key=>[key,fields[key]])),scrapeInterval:spec.scrapeInterval,retention:spec.retention}
 const network=Object.fromEntries(networkKeys.filter(key=>key in fields).map(key=>[key,fields[key]]))
 return <div className="readout-columns"><InputData value={general} references={references}/><InputData value={network} references={references}/></div>
}
export function PoolPropertiesReadout({value,spec,references={}}:{value:Spec;spec:Spec;references?:Record<string,string>}) {
 const {capacity,...fields}=value
 const keys=['name','count','zones',...Object.keys(fields).filter(key=>!['name','count','zones'].includes(key))]
 const properties=Object.fromEntries(keys.filter(key=>key in fields&&!['labels','taints'].includes(key)&&!(spec.networking==='cilium'&&key==='zones')).map(key=>[key==='count'?'nodes':key,fields[key]]))
 const resources=record(capacity)
 const resourceKeys=['cores','memoryMiB','diskGiB',...Object.keys(resources).filter(key=>!['cores','memoryMiB','diskGiB'].includes(key))]
 return <div className="readout-columns"><InputData value={properties} references={references}/><InputData value={Object.fromEntries(resourceKeys.filter(key=>key in resources).map(key=>[key,resources[key]]))} references={references}/></div>
}
export function ClusterPoolsReadout({spec,references={},renderPool,actions}:{spec:Spec;references?:Record<string,string>;renderPool?:(pool:Spec,index:number)=>ReactNode;actions?:ReactNode}) {
 const pools=Array.isArray(spec.applicationPools)?spec.applicationPools.map(record):[]
 const management=record(spec.managementPool)
 const pool=(value:Spec,role:'application'|'control-plane'|'management')=><PoolPlacementReadout disclosure={false} properties={<PoolPropertiesReadout value={value} spec={spec} references={references}/>} pool={{clusterName:String(spec.name??''),name:String(value.name??''),role,count:Number(value.count??1),zones:value.zones as string[]|undefined,networking:String(spec.networking??'')}} labels={value.labels as Record<string,string>|undefined} taints={value.taints as NodeTaint[]|undefined}/>

 return <PoolSections actions={actions} master={pool({name:'control-plane',count:spec.controlPlanes,zones:spec.controlPlaneZones,capacity:spec.controlPlaneCapacity},'control-plane')} management={pool(management,'management')} pools={pools.map((value,index)=>({id:String(index),title:String(value.name??`Pool ${index+1}`),content:renderPool?renderPool(value,index):pool(value,'application')}))}/>
}
export function ClusterVMReadout({spec}:{spec:Spec}) {
 return <ReadoutTable label="VM placement" data={{headers:['VM name','Proxmox server'],rows:Object.entries(record(spec.machineNodes)).sort(([a],[b])=>a.localeCompare(b,undefined,{numeric:true})).map(([name,node])=>[name,String(node)])}}/>
}
export function ClusterConfigurationReadout({spec,references}:{spec:Spec;references:Record<string,string>}) {
 const members:Spec[]=spec.mode==='multi'&&Array.isArray(spec.members)?spec.members.map(value=>({...record(value),networking:'cilium'})):[]
 const memberTabs=(label:string,render:(member:Spec)=>ReactNode)=>members.length?<FormTabs label={label} items={members.map((member,index)=>({id:String(index),title:String(member.name),content:render(member)}))}/>:render(spec)
 return <WizardReadout label="Cluster configuration sections" sections={[
 {id:'general',title:'General',content:memberTabs('Cluster settings',member=><ClusterGeneralReadout spec={member} references={references}/>)},
 {id:'pools',title:'Node pools',content:memberTabs('Cluster pools',member=><ClusterPoolsReadout spec={member} references={references}/>)},
 {id:'placement',title:'VM placement',content:memberTabs('Cluster VM placement',member=><ClusterVMReadout spec={member}/>)}
 ]}/>
}
