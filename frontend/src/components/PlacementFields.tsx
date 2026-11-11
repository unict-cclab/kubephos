import {ValidatedInput} from './ValidatedInput'
import {PoolDefaultLabels, PoolDefaultTaints, type PoolDefaults} from './PlacementDefaults'
import {useState,type ReactNode} from 'react'
import type {NodeTaint, PodToleration} from '../types'
import {FormDisclosure} from './FormDisclosure'
import {FormTabs} from './FormTabs'
import {ResourceAction} from './ResourceAction'

const namePattern = '[A-Za-z0-9]([A-Za-z0-9_.\\-]{0,61}[A-Za-z0-9])?'
const keyPattern = `([a-z0-9]([a-z0-9\\-]{0,61}[a-z0-9])?(\\.[a-z0-9]([a-z0-9\\-]{0,61}[a-z0-9])?)*/)?${namePattern}`
const managed = (key: string) => /^(kubephos\.dev|kubernetes\.io|node-role\.kubernetes\.io|topology\.kubernetes\.io)\//.test(key)
const managedTaint = (key: string) => managed(key) || /^(node\.kubernetes\.io|node\.cloudprovider\.kubernetes\.io)\//.test(key)
const effects = ['NoSchedule', 'PreferNoSchedule', 'NoExecute']

export function KeyValueFields({label, value = {}, onChange, protectManaged = false,validate,empty}: {empty?:string;validate?:(key:string,value:string)=>string;label: string; value?: Record<string,string>; onChange: (value: Record<string,string>) => void; protectManaged?: boolean}) {
  const [rows, setRows] = useState(() => Object.entries(value).map(([key,value]) => ({key,value})))
  const update = (next: typeof rows) => {setRows(next); onChange(Object.fromEntries(next.filter(row => row.key).map(row => [row.key,row.value])))}
  return <div className="placement-fields" aria-label={label}>{empty&&!rows.length&&<p className="placement-empty">{empty}</p>}<div className="placement-field-actions"><button type="button" className="button secondary compact" disabled={rows.length>=64} onClick={() => update([...rows,{key:'',value:''}])}>Add {label.toLowerCase()}</button></div>
    {rows.map((row,index) => <div className="placement-pair" key={index}>
      <label>Key<ValidatedInput aria-label={`${label} key ${index+1}`} placeholder={label.toLowerCase().includes('node selector')?'kubephos.dev/pool':undefined} value={row.key} required pattern={keyPattern} onChange={event => update(rows.map((item,i) => i===index?{...item,key:event.target.value}:item))} error={rows.some((item,i) => i!==index && item.key===row.key)?'Duplicate key.':protectManaged && managed(row.key)?'This key is reserved.':validate?.(row.key,row.value)??''} /></label>
      <label>Value<ValidatedInput aria-label={`${label} value ${index+1}`} value={row.value} pattern={`(${namePattern})?`} maxLength={63} onChange={event => update(rows.map((item,i) => i===index?{...item,value:event.target.value}:item))} /></label>
      <ResourceAction action="delete" label={`Remove ${label.toLowerCase()} ${index+1}`} onClick={() => update(rows.filter((_,i) => i!==index))} />
    </div>)}

  </div>
}

export function TaintFields({value=[], onChange}: {value?: NodeTaint[]; onChange: (value: NodeTaint[]) => void}) {
  const update = (index: number, change: Partial<NodeTaint>) => onChange(value.map((item,i) => i===index?{...item,...change}:item))
  return <div className="placement-fields" aria-label="Taints"><div className="placement-field-actions"><button type="button" className="button secondary compact" disabled={value.length>=64} onClick={() => onChange([...value,{key:'',effect:'NoSchedule'}])}>Add taint</button></div>{value.map((item,index) => <div className="placement-taint" key={index}>
    <label>Key<ValidatedInput aria-label={`Taint key ${index+1}`} required pattern={keyPattern} value={item.key} onChange={event => update(index,{key:event.target.value})} error={managedTaint(item.key)?'This key is reserved.':value.some((t,i) => i!==index && t.key===item.key && t.effect===item.effect)?'Duplicate key and effect.':''} /></label>
    <label>Value<ValidatedInput aria-label={`Taint value ${index+1}`} pattern={`(${namePattern})?`} maxLength={63} value={item.value??''} onChange={event => update(index,{value:event.target.value})} /></label>
    <label>Effect<select aria-label={`Taint effect ${index+1}`} value={item.effect} onChange={event => update(index,{effect:event.target.value as NodeTaint['effect']})}>{effects.map(effect => <option key={effect}>{effect}</option>)}</select></label>
    <ResourceAction action="delete" label={`Remove taint ${index+1}`} onClick={() => onChange(value.filter((_,i) => i!==index))} />
  </div>)}</div>
}

