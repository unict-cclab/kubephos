import {DeploymentPlacementEditor} from './DeploymentPlacementEditor'
import {useState} from 'react'
import {request} from '../api'
import {useDetailRoute} from '../detailRoute'
import type {Application, CatalogStrategy, ExperimentConfiguration, ManagedResource, PipelineRun, Plugin, Session, Workspace} from '../types'
import {ExperimentConfigurationDialog} from './ExperimentConfigurationsView'
import {ConfigurationReadout} from './ConfigurationReadout'
import {ResourceListHeading} from './ResourceListHeading'
import {ResourceListTable} from './ResourceListTable'
import {ResourceAction} from './ResourceAction'
import {ResourceAttribution} from './ResourceAttribution'
import {ResourceActivity} from './ResourceActivity'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {ReadoutTable} from './ReadoutTable'

interface Props {
 deployments?: ManagedResource[]
 kubernetesClusters: ManagedResource[]
 workspaces: Workspace[]
 applications: Application[]
 plugins: Plugin[]
 strategies: CatalogStrategy[]
 session: Session
 isAdmin: boolean
 changed: (message?:string)=>Promise<void>
 openOperation: (id:string)=>void
 openPipelineRun?: (id:string)=>void
 pipelineRuns: PipelineRun[]
}

export function ApplicationDeploymentsView(props:Props) {
 const [creating,setCreating]=useState(false)
 const [deleting,setDeleting]=useState<string|null>(null)
 const [error,setError]=useErrorState()
 const [editing,setEditing]=useState<ManagedResource|null>(null)
 const [retrying,setRetrying]=useState<string|null>(null)
 const detail=useDetailRoute('deployments')
 const {confirmDelete,confirmationDialog}=useDeleteConfirmation()
 const deployments=props.deployments??[]
 const resource=detail.route?.kind==='resource'?deployments.find(item=>item.id===detail.route?.id):undefined
 const cluster=(item:ManagedResource)=>props.kubernetesClusters.find(cluster=>cluster.id===item.spec.clusterResourceId)
 const remove=async(item:ManagedResource)=>{
  if(!await confirmDelete(`Delete deployment ${item.name} and its namespace ${definition(item).namespace} on ${cluster(item)?.spec.mode==='multi'?'all cluster members':'the cluster'}?`))return
  setDeleting(item.id);setError('')
  try {await request(`/application-deployments/${item.id}`,{method:'DELETE'},props.session.csrfToken);await props.changed('Deployment removal queued.')}
  catch(cause){setError(cause instanceof Error?cause.message:'Could not remove the deployment.')}
  finally {setDeleting(null)}
 }
 const retry=async(item:ManagedResource)=>{setRetrying(item.id);try{await request(`/application-deployments/${item.id}/retry`,{method:'POST'},props.session.csrfToken);await props.changed('Deployment retry queued.')}catch(cause){setError(cause instanceof Error?cause.message:'Could not retry deployment.')}finally{setRetrying(null)}}
 if(editing)return <DeploymentPlacementEditor resource={editing} application={props.applications.find(app=>app.reference===editing.spec.applicationRef)} session={props.session} networking={cluster(editing)?.spec.mode==='multi'?'cilium':String(cluster(editing)?.spec.networking??'')} onClose={()=>setEditing(null)} onSaved={warning=>props.changed(warning?undefined:'Placement updated.')}/>
 const actions=(item:ManagedResource)=><>{['ready','provisioning','failed'].includes(item.status)&&<ResourceAction action="edit" label={`Edit placement for ${item.name}`} onClick={()=>setEditing(item)}/>} {item.status==='failed'&&<button type="button" className="button secondary compact" disabled={retrying===item.id} onClick={()=>void retry(item)}>Retry</button>}</>
 if(creating)return <ExperimentConfigurationDialog deploymentMode open items={[]} experiments={[]} clusters={props.kubernetesClusters} workspaces={props.workspaces} applications={props.applications} plugins={props.plugins} strategies={props.strategies} session={props.session} isAdmin={props.isAdmin} changed={()=>props.changed('Application deployment queued.')} openOperation={props.openOperation} close={()=>setCreating(false)}/>
 const canRemove=(item:ManagedResource)=>['ready','failed','deletion-failed'].includes(item.status)
 const removeAction=(item:ManagedResource)=><ResourceAction action="delete" label={`Delete ${item.name}`} disabled={!canRemove(item)||deleting===item.id} onClick={()=>void remove(item)}/>
 return <>{confirmationDialog}<ErrorFeedback message={error}/>{resource?<DeploymentDetail {...props} resource={resource} cluster={cluster(resource)} close={detail.close} remove={<>{actions(resource)}{removeAction(resource)}</>}/>:<>
  <ResourceListHeading title="Deployments" icon="application" action={<ResourceAction action="create" label="Deploy application" disabled={!props.kubernetesClusters.some(item=>item.status==='ready')||!props.applications.length} onClick={()=>setCreating(true)}/>}/>
  <ResourceListTable items={deployments} label="Application deployments" searchText={item=>`${item.name} ${item.spec.applicationRef} ${definition(item).namespace} ${cluster(item)?.name??''}`} columns={[
   {label:'Name',render:item=><h3>{item.name}</h3>},
   {label:'Application',render:item=>props.applications.find(app=>app.reference===item.spec.applicationRef)?.name??String(item.spec.applicationRef)},
   {label:'Cluster',render:item=>cluster(item)?.name??'—'},
   {label:'Namespace',render:item=>definition(item).namespace??'—'},
   {label:'Status',render:item=><span className={`status ${item.status}`}>{item.status}</span>}
  ]} actions={item=><><ResourceAction action="view" label={`View ${item.name} details`} onClick={()=>detail.open('resource',item.id)}/>{actions(item)}{removeAction(item)}</>} empty={<><strong>No application deployments</strong><p>Deploy a catalog application on a ready cluster.</p></>}/>
 </>}</>
}
function definition(resource:ManagedResource):ExperimentConfiguration['definition'] {return resource.spec.definition as ExperimentConfiguration['definition']}
function DeploymentDetail(props:Props & {resource:ManagedResource;cluster?:ManagedResource;close:()=>void;remove:React.ReactNode}) {
 const [tab,setTab]=useState<'configuration'|'activity'>('configuration')
 const configuration={id:props.resource.id,name:props.resource.name,clusterResourceId:props.resource.spec.clusterResourceId,applicationRef:props.resource.spec.applicationRef,definition:definition(props.resource)} as ExperimentConfiguration
 const members=props.cluster?.spec.mode==='multi' && Array.isArray(props.cluster.spec.members)?props.cluster.spec.members as {name:string}[]:[]
 return <div className="detail-page"><DetailBreadcrumb parent="Deployments" current={props.resource.name} back={props.close}/><div className="detail-page-heading"><h2>{props.resource.name}</h2><div className="detail-page-actions"><span className={`status ${props.resource.status}`}>{props.resource.status}</span>{props.remove}</div></div><ResourceAttribution resource={props.resource}/>
  {members.length>0&&<ReadoutTable label="Cluster members" data={{headers:['Cluster','Namespace','Status'],rows:members.map((member,index)=>[member.name,definition(props.resource).namespace??'—',props.pipelineRuns.find(run=>run.id===props.resource.pipelineRunId)?.stages.find(stage=>stage.stageId===`deploy-${index+1}`)?.status??'pending'])}}/>}
  <nav className="resource-tabs" aria-label="Deployment detail">{(['configuration','activity'] as const).map(id=><button key={id} aria-current={tab===id?'page':undefined} className={tab===id?'active':''} onClick={()=>setTab(id)}>{id==='configuration'?'Configuration':'Activity'}</button>)}</nav>
  {tab==='configuration'?<ConfigurationReadout session={props.session} deploymentMode showHeading={false} item={configuration} clusters={props.kubernetesClusters} applications={props.applications} plugins={props.plugins} strategies={props.strategies}/>:<ResourceActivity resource={props.resource} initialRun={props.pipelineRuns.find(run=>run.id===(props.resource.deletionPipelineRunId??props.resource.pipelineRunId))} openOperation={props.openOperation} openPipelineRun={props.openPipelineRun}/>}
 </div>
}
