import type {WorkloadPlacementDefaults} from './PlacementDefaults'
import {ApplicationDefaultFields} from './PlacementDefaults'
import {useApplicationPlacementDefaults} from './useApplicationPlacementDefaults'
import {useState} from 'react'
import type {Application, Session, ApplicationComponent, ApplicationPlacement, ServicePlacementOverride} from '../types'
import {KeyValueFields, TolerationFields} from './PlacementFields'
import {FormTabs} from './FormTabs'
import {ResourceAction} from './ResourceAction'
import {ReadoutTable} from './ReadoutTable'

type Mode = 'inherit' | 'custom' | 'none'
const modeNames:Record<Mode,string>={inherit:'Common',custom:'Override',none:'Defaults only'}
export function ApplicationPlacementEditor({services, initial, application, session, values, enabled, networking,defaults}: {defaults?:WorkloadPlacementDefaults[];services: ApplicationComponent[]; initial?: ApplicationPlacement;application?:Application;session?:Session;values?:Record<string,unknown>;enabled?:boolean;networking?:string}) {
  const [selectedService,setSelectedService]=useState<string>()
  const loaded=useApplicationPlacementDefaults({application,session,values,networking,enabled:defaults===undefined&&enabled!==false,services})
  const baseline={...loaded,...(defaults?{defaults:defaults.filter(value=>networking!=='cilium'||value.nodeBinding!=='application-node'),pending:false,error:''}:{})}
  const [settings,setSettings]=useState<ApplicationPlacement>(() => structuredClone(initial??{}))
  const drafts=Object.fromEntries(Object.entries(settings.serviceOverrides??{}).filter(([id]) => services.some(service => service.id===id)))
  const overrides=Object.fromEntries(Object.entries(drafts).map(([id,entry])=>[id,{nodeSelectorMode:entry.nodeSelectorMode,tolerationsMode:entry.tolerationsMode,...(entry.nodeSelectorMode==='custom'?{nodeSelector:entry.nodeSelector}:{}),...(entry.tolerationsMode==='custom'?{tolerations:entry.tolerations}:{})}]))
  const next=services.find(service => !Object.hasOwn(overrides,service.id))
  const change=(id:string,values:Partial<ServicePlacementOverride>) => setSettings(current => ({...current,serviceOverrides:{...current.serviceOverrides,[id]:{...current.serviceOverrides?.[id],...values}}}))
  const mode=(id:string,kind:'nodeSelector'|'tolerations',value:Mode) => {
    const current=drafts[id]
    change(id,{[`${kind}Mode`]:value,...(value==='custom'&&current?.[kind]===undefined?{[kind]:structuredClone(settings[kind]??(kind==='nodeSelector'?{}:[]))}:{})})
  }
  const selectorError=(service?:string)=>(key:string,value:string)=>{
    if(!key)return ''
    const candidates=baseline.defaults.filter(item=>service?item.id===service:!['custom','none'].includes(overrides[item.id]?.nodeSelectorMode??'inherit'))
    const conflict=candidates.find(item=>item.nodeSelector&&Object.hasOwn(item.nodeSelector,key)&&item.nodeSelector[key]!==value)
    return conflict?`${conflict.id} requires ${key}=${conflict.nodeSelector![key]}.`:''
  }
  const hasSettings=Object.keys(settings.nodeSelector??{}).length || settings.tolerations?.length || Object.keys(overrides).length
  const selectMode=(id:string,kind:'nodeSelector'|'tolerations',value?:Mode)=><select aria-label={`${kind==='nodeSelector'?'Node selector':'Tolerations'} mode for ${id}`} value={value??'inherit'} onChange={event=>mode(id,kind,event.target.value as Mode)}>{(Object.keys(modeNames) as Mode[]).map(mode=><option value={mode} key={mode}>{modeNames[mode]}</option>)}</select>
  return <section ref={baseline.ref} className="form-stack" data-review-group="Placement">
    <input type="hidden" name="applicationPlacement" value={JSON.stringify(hasSettings?{...settings,serviceOverrides:overrides}:null)} data-review-title="Placement" data-review-table={JSON.stringify({headers:['Scope','Field','Value'],rows:[...Object.entries(settings.nodeSelector??{}).map(([key,value])=>['Common',`Node selector · ${key}`,value]),...(settings.tolerations??[]).map(t=>['Common','Toleration',`${t.key||'All'} · ${t.operator||'Equal'}${t.value?` · ${t.value}`:''} · ${t.effect||'All'}${t.tolerationSeconds===undefined?'':` · ${t.tolerationSeconds} s`}`]),...Object.entries(overrides).flatMap(([id,o])=>[[id,'Node selector',o.nodeSelectorMode==='custom'?`${modeNames.custom} · ${Object.entries(o.nodeSelector??{}).map(([key,value])=>`${key}=${value}`).join(', ')||'—'}`:modeNames[o.nodeSelectorMode??'inherit']],[id,'Tolerations',o.tolerationsMode==='custom'?`${modeNames.custom} · ${(o.tolerations??[]).map(t=>`${t.key||'All'} ${t.operator||'Equal'} ${t.value||''} ${t.effect||'All'}${t.tolerationSeconds===undefined?'':` ${t.tolerationSeconds}s`}`).join(', ')||'—'}`:modeNames[o.tolerationsMode??'inherit']]])]})} />
    <div data-review-skip="true" className="form-stack">
      <FormTabs label="Placement settings" items={[
       {id:'common',title:'Common settings',content:<><section className="form-stack"><div className="placement-common-grid placement-sections">
        <section className="placement-field-group" aria-label="Common node selector"><h4>Node selector</h4><KeyValueFields validate={selectorError()} label="Node selector" value={settings.nodeSelector} onChange={nodeSelector => setSettings(current => ({...current,nodeSelector}))}/></section>
        <section className="placement-field-group" aria-label="Common tolerations"><h4>Tolerations</h4><TolerationFields value={settings.tolerations} onChange={tolerations => setSettings(current => ({...current,tolerations}))}/></section>
      </div></section></>},
       {id:'services',title:'Service overrides',content:<FormTabs label="Service placement overrides" selectedId={selectedService} onSelect={setSelectedService} actions={<button type="button" className="button secondary compact" disabled={!next} onClick={()=>{if(next){change(next.id,{nodeSelectorMode:'inherit',tolerationsMode:'inherit'});setSelectedService(next.id)}}}>Add service override</button>} items={Object.entries(drafts).map(([id,override])=>({id,title:id,content:<section className="form-stack" aria-label={`Placement for ${id}`}>
        <div className="placement-service-heading"><label>Service<select aria-label={`Placement service ${id}`} value={id} onChange={event=>{const target=event.target.value;setSettings(current=>{const copy={...current.serviceOverrides};delete copy[id];copy[target]=override;return {...current,serviceOverrides:copy}});setSelectedService(target)}}>{services.map(service=><option value={service.id} key={service.id} disabled={service.id!==id&&Object.hasOwn(drafts,service.id)}>{service.id}</option>)}</select></label><ResourceAction action="delete" label={`Remove placement override for ${id}`} onClick={()=>setSettings(current=>{const copy={...current.serviceOverrides};delete copy[id];return {...current,serviceOverrides:copy}})}/></div>
        <div className="placement-common-grid placement-sections">
          <section className="placement-field-group"><label>Node selector{selectMode(id,'nodeSelector',override.nodeSelectorMode)}</label>{override.nodeSelectorMode==='custom'&&<KeyValueFields validate={selectorError(id)} label={`${id} node selector`} value={override.nodeSelector} onChange={nodeSelector=>change(id,{nodeSelector})}/>}</section>
          <section className="placement-field-group"><label>Tolerations{selectMode(id,'tolerations',override.tolerationsMode)}</label>{override.tolerationsMode==='custom'&&<TolerationFields value={override.tolerations} onChange={tolerations=>change(id,{tolerations})}/>}</section>
        </div>
       </section>}))}/>},
       {id:'defaults',title:'Defaults',content:<><div className="placement-common-grid placement-sections"><ApplicationDefaultFields {...baseline} kind="nodeSelector" heading="Node selector"/><ApplicationDefaultFields {...baseline} kind="tolerations" heading="Tolerations"/></div></>}
      ]}/>
    </div>
  </section>
}

