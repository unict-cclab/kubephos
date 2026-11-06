import type {ReactNode} from 'react'
import {FormDisclosure} from './FormDisclosure'
import {FormTabs} from './FormTabs'
import {ReadoutTable} from './ReadoutTable'
import {PoolDefaultLabels, PoolDefaultTaints, type PoolDefaults} from './PlacementDefaults'
import type {NodeTaint} from '../types'

export function PoolPlacementReadout({pool, labels={}, taints=[],disclosure=true,properties}: {pool:PoolDefaults;labels?:Record<string,string>;taints?:NodeTaint[];disclosure?:boolean;properties?:ReactNode}) {
 const content=<FormTabs label={`${pool.name} labels and taints`} items={[
  ...(properties?[{id:'properties',title:'Properties',content:properties}]:[]),
  {id:'labels',title:'Labels',content:<div className="pool-placement-layout"><PoolDefaultLabels pool={pool}/>{Object.keys(labels).length>0&&<ReadoutTable label="Custom labels" data={{headers:['Key','Value'],rows:Object.entries(labels)}}/>}</div>},
  {id:'taints',title:'Taints',content:<div className="pool-placement-layout"><PoolDefaultTaints pool={pool}/>{taints.length>0&&<ReadoutTable label="Custom taints" data={{headers:['Key','Value','Effect'],rows:taints.map(t=>[t.key,t.value||'—',t.effect])}}/>}</div>}
 ]}/>
 return disclosure?<FormDisclosure title="Labels and taints">{content}</FormDisclosure>:content
}
