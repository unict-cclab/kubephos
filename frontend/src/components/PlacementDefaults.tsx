import {ValidationMessage} from './ValidationMessage'
import {ReadoutTable} from './ReadoutTable'
import type {NodeTaint, PodToleration} from '../types'

export type PoolDefaults = {clusterName?: string; name: string; role?: 'application'|'management'|'control-plane'; zones?: string[]; count?: number; networking?: string}
export type WorkloadPlacementDefaults = {id: string; nodeSelector?: Record<string,string>; tolerations?: PodToleration[]; nodeBinding?: string}

export function defaultPoolLabels(pool: PoolDefaults): Array<[string,string]> {
 const count=Math.max(0,Math.min(12,pool.count??1))
 const rows:Array<[string,string]>=[['kubephos.dev/pool',pool.name||'—'],['kubephos.dev/role',pool.role??'application']]
 if(pool.networking==='cilium') rows.push(['kubephos.dev/cluster',pool.clusterName||'—'])
 else for(const zone of new Set((pool.zones??[]).filter(Boolean).slice(0,count))) rows.push(['topology.kubernetes.io/zone',zone])
 return rows
}
export function defaultPoolTaints(pool: PoolDefaults): NodeTaint[] {return pool.role==='control-plane'?[{key:'node-role.kubernetes.io/control-plane',effect:'NoSchedule'}]:[]}

export function PoolDefaultLabels({pool}: {pool: PoolDefaults}) {
 return <div className="placement-defaults"><h4>Defaults</h4><ReadoutTable label="Default labels" data={{headers:['Key','Value'],rows:defaultPoolLabels(pool)}} /></div>
}
export function PoolDefaultTaints({pool}: {pool: PoolDefaults}) {
 const taints=defaultPoolTaints(pool)
 return <div className="placement-defaults"><h4>Defaults</h4>{taints.length?<ReadoutTable label="Default taints" data={{headers:['Key','Value','Effect'],rows:taints.map(item=>[item.key,item.value||'—',item.effect])}}/>:<p className="details-copy">None</p>}</div>
}

export function ApplicationDefaultFields({defaults, kind, pending, error, heading='Defaults'}: {heading?:string; defaults:WorkloadPlacementDefaults[];kind:'nodeSelector'|'tolerations';pending?:boolean;error?:string}) {
 if(pending)return <p className="details-copy" role="status">Loading defaults…</p>
 if(error)return <ValidationMessage message={error}/>
 const groups=new Map<string,{values:string[];services:string[]}>()
 for(const service of defaults) {
  const rows=kind==='nodeSelector'?Object.entries(service.nodeSelector??{}): (service.tolerations??[]).map(t=>[t.key||'All',t.operator||'Equal',t.value||'—',t.effect||'All',t.effect!=='NoExecute'?'—':t.tolerationSeconds===undefined?'Unlimited':String(t.tolerationSeconds)])
  for(const row of rows){const key=JSON.stringify(row);const group=groups.get(key)??{values:row,services:[]};group.services.push(service.id);groups.set(key,group)}
 }
 const title=kind==='nodeSelector'?'Default node selector':'Default tolerations'
 const rows=[...groups.values()].map(group=>[...group.values,group.services.length===defaults.length?'All workloads':group.services.join(', ')])
 return <div className="placement-defaults"><h4>{heading}</h4>{rows.length?<ReadoutTable label={title} data={{headers:kind==='nodeSelector'?['Key','Value','Applies to']:['Key','Operator','Value','Effect','Time (s)','Applies to'],rows}}/>:<p className="details-copy">None</p>}
 {kind==='nodeSelector'&&defaults.some(value=>value.nodeBinding==='application-node')&&<ReadoutTable label="Node binding" data={{headers:['Service','Node binding'],rows:defaults.filter(value=>value.nodeBinding==='application-node').map(value=>[value.id,'Per application node'])}} />}</div>
}
