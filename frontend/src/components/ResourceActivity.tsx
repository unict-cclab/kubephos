import {useEffect, useState} from 'react'
import {request} from '../api'
import type {ManagedResource, Operation, PipelineRun, PipelineRunLogEntry} from '../types'
import {ExecutionProgressGraph} from './ExecutionProgressGraph'
import {LogViewer} from './LogViewer'
import {operationProgressNodes} from '../operationProgress'

const finished = (status: string) => ['succeeded','failed','canceled'].includes(status)

export function ResourceActivity({resource, initialRun, openOperation, openPipelineRun}: {resource: ManagedResource; initialRun?: PipelineRun; openOperation: (id: string) => void; openPipelineRun?: (id: string) => void}) {
 const runID = resource.deletionOperationId ? undefined : resource.recreationPipelineRunId ?? resource.deletionPipelineRunId ?? resource.pipelineRunId
 const operationID = resource.deletionOperationId ?? resource.operationId
 const source = runID ? `/pipeline-runs/${runID}` : operationID ? `/operations/${operationID}` : ''
 const [run,setRun] = useState<PipelineRun | undefined>(initialRun?.id === runID ? initialRun : undefined)
 const [operation,setOperation] = useState<Operation>()
 const [logs,setLogs] = useState<PipelineRunLogEntry[]>([])
 const [error,setError] = useState('')
 const [loaded,setLoaded] = useState(false)
 const [retry,setRetry] = useState(0)
 useEffect(()=>{
  const controller = new AbortController()
  let timer: number | undefined
  let after=0
  setLogs([]);setError('');setLoaded(false)
  const load=async()=>{
   let done=false
   try {
    const current = await request<PipelineRun | Operation>(source,{},undefined,controller.signal)
    if(controller.signal.aborted)return
    if(!current) throw new Error('Could not load provisioning activity.')
    if(runID)setRun(current as PipelineRun);else setOperation(current as Operation)
    for(;;){
     const result=await request<{items:PipelineRunLogEntry[]}>(`${source}/logs?after=${after}`,{},undefined,controller.signal)
     if(controller.signal.aborted)return
     if(!result?.items)throw new Error('Could not load provisioning logs.')
     if(result.items.length){after=result.items.at(-1)!.sequence;setLogs(items=>[...items,...result.items])}
     if(result.items.length<(runID?5000:1000))break
    }
    done=finished(current.status);setError('');setLoaded(true)
   }catch(cause){if(!controller.signal.aborted)setError(cause instanceof Error?cause.message:'Could not load provisioning activity.');done=true}
   finally{if(!controller.signal.aborted&&!done)timer=window.setTimeout(()=>void load(),2000)}
  }
  if(source)void load()
  return()=>{controller.abort();if(timer!==undefined)window.clearTimeout(timer)}
 },[source,runID,retry])
 const nodes=runID ? run?.stages.map(stage=>({id:stage.id,title:stage.title,status:stage.status,detail:stage.error||stage.cleanupError||undefined,operationId:stage.operationId}))??[] : operation ? operationProgressNodes(operation) : []
 const status = runID ? run?.status : operation?.status
 const showLogs = runID && openPipelineRun ? ()=>openPipelineRun(runID) : operationID ? ()=>openOperation(operationID) : undefined
 return <>
  {resource.error&&<p className="managed-error">{resource.error}</p>}
  <section className="details-section activity-status"><div><span>Status</span><strong className={`status-pill ${resource.status}`}>{resource.status}</strong></div>{nodes.length>0&&<div><span>Progress</span><strong>{nodes.filter(node=>node.status==='succeeded').length}/{nodes.length}</strong></div>}</section>
  {nodes.length>0&&<section className="details-section"><ExecutionProgressGraph nodes={nodes} label={`${resource.name} lifecycle progress`} openOperation={runID&&openPipelineRun?undefined:openOperation} showLogs={showLogs}/></section>}
  {source?<section className="details-section"><div className="details-heading"><strong>Process logs</strong></div>{error&&<p className="managed-error" role="alert">{error} <button className="text-button" onClick={()=>setRetry(value=>value+1)}>Retry</button></p>}<LogViewer entries={logs} label="Process logs" context={runID?entry=>`${entry.stagePosition}. ${entry.stageTitle}${entry.phase==='cleanup'?' · cleanup':''}`:undefined} filename={`${resource.name}-provisioning-logs.txt`} emptyMessage={error?'Logs could not be loaded.':!loaded?'Loading process logs…':finished(status??'')?'No process log entries.':'Waiting for the first process log entry…'}/></section>:<p className="details-copy">No provisioning activity available.</p>}
 </>
}
