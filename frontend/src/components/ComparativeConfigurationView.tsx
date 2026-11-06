import {ErrorFeedback} from './ToastRegion'
import {ResourceAttribution} from './ResourceAttribution'
import type {Application,ExperimentConfiguration,ManagedResource} from '../types'
import {EditorPage} from './EditorPage'
import {WizardReadout} from './WizardReadout'
import {ReadoutTable,type ReadoutTableData} from './ReadoutTable'
import {InputData} from './InputData'
export function ComparativeConfigurationView({item,configurations,applications,clusters,close}:{item:ExperimentConfiguration;configurations:ExperimentConfiguration[];applications:Application[];clusters:ManagedResource[];close:()=>void}) {
 const variants=item.definition.variants??[]
 const runs=item.definition.runs??3
 const rows:ReadoutTableData['rows']=variants.map(member=>{
  const source=configurations.find(value=>value.id===member.configurationId)
  const alias=member.alias||source?.name||'Unavailable configuration'
  return [source?.name??'Unavailable configuration',alias,{type:'color',value:member.color??'',label:`Plot color for ${alias}`},clusters.find(value=>value.id===source?.clusterResourceId)?.name??'Unavailable cluster',source?.definition.destinationMode==='external'?source.definition.externalDestination?.targetUrl??'—':applications.find(value=>value.reference===source?.applicationRef)?.name??'—',!source?'This configuration is no longer available.':!source.validation.valid?'This configuration needs validation.':'Ready']
 })
 return <EditorPage title={item.name} onClose={close} className="comparative-view-page"><ResourceAttribution resource={item} editable/><WizardReadout sections={[
  {id:'configurations',title:'Configurations',content:<ReadoutTable label="Configurations" data={{headers:['Name','Plot label','Color','Kubernetes cluster','Application / external destination','Status'],rows}}/>},
  {id:'execution',title:'Execution',content:<div className="form-stack"><InputData value={{executionsPerConfiguration:runs,totalExecutions:variants.length*runs}}/><ReadoutTable label="Execution order" data={{headers:['Position','Configuration'],rows:variants.map((_,index)=>[String(index+1),String(rows[index][1])])}}/></div>}
 ]}/>{!item.validation.valid&&<ErrorFeedback message="Validate this comparison to run it."/>}</EditorPage>
}
