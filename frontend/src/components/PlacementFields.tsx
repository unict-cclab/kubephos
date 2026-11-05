import {useState} from 'react'
import type {NodeTaint, PodToleration} from '../types'
import {FormDisclosure} from './FormDisclosure'
import {FormTabs} from './FormTabs'
import {ResourceAction} from './ResourceAction'

const namePattern = '[A-Za-z0-9]([A-Za-z0-9_.\\-]{0,61}[A-Za-z0-9])?'
const keyPattern = `([a-z0-9]([a-z0-9\\-]{0,61}[a-z0-9])?(\\.[a-z0-9]([a-z0-9\\-]{0,61}[a-z0-9])?)*/)?${namePattern}`
const managed = (key: string) => /^(kubephos\.dev|kubernetes\.io|node-role\.kubernetes\.io|topology\.kubernetes\.io)\//.test(key)
const managedTaint = (key: string) => managed(key) || /^(node\.kubernetes\.io|node\.cloudprovider\.kubernetes\.io)\//.test(key)
const effects = ['NoSchedule', 'PreferNoSchedule', 'NoExecute']

export function KeyValueFields({label, value = {}, onChange, protectManaged = false}: {label: string; value?: Record<string,string>; onChange: (value: Record<string,string>) => void; protectManaged?: boolean}) {
  const [rows, setRows] = useState(() => Object.entries(value).map(([key,value]) => ({key,value})))
  const update = (next: typeof rows) => {setRows(next); onChange(Object.fromEntries(next.filter(row => row.key).map(row => [row.key,row.value])))}
  return <div className="placement-fields" aria-label={label}>
    {rows.map((row,index) => <div className="placement-pair" key={index}>
      <label>Key<input aria-label={`${label} key ${index+1}`} value={row.key} required pattern={keyPattern} onChange={event => update(rows.map((item,i) => i===index?{...item,key:event.target.value}:item))} ref={input => {input?.setCustomValidity(rows.some((item,i) => i!==index && item.key===row.key)?'Keys must be unique.':protectManaged && managed(row.key)?'This key is managed by KubePhos or Kubernetes.':'')}} /></label>
      <label>Value<input aria-label={`${label} value ${index+1}`} value={row.value} pattern={`(${namePattern})?`} maxLength={63} onChange={event => update(rows.map((item,i) => i===index?{...item,value:event.target.value}:item))} /></label>
      <ResourceAction action="delete" label={`Remove ${label.toLowerCase()} ${index+1}`} onClick={() => update(rows.filter((_,i) => i!==index))} />
    </div>)}
    <button type="button" className="button secondary compact" onClick={() => update([...rows,{key:'',value:''}])}>Add {label.toLowerCase()}</button>
  </div>
}

export function TaintFields({value=[], onChange}: {value?: NodeTaint[]; onChange: (value: NodeTaint[]) => void}) {
  const update = (index: number, change: Partial<NodeTaint>) => onChange(value.map((item,i) => i===index?{...item,...change}:item))
  return <div className="placement-fields" aria-label="Taints">{value.map((item,index) => <div className="placement-taint" key={index}>
    <label>Key<input aria-label={`Taint key ${index+1}`} required pattern={keyPattern} value={item.key} onChange={event => update(index,{key:event.target.value})} ref={input => input?.setCustomValidity(managedTaint(item.key)?'This key is managed by KubePhos or Kubernetes.':value.some((t,i) => i!==index && t.key===item.key && t.effect===item.effect)?'This key and effect are already configured.':'')} /></label>
    <label>Value<input aria-label={`Taint value ${index+1}`} pattern={`(${namePattern})?`} maxLength={63} value={item.value??''} onChange={event => update(index,{value:event.target.value})} /></label>
    <label>Effect<select aria-label={`Taint effect ${index+1}`} value={item.effect} onChange={event => update(index,{effect:event.target.value as NodeTaint['effect']})}>{effects.map(effect => <option key={effect}>{effect}</option>)}</select></label>
    <ResourceAction action="delete" label={`Remove taint ${index+1}`} onClick={() => onChange(value.filter((_,i) => i!==index))} />
  </div>)}<button type="button" className="button secondary compact" onClick={() => onChange([...value,{key:'',effect:'NoSchedule'}])}>Add taint</button></div>
}

export function TolerationFields({value=[], onChange}: {value?: PodToleration[]; onChange: (value: PodToleration[]) => void}) {
  const update = (index: number, change: Partial<PodToleration>) => onChange(value.map((item,i) => i===index?{...item,...change}:item))
  return <div className="placement-fields" aria-label="Tolerations">{value.map((item,index) => <div className="placement-toleration" key={index}>
    <label>Key<input aria-label={`Toleration key ${index+1}`} required={item.operator!=='Exists'} pattern={`(${keyPattern})?`} value={item.key??''} onChange={event => update(index,{key:event.target.value})} /></label>
    <label>Operator<select aria-label={`Toleration operator ${index+1}`} value={item.operator??'Equal'} onChange={event => update(index,{operator:event.target.value as PodToleration['operator'],...(event.target.value==='Exists'?{value:undefined}:{})})}><option>Equal</option><option>Exists</option></select></label>
    <label>Value<input aria-label={`Toleration value ${index+1}`} pattern={`(${namePattern})?`} maxLength={63} disabled={item.operator==='Exists'} value={item.value??''} onChange={event => update(index,{value:event.target.value})} /></label>
    <label>Effect<select aria-label={`Toleration effect ${index+1}`} value={item.effect??''} onChange={event => update(index,{effect:event.target.value as PodToleration['effect'],...(event.target.value!=='NoExecute'?{tolerationSeconds:undefined}:{})})}><option value="">All</option>{effects.map(effect => <option key={effect}>{effect}</option>)}</select></label>
    {item.effect==='NoExecute' && <label>Time (s)<input aria-label={`Toleration time ${index+1}`} type="number" min={0} step={1} value={item.tolerationSeconds??''} onChange={event => update(index,{tolerationSeconds:event.target.value===''?undefined:Number(event.target.value)})} /></label>}
    <ResourceAction action="delete" label={`Remove toleration ${index+1}`} onClick={() => onChange(value.filter((_,i) => i!==index))} />
  </div>)}<button type="button" className="button secondary compact" onClick={() => onChange([...value,{key:'',operator:'Equal',effect:'NoSchedule'}])}>Add toleration</button></div>
}

export function PoolPlacementFields({labels, taints, onChange, name = 'Pool'}: {name?: string; labels?: Record<string,string>; taints?: NodeTaint[]; onChange: (change: {labels?: Record<string,string>; taints?: NodeTaint[]}) => void}) {
  return <FormDisclosure title="Labels and taints" defaultOpen={Boolean(Object.keys(labels??{}).length || taints?.length)}><input type="hidden" data-review-title={`${name} · Labels and taints`} data-review-table={JSON.stringify({headers:['Setting','Key','Value','Effect'],rows:[...Object.entries(labels??{}).map(([key,value])=>['Label',key,value,'—']),...(taints??[]).map(t=>['Taint',t.key,t.value??'',t.effect])]})} /><div data-review-skip="true"><FormTabs label="Pool labels and taints" items={[
    {id:'labels',title:'Labels',content:<KeyValueFields label="Label" value={labels} protectManaged onChange={labels => onChange({labels})} />},
    {id:'taints',title:'Taints',content:<TaintFields value={taints} onChange={taints => onChange({taints})} />}
  ]} /></div></FormDisclosure>
}