export function PlacementReadout({value={},application,session,values,networking,deploymentId}: {deploymentId?:string;value?:ApplicationPlacement;application?:Application;session?:Session;values?:Record<string,unknown>;networking?:string}) {
  const baseline=useApplicationPlacementDefaults({application,session,values,networking,deploymentId,services:application?.descriptor.spec?.interface?.components??[]})
  const selectors=(selector?:Record<string,string>) => <div className="placement-defaults"><h4>Node selector</h4>{Object.keys(selector??{}).length?<ReadoutTable label="Node selector" data={{headers:['Key','Value'],rows:Object.entries(selector!)}}/>:<p className="details-copy">None</p>}</div>
  const tolerations=(items:ApplicationPlacement['tolerations']) => <div className="placement-defaults"><h4>Tolerations</h4>{items?.length?<ReadoutTable label="Tolerations" data={{headers:['Key','Operator','Value','Effect','Time (s)'],rows:items.map(t => [t.key||'All',t.operator||'Equal',t.value||'—',t.effect||'All',t.effect!=='NoExecute'?'—':t.tolerationSeconds===undefined?'Unlimited':String(t.tolerationSeconds)])}}/>:<p className="details-copy">None</p>}</div>
  return <section ref={baseline.ref}><FormTabs label="Placement settings" items={[
   {id:'common',title:'Common settings',content:<div className="placement-common-grid placement-sections">{selectors(value.nodeSelector)}{tolerations(value.tolerations)}</div>},
   {id:'services',title:'Service overrides',content:Object.keys(value.serviceOverrides??{}).length?<FormTabs label="Service placement overrides" items={Object.entries(value.serviceOverrides??{}).map(([id,override])=>({id,title:id,content:<div className="form-stack"><ReadoutTable label={`${id} placement`} data={{headers:['Field','Value'],rows:[['Node selector',modeNames[override.nodeSelectorMode??'inherit']],['Tolerations',modeNames[override.tolerationsMode??'inherit']]]}}/><div className="placement-common-grid placement-sections">{override.nodeSelectorMode==='custom'&&selectors(override.nodeSelector)}{override.tolerationsMode==='custom'&&tolerations(override.tolerations)}</div></div>}))}/>:<p className="details-copy">None</p>},
   {id:'defaults',title:'Defaults',content:<div className="placement-common-grid placement-sections"><ApplicationDefaultFields {...baseline} kind="nodeSelector" heading="Node selector"/><ApplicationDefaultFields {...baseline} kind="tolerations" heading="Tolerations"/></div>}
  ]}/></section>
}
