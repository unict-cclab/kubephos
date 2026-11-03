import {useState} from 'react'
import {FormDisclosure} from './FormDisclosure'
import {ResponsiveTable} from './ResponsiveTable'
import {ResourceAction} from './ResourceAction'
import type {ApplicationComponent} from '../types'

export function IstioInjectionEditor({services, enabled: initialEnabled = true, overrides: initialOverrides = {}, disabled = false}: {services: ApplicationComponent[]; enabled?: boolean; overrides?: Record<string, boolean>; disabled?: boolean}) {
 const [enabled,setEnabled]=useState(initialEnabled)
 const [overrides,setOverrides]=useState<Record<string,boolean>>({...initialOverrides})
 const next=services.find(service=>overrides[service.id]===undefined)
 return <section className="form-stack istio-injection-editor" data-review-group="Istio injection">
  <label className="checkbox-field"><input data-review-skip="true" type="checkbox" checked={!disabled&&enabled} disabled={disabled} onChange={event=>setEnabled(event.target.checked)}/><span>Enable Istio injection</span></label>
  <input type="hidden" name="istioInjection" value={String(!disabled&&enabled)}/>
  <input type="hidden" name="istioInjectionOverrides" value={JSON.stringify(disabled?{}:overrides)} data-review-title="Istio injection" data-review-table={JSON.stringify({headers:['Service','Istio injection'],rows:[['Application default',!disabled&&enabled?'Enabled':'Disabled'],...Object.entries(disabled?{}:overrides).map(([id,value])=>[id,value?'Enabled':'Disabled'])]})}/>
  {!disabled&&<FormDisclosure title="Service overrides" defaultOpen={Object.keys(overrides).length>0}>
   <div className="form-section-actions"><button type="button" className="button secondary compact" disabled={!next} onClick={()=>{if(next)setOverrides(current=>({...current,[next.id]:!enabled}))}}>Add service override</button></div>
   {Object.keys(overrides).length>0&&<ResponsiveTable aria-label="Istio service overrides"><thead><tr><th>Service</th><th>Istio injection</th><th>Actions</th></tr></thead><tbody>{Object.entries(overrides).map(([id,value])=><tr key={id}><td><select data-review-skip="true" aria-label={`Istio service ${id}`} value={id} onChange={event=>setOverrides(current=>{const copy={...current};delete copy[id];copy[event.target.value]=value;return copy})}>{!services.some(service=>service.id===id)&&<option value={id}>{id} (unavailable)</option>}{services.map(service=><option key={service.id} value={service.id} disabled={service.id!==id&&overrides[service.id]!==undefined}>{service.id}</option>)}</select></td><td><select data-review-skip="true" aria-label={`Istio injection for ${id}`} value={String(value)} onChange={event=>setOverrides(current=>({...current,[id]:event.target.value==='true'}))}><option value="true">Enabled</option><option value="false">Disabled</option></select></td><td><ResourceAction action="delete" label={`Remove Istio override for ${id}`} onClick={()=>setOverrides(current=>{const copy={...current};delete copy[id];return copy})}/></td></tr>)}</tbody></ResponsiveTable>}
  </FormDisclosure>}
 </section>
}
