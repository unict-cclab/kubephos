import {useState} from 'react'
import type {ApplicationComponent, ApplicationPlacement, ServicePlacementOverride} from '../types'
import {KeyValueFields, TolerationFields} from './PlacementFields'
import {FormTabs} from './FormTabs'
import {FormDisclosure} from './FormDisclosure'
import {ResourceAction} from './ResourceAction'
import {ReadoutTable} from './ReadoutTable'

type Mode = 'inherit' | 'custom' | 'none'
export function ApplicationPlacementEditor({services, initial}: {services: ApplicationComponent[]; initial?: ApplicationPlacement}) {
  const [settings,setSettings]=useState<ApplicationPlacement>(() => structuredClone(initial??{}))
  const overrides=Object.fromEntries(Object.entries(settings.serviceOverrides??{}).filter(([id]) => services.some(service => service.id===id)))
  const next=services.find(service => !Object.hasOwn(overrides,service.id))
  const change=(id:string,values:Partial<ServicePlacementOverride>) => setSettings(current => ({...current,serviceOverrides:{...current.serviceOverrides,[id]:{...current.serviceOverrides?.[id],...values}}}))
  const mode=(id:string,kind:'nodeSelector'|'tolerations',value:Mode) => change(id,{[`${kind}Mode`]:value,[kind]:value==='custom'?structuredClone(settings[kind]??(kind==='nodeSelector'?{}:[])):undefined})
  const effective={...settings,serviceOverrides:overrides}
  const hasSettings=Object.keys(settings.nodeSelector??{}).length || settings.tolerations?.length || Object.keys(overrides).length
  return <section className="form-stack" data-review-group="Placement">
    <input type="hidden" name="applicationPlacement" value={JSON.stringify(hasSettings?effective:null)} data-review-title="Placement" data-review-table={JSON.stringify({headers:['Scope','Field','Value'],rows:[...Object.entries(settings.nodeSelector??{}).map(([key,value])=>['Application',`Node selector · ${key}`,value]),...(settings.tolerations??[]).map(t=>['Application','Toleration',`${t.key||'All'} · ${t.operator||'Equal'}${t.value?` · ${t.value}`:''} · ${t.effect||'All'}${t.tolerationSeconds===undefined?'':` · ${t.tolerationSeconds} s`}`]),...Object.entries(overrides).flatMap(([id,o])=>[[id,'Node selector',o.nodeSelectorMode==='custom'?Object.entries(o.nodeSelector??{}).map(([key,value])=>`${key}=${value}`).join(', ')||'None':o.nodeSelectorMode??'inherit'],[id,'Tolerations',o.tolerationsMode==='custom'?(o.tolerations??[]).map(t=>`${t.key||'All'} · ${t.operator||'Equal'} · ${t.effect||'All'}`).join(', ')||'None':o.tolerationsMode??'inherit']])]})} />
    <div data-review-skip="true"><FormTabs label="Application placement" items={[
      {id:'selector',title:'Node selector',content:<KeyValueFields label="Node selector" value={settings.nodeSelector} onChange={nodeSelector => setSettings(current => ({...current,nodeSelector}))} />},
      {id:'tolerations',title:'Tolerations',content:<TolerationFields value={settings.tolerations} onChange={tolerations => setSettings(current => ({...current,tolerations}))} />}
    ]} />
    <FormDisclosure title="Service overrides" defaultOpen={Boolean(Object.keys(overrides).length)}>
      <div className="form-section-actions"><button type="button" className="button secondary compact" disabled={!next} onClick={() => {if(next)change(next.id,{nodeSelectorMode:'inherit',tolerationsMode:'inherit'})}}>Add service override</button></div>
      {Object.entries(overrides).map(([id,override]) => <section className="placement-service form-stack" key={id} aria-label={`Placement for ${id}`}>
        <div className="placement-service-heading"><label>Service<select aria-label={`Placement service ${id}`} value={id} onChange={event => setSettings(current => {const copy={...current.serviceOverrides};delete copy[id];copy[event.target.value]=override;return {...current,serviceOverrides:copy}})}>{services.map(service => <option key={service.id} value={service.id} disabled={service.id!==id && Object.hasOwn(overrides,service.id)}>{service.id}</option>)}</select></label><ResourceAction action="delete" label={`Remove placement override for ${id}`} onClick={() => setSettings(current => {const copy={...current.serviceOverrides};delete copy[id];return {...current,serviceOverrides:copy}})} /></div>
        <div className="placement-override-grid"><div className="form-stack"><label>Node selector<select aria-label={`Node selector mode for ${id}`} value={override.nodeSelectorMode??'inherit'} onChange={event => mode(id,'nodeSelector',event.target.value as Mode)}><option value="inherit">Inherit</option><option value="custom">Custom</option><option value="none">None</option></select></label>{override.nodeSelectorMode==='custom' && <KeyValueFields key={`${id}-selector`} label={`${id} node selector`} value={override.nodeSelector} onChange={nodeSelector => change(id,{nodeSelector})} />}</div>
        <div className="form-stack"><label>Tolerations<select aria-label={`Tolerations mode for ${id}`} value={override.tolerationsMode??'inherit'} onChange={event => mode(id,'tolerations',event.target.value as Mode)}><option value="inherit">Inherit</option><option value="custom">Custom</option><option value="none">None</option></select></label>{override.tolerationsMode==='custom' && <TolerationFields value={override.tolerations} onChange={tolerations => change(id,{tolerations})} />}</div></div>
      </section>)}
    </FormDisclosure></div>
  </section>
}

export function PlacementReadout({value}: {value?:ApplicationPlacement}) {
  if (!value || !(Object.keys(value.nodeSelector??{}).length || value.tolerations?.length || Object.keys(value.serviceOverrides??{}).length)) return null
  const selectors=(selector?:Record<string,string>) => Object.keys(selector??{}).length ? <ReadoutTable label="Node selector" data={{headers:['Key','Value'],rows:Object.entries(selector!)}} /> : null
  const tolerations=(items:ApplicationPlacement['tolerations']) => items?.length ? <ReadoutTable label="Tolerations" data={{headers:['Key','Operator','Value','Effect','Time (s)'],rows:items.map(t => [t.key||'All',t.operator||'Equal',t.value||'—',t.effect||'All',t.effect!=='NoExecute'?'—':t.tolerationSeconds===undefined?'Unlimited':String(t.tolerationSeconds)])}} /> : null
  return <FormDisclosure title="Placement" defaultOpen><div className="form-stack">{selectors(value.nodeSelector)}{tolerations(value.tolerations)}{Object.entries(value.serviceOverrides??{}).map(([id,override]) => <section key={id} className="form-stack"><h4>{id}</h4><ReadoutTable label={`${id} placement`} data={{headers:['Field','Value'],rows:[['Node selector',override.nodeSelectorMode??'inherit'],['Tolerations',override.tolerationsMode??'inherit']]}} />{override.nodeSelectorMode==='custom'&&selectors(override.nodeSelector)}{override.tolerationsMode==='custom'&&tolerations(override.tolerations)}</section>)}</div></FormDisclosure>
}
