import {ValidatedInput} from './ValidatedInput'
import {resourceFieldError} from '../resourceValidation'
import {useState} from 'react'
import {FormTabs} from './FormTabs'
import {ResourceAction} from './ResourceAction'
import type {ApplicationComponent} from '../types'

export const serviceResourceFields = ['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit'] as const
const rows = [{name: 'CPU', request: 'cpuRequest', limit: 'cpuLimit'}, {name: 'Memory', request: 'memoryRequest', limit: 'memoryLimit'}] as const

export function ServiceResourceEditor({prefix,values,targets,onValue,scope='service',title='Service resources'}: {prefix:string;values:Record<string,unknown>;targets?:ApplicationComponent[];onValue:(changes:Record<string,unknown>)=>void;scope?:'service'|'proxy';title?:string}) {
 const services=targets?.filter(target=>scope==='proxy'||target.traits?.some(trait=>trait==='scalable'||trait==='schedulable'))
 const overrides=record(values.resourceOverrides)
 const configured=services?Object.fromEntries(services.filter(service=>Object.hasOwn(overrides,service.id)).map(service=>[service.id,overrides[service.id]])):overrides
 const [selected,setSelected]=useState('')
 const names=Object.keys(configured)
 const active=names.includes(selected)?selected:names[0]
 const next=services?.find(service=>!Object.hasOwn(configured,service.id))
 const override=(name:string,changes:Record<string,unknown>|undefined)=>{const copy={...overrides};if(changes===undefined)delete copy[name];else copy[name]=changes;onValue({resourceOverrides:copy})}
 const addAction=<button type="button" className="button secondary compact" disabled={!next} onClick={()=>{if(next){override(next.id,{});setSelected(next.id)}}}>{scope==='proxy'?'Add proxy override':'Add service override'}</button>
 const common=<div className="service-resource-grid"><span/><strong>Request</strong><strong>Limit</strong>{rows.map(row=><ResourceRow key={row.name} row={row} prefix={prefix} values={values} onValue={onValue} scope={scope}/>)}</div>
 const serviceTabs=<FormTabs actions={addAction} label={scope==='proxy'?'Proxy resource overrides':'Service resource overrides'} selectedId={active} onSelect={setSelected} items={names.map(service=>({id:service,title:service,content:<section data-review-group={service} className="form-stack"><div className="form-section-actions"><ResourceAction action="delete" label={`Remove ${scope==='proxy'?'proxy':'resource'} override for ${service}`} onClick={()=>override(service,undefined)}/></div><div className="service-resource-grid"><span/><strong>Request</strong><strong>Limit</strong>{rows.map(row=><ResourceRow key={row.name} row={row} service={service} prefix={prefix} values={record(configured[service])} inherited={values} scope={scope} onValue={changes=>{const next={...record(configured[service])};for(const[field,value]of Object.entries(changes)){if(value==='')delete next[field];else next[field]=value};override(service,next)}}/>)}</div></section>}))}/>
 return <section className={`service-resources wide${scope==='proxy'?' proxy-resources':''}`}>
  {services?<><input type="hidden" name={`${prefix}.resourceOverrides`} value={JSON.stringify(configured)} readOnly data-review-title={`${scope==='proxy'?'Proxy resources':'Resources'} · Service overrides`} data-review-table={JSON.stringify({headers:['Service','CPU request','CPU limit','Memory request','Memory limit'],rows:Object.entries(configured).map(([id,entry])=>[id,...serviceResourceFields.map(field=>String(record(entry)[field]??values[field]??'—'))])})}/><FormTabs label={scope==='proxy'?'Proxy resource settings':'Service resource settings'} items={[{id:'common',title:'Common settings',content:common},{id:'services',title:'Service overrides',content:<div data-review-skip="true">{serviceTabs}</div>}]}/></>:<><strong>{title}</strong>{common}</>}
 </section>
}

function ResourceRow({row,prefix,values,service,inherited,onValue,scope}: {row:typeof rows[number];prefix:string;values:Record<string,unknown>;service?:string;inherited?:Record<string,unknown>;onValue:(changes:Record<string,unknown>)=>void;scope:'service'|'proxy'}) {
 return <><strong>{row.name}</strong>{[row.request,row.limit].map((field,index)=><label className="service-resource-input" key={field}><ValidatedInput error={resourceFieldError(field,values,inherited)} name={service?undefined:`${prefix}.${field}`} aria-label={`${row.name} ${index===0?'request':'limit'} ${service?`${scope==='proxy'?'for proxy on':'for'} ${service}`:scope==='proxy'?'for proxy (optional)':'per service (optional)'}`} value={String(values[field]??'')} placeholder={service&&inherited?.[field]?`Inherited: ${inherited[field]}`:'Not set'} onChange={event=>onValue({[field]:event.target.value})}/></label>)}</>
}
function record(value:unknown):Record<string,unknown>{return value&&typeof value==='object'&&!Array.isArray(value)?value as Record<string,unknown>:{}}
