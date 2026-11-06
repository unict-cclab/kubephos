import {useEffect,useState} from 'react'
import {request} from '../api'
import type {WorkloadPlacementDefaults} from './PlacementDefaults'
import type {Application,ApplicationComponent,ApplicationPlacement,ManagedResource,Session} from '../types'
import {ApplicationPlacementEditor} from './ApplicationPlacementEditor'
import {Dialog} from './Dialog'
import {ErrorFeedback,useErrorState,useNotifications} from './ToastRegion'

export function DeploymentPlacementEditor({resource,application,session,networking,onClose,onSaved}:{resource:ManagedResource;application?:Application;session:Session;networking?:string;onClose:()=>void;onSaved:(warning?:string)=>Promise<void>}) {
 const [pending,setPending]=useState(false),[error,setError]=useErrorState()
 const [services,setServices]=useState<ApplicationComponent[]|null>(null)
 const notify=useNotifications()?.notify
 const [defaults,setDefaults]=useState<WorkloadPlacementDefaults[]>([])
 const definition=resource.spec.definition as {applicationPlacement?:ApplicationPlacement;applicationValues?:Record<string,unknown>}
 useEffect(()=>{
  let current=true
  void request<{workloadTargets:ApplicationComponent[];placementDefaults:WorkloadPlacementDefaults[]}>(`/application-deployments/${resource.id}/placement-defaults`).then(result=>{if(current){setServices(result.workloadTargets);setDefaults(result.placementDefaults)}}).catch(()=>{if(current)setError('Could not load application services.')})
  return()=>{current=false}
 },[application?.id,resource.id,session.csrfToken])
 return <Dialog open presentation="page" title="Edit placement" onClose={pending?undefined:onClose}>
  <form onSubmit={async event=>{
   event.preventDefault();const form=event.currentTarget
   if(!form.reportValidity())return
   const value=new FormData(form).get('applicationPlacement')
   setPending(true);setError('')
   try {
    const saved=await request<ManagedResource&{placementWarning?:string}>(`/application-deployments/${resource.id}/placement`,{method:'PUT',body:JSON.stringify({updatedAt:resource.updatedAt,applicationPlacement:JSON.parse(String(value??'null'))})},session.csrfToken)
    await onSaved(saved.placementWarning);onClose()
    if(saved.placementWarning)notify?.(saved.placementWarning,'info')
   } catch(cause){setError(cause instanceof Error?cause.message:'Could not update placement.')}
   finally{setPending(false)}
  }}>
   <fieldset className="deployment-placement-fields" disabled={pending||services===null}>
    {services===null?<p role="status">Loading services…</p>:<ApplicationPlacementEditor defaults={defaults} services={services} application={application} session={session} values={definition.applicationValues} networking={networking} initial={definition.applicationPlacement}/>}
   </fieldset>
   <ErrorFeedback message={error}/>
   <div className="modal-actions"><button type="button" className="button secondary" disabled={pending} onClick={onClose}>Cancel</button><button className="button primary" disabled={pending||services===null}>{pending?'Saving…':'Save'}</button></div>
  </form>
 </Dialog>
}
