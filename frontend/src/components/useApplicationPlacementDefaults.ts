import {useEffect, useRef, useState} from 'react'
import type {Application, ApplicationComponent, Session} from '../types'
import {resolveApplicationPreview} from '../applicationPreview'
import {readSchemaValues} from '../lib'
import type {WorkloadPlacementDefaults} from './PlacementDefaults'

type Preview = {placementDefaults?:WorkloadPlacementDefaults[]; placementDefaultsError?:string}
export function useApplicationPlacementDefaults({application,session,values,networking,enabled=true,services=[]}: {application?:Application;session?:Session;values?:Record<string,unknown>;networking?:string;enabled?:boolean;services?:ApplicationComponent[]}) {
 const ref=useRef<HTMLElement>(null)
 const [defaults,setDefaults]=useState<WorkloadPlacementDefaults[]|null>(null)
 const [pending,setPending]=useState(false)
 const [error,setError]=useState('')
 const valuesKey=JSON.stringify(values??{})
 useEffect(()=>{
  setDefaults(null);setError('');setPending(false)
  if(!enabled || !application || !session)return
  let current=true,sequence=0,timer:ReturnType<typeof setTimeout>|undefined
  const load=async(form?:HTMLFormElement)=>{
   const id=++sequence;setPending(true);setError('')
   try {
    const parameters=form?readSchemaValues(form,application.descriptor.spec?.valuesSchema??{type:'object'},'applicationValues',[application]):JSON.parse(valuesKey)
    const preview=await resolveApplicationPreview<Preview>(application,parameters,session.csrfToken)
    if(!current||id!==sequence)return
    setDefaults(preview.placementDefaults??[]);setError(preview.placementDefaultsError??'')
   } catch {if(current&&id===sequence)setError('Could not load application placement defaults.')}
   finally {if(current&&id===sequence)setPending(false)}
  }
  const form=ref.current?.closest('form')
  void load(form??undefined)
  const change=(event:Event)=>{const target=event.target;if(!(target instanceof HTMLInputElement||target instanceof HTMLSelectElement||target instanceof HTMLTextAreaElement)||!target.name.startsWith('applicationValues.'))return;clearTimeout(timer);sequence++;timer=setTimeout(()=>void load(form??undefined),300)}
  form?.addEventListener('change',change)
  return ()=>{current=false;clearTimeout(timer);form?.removeEventListener('change',change)}
 },[application?.id,application?.digest,session?.csrfToken,enabled,valuesKey])
 // The runtime always adds this selector to declared application workloads. Do
 // not infer catalog tolerations while a manifest preview is unavailable.
 const baseline:WorkloadPlacementDefaults[]=services.map(service=>({id:service.id,nodeSelector:{'kubephos.dev/role':'application'}}))
 return {ref,defaults:(defaults??baseline).filter(value=>networking!=='cilium'||!('nodeBinding' in value)||value.nodeBinding!=='application-node'),pending,error}
}