export function TolerationFields({value=[], onChange, empty}: {empty?: string; value?: PodToleration[]; onChange: (value: PodToleration[]) => void}) {
  const update = (index: number, change: Partial<PodToleration>) => onChange(value.map((item,i) => i===index?{...item,...change}:item))
  return <div className="placement-fields" aria-label="Tolerations">{empty&&!value.length&&<p className="placement-empty">{empty}</p>}<div className="placement-field-actions"><button type="button" className="button secondary compact" disabled={value.length>=64} onClick={() => onChange([...value,{key:'',operator:'Equal',effect:'NoSchedule'}])}>Add toleration</button></div>{value.map((item,index) => <div className="placement-toleration" key={index}>
    <label>Key<ValidatedInput aria-label={`Toleration key ${index+1}`} required={item.operator!=='Exists'} error={(item.key||item.operator==='Exists')&&value.some((other,i)=>i!==index&&JSON.stringify([other.key??'',other.operator??'Equal',other.value??'',other.effect??'',other.tolerationSeconds??null])===JSON.stringify([item.key??'',item.operator??'Equal',item.value??'',item.effect??'',item.tolerationSeconds??null]))?'Duplicate toleration.':''} pattern={`(${keyPattern})?`} value={item.key??''} onChange={event => update(index,{key:event.target.value})} /></label>
    <label>Operator<select aria-label={`Toleration operator ${index+1}`} value={item.operator??'Equal'} onChange={event => update(index,{operator:event.target.value as PodToleration['operator'],...(event.target.value==='Exists'?{value:undefined}:{})})}><option>Equal</option><option>Exists</option></select></label>
    <label>Value<ValidatedInput aria-label={`Toleration value ${index+1}`} pattern={`(${namePattern})?`} maxLength={63} disabled={item.operator==='Exists'} value={item.value??''} onChange={event => update(index,{value:event.target.value})} /></label>
    <label>Effect<select aria-label={`Toleration effect ${index+1}`} value={item.effect??''} onChange={event => update(index,{effect:event.target.value as PodToleration['effect'],...(event.target.value!=='NoExecute'?{tolerationSeconds:undefined}:{})})}><option value="">All</option>{effects.map(effect => <option key={effect}>{effect}</option>)}</select></label>
    {item.effect==='NoExecute' && <label>Time (s)<ValidatedInput aria-label={`Toleration time ${index+1}`} type="number" min={0} step={1} value={item.tolerationSeconds??''} onChange={event => update(index,{tolerationSeconds:event.target.value===''?undefined:Number(event.target.value)})} /></label>}
    <ResourceAction action="delete" label={`Remove toleration ${index+1}`} onClick={() => onChange(value.filter((_,i) => i!==index))} />
  </div>)}</div>
}

export function PoolPlacementFields({labels, taints, onChange, name = 'Pool', defaults,disclosure=true,properties,actions}: {actions?:ReactNode;disclosure?:boolean;properties?:ReactNode;defaults?: PoolDefaults; name?: string; labels?: Record<string,string>; taints?: NodeTaint[]; onChange: (change: {labels?: Record<string,string>; taints?: NodeTaint[]}) => void}) {
  const content=<><input type="hidden" data-review-title={`${name} · Labels and taints`} data-review-table={JSON.stringify({headers:['Setting','Key','Value','Effect'],rows:[...Object.entries(labels??{}).map(([key,value])=>['Label',key,value,'—']),...(taints??[]).map(t=>['Taint',t.key,t.value??'',t.effect])]})} /><div data-review-skip="true"><FormTabs actions={actions} label="Pool labels and taints" items={[
    ...(properties?[{id:'properties',title:'Properties',content:properties}]:[]),
    {id:'labels',title:'Labels',content:<div className="pool-placement-layout">{defaults&&<PoolDefaultLabels pool={defaults}/>}<div><KeyValueFields label="Label" value={labels} protectManaged onChange={labels => onChange({labels})} /></div></div>},
    {id:'taints',title:'Taints',content:<div className="pool-placement-layout">{defaults&&<PoolDefaultTaints pool={defaults}/>}<div><TaintFields value={taints} onChange={taints => onChange({taints})} /></div></div>}
  ]} /></div></>
  return disclosure?<FormDisclosure title="Labels and taints" defaultOpen={Boolean(Object.keys(labels??{}).length||taints?.length)}>{content}</FormDisclosure>:content
}
