import {useState} from 'react'
import {FormTabs} from './FormTabs'
import {ResourceAction} from './ResourceAction'
import {ReadoutTable} from './ReadoutTable'
import {ServiceOverridePicker} from './ServiceOverridePicker'
import type {ApplicationComponent} from '../types'

export function IstioInjectionEditor({services, enabled: initialEnabled = true, overrides: initialOverrides = {}, disabled = false}: {services: ApplicationComponent[]; enabled?: boolean; overrides?: Record<string, boolean>; disabled?: boolean}) {
 const [enabled,setEnabled]=useState(initialEnabled)
 const [overrides,setOverrides]=useState<Record<string,boolean>>({...initialOverrides})
 const [selected,setSelected]=useState<string>()
 return <section className="istio-injection-editor" data-review-group="Istio injection">
  <input type="hidden" name="istioInjection" value={String(!disabled&&enabled)}/>
  <input type="hidden" name="istioInjectionOverrides" value={JSON.stringify(disabled?{}:overrides)} data-review-title="Istio injection" data-review-table={JSON.stringify({headers:['Service','Istio injection'],rows:[['Application default',!disabled&&enabled?'Enabled':'Disabled'],...Object.entries(disabled?{}:overrides).map(([id,value])=>[id,value?'Enabled':'Disabled'])]})}/>
  <div>
   <label className="checkbox-field"><input data-review-skip="true" type="checkbox" checked={!disabled&&enabled} disabled={disabled} onChange={event=>setEnabled(event.target.checked)}/><span>Enable Istio injection</span></label>
   {!disabled&&<p className="istio-hint">Turn this off to inject sidecars only into some services, then add them under Service overrides.</p>}
  </div>
  {!disabled&&<div className="istio-overrides">
   <h5 className="istio-overrides-title">Service overrides</h5>
   <FormTabs label="Istio service overrides" selectedId={selected} onSelect={setSelected} actions={<ServiceOverridePicker services={services} taken={id=>overrides[id]!==undefined} onAdd={id=>{setOverrides(current=>({...current,[id]:!enabled}));setSelected(id)}}/>} items={Object.entries(overrides).map(([id,value])=>({id,title:id,content:<div className="istio-service-heading"><label>Service<select data-review-skip="true" aria-label={`Istio service ${id}`} value={id} onChange={event=>{const target=event.target.value;setOverrides(current=>{const copy={...current};delete copy[id];copy[target]=value;return copy});setSelected(target)}}>{!services.some(service=>service.id===id)&&<option value={id}>{id} (unavailable)</option>}{services.map(service=><option key={service.id} value={service.id} disabled={service.id!==id&&overrides[service.id]!==undefined}>{service.id}</option>)}</select></label><label>Istio injection<select data-review-skip="true" aria-label={`Istio injection for ${id}`} value={String(value)} onChange={event=>setOverrides(current=>({...current,[id]:event.target.value==='true'}))}><option value="true">Enabled</option><option value="false">Disabled</option></select></label><ResourceAction action="delete" label={`Remove Istio override for ${id}`} onClick={()=>setOverrides(current=>{const copy={...current};delete copy[id];return copy})}/></div>}))}/>
  </div>}
 </section>
}

export function IstioInjectionReadout({enabled,overrides={}}:{enabled:boolean;overrides?:Record<string,boolean>}) {
 return <FormTabs label="Istio injection settings" items={[
  {id:'common',title:'Common settings',content:<ReadoutTable label="Istio injection" data={{headers:['Field','Value'],rows:[['Enable Istio injection',enabled?'Enabled':'Disabled']]}}/>},
  {id:'services',title:'Service overrides',content:Object.keys(overrides).length?<FormTabs label="Istio service overrides" items={Object.entries(overrides).map(([id,value])=>({id,title:id,content:<ReadoutTable label={`Istio injection for ${id}`} data={{headers:['Field','Value'],rows:[['Istio injection',value?'Enabled':'Disabled']]}}/>}))}/>:<ReadoutTable label="Istio service overrides" data={{headers:['Field','Value'],rows:[['Service overrides','None']]}}/>}
 ]}/>
}
