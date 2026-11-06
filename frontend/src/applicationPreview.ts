import {request} from './api'
import type {Application} from './types'

const pending=new Map<string,Promise<unknown>>()
function stable(value:unknown):unknown {
 if(Array.isArray(value))return value.map(stable)
 if(value&&typeof value==='object')return Object.fromEntries(Object.entries(value).sort(([a],[b])=>a.localeCompare(b)).map(([key,item])=>[key,stable(item)]))
 return value
}
// Topology and placement share the same read-only materialization request.
// Keep only in-flight requests, so later inspections always receive fresh data.
export function resolveApplicationPreview<T>(application:Application,values:Record<string,unknown>,csrfToken?:string):Promise<T> {
 const body=JSON.stringify(stable({...application.descriptor.spec?.defaults,...values}))
 const key=JSON.stringify([application.id,application.digest,csrfToken,body])
 const current=pending.get(key)
 if(current)return current as Promise<T>
 const result=request<T>(`/catalog/applications/${encodeURIComponent(application.id)}/resolve`,{method:'POST',body},csrfToken)
 pending.set(key,result)
 void result.then(()=>pending.delete(key),()=>pending.delete(key))
 return result
}
