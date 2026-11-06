import {useState} from 'react'
import {ServiceResourceEditor,serviceResourceFields} from './ServiceResourceEditor'
import type {ApplicationComponent} from '../types'

export function ProxyResourcesEditor({services,initial={},disabled=false}: {services:ApplicationComponent[];initial?:Record<string,unknown>;disabled?:boolean}) {
 const [values,setValues]=useState(initial)
 const allowed=new Set(services.map(service=>service.id))
 const overrides=Object.fromEntries(Object.entries((values.resourceOverrides??{}) as Record<string,unknown>).filter(([id])=>allowed.has(id)))
 const resources={...Object.fromEntries(serviceResourceFields.flatMap(field=>typeof values[field]==='string'&&values[field]!==''?[[field,values[field]]]:[])),...(Object.keys(overrides).length?{resourceOverrides:overrides}:{})}
 return <section data-review-group="Proxy resources"><input type="hidden" name="istioProxyResources" value={JSON.stringify(!disabled&&Object.keys(resources).length?resources:null)} data-review-skip="true"/><fieldset disabled={disabled} className="proxy-resource-fields"><ServiceResourceEditor title="Proxy resources" scope="proxy" prefix="istioProxyResourceFields" targets={services} values={values} onValue={changes=>setValues(current=>({...current,...changes}))}/></fieldset></section>
}
