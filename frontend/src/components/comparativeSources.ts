import type {ExperimentConfiguration} from '../types'
export function comparativeSources(items:ExperimentConfiguration[]) {
 const seen=new Set<string>()
 return items.filter(item=>{
  if(item.kind==='suite'||item.definition.kind==='suite'||!item.validation.valid||seen.has(item.id))return false
  seen.add(item.id)
  return true
 })
}
