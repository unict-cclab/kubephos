import {ErrorFeedback} from './ToastRegion'
import {ClusterGeneralReadout,ClusterVMReadout,ClusterPoolsReadout,PoolPropertiesReadout} from './ClusterReadout'
import {WizardForm,WizardStep} from './WizardForm'
import {FormTabs} from './FormTabs'
import {PoolPlacementFields} from './PlacementFields'
import type {NodeTaint} from '../types'
import {useErrorState} from './ToastRegion'
import {useEffect, useId, useState} from 'react'
import {request} from '../api'
import type {ManagedResource, PipelineRun, Session} from '../types'
import {Dialog} from './Dialog'

type Capacity = {cores: number; memoryMiB: number; diskGiB: number}
type Pool = {labels?: Record<string,string>; taints?: NodeTaint[]; name: string; count: number; zones: string[]; capacity: Capacity}
type ClusterSpec = {name: string; controlPlanes: number; controlPlaneZones?: string[]; networking?: string; managementPool: Pool; applicationPools: Pool[]; machineNodes: Record<string, string>; baseVMID: number; addressStart: string}
export function clusterMachineNames(spec: ClusterSpec, pools: Pool[]) {
  return [{name: 'control-plane', count: spec.controlPlanes}, spec.managementPool, ...pools].flatMap(pool => Array.from({length: Math.max(0, Math.min(12, pool.count || 0))}, (_, index) => `${spec.name}-${pool.name}-${index + 1}`))
}

export function ClusterMaintenance({resource, runs, session,references={}}: {references?:Record<string,string>;resource: ManagedResource; runs: PipelineRun[]; session: Session}) {
  const [open, setOpen] = useState(false)
  const [queued, setQueued] = useState(false)
  const [saved,setSaved]=useState<ManagedResource|null>(null)
  useEffect(()=>setSaved(null),[resource.updatedAt])
  const current=saved??resource
  useEffect(() => {if (resource.status !== 'ready') setQueued(false)}, [resource.status])
  const spec = current.spec as unknown as ClusterSpec
  const blocked = resource.status !== 'ready' || queued
  const structuralDisabled=runs.some(run=>run.clusterResourceId===resource.id&&['queued','running'].includes(run.status))
  if (session.user?.role !== 'admin') return null
  return <><button className="button secondary" disabled={blocked} title={blocked?'The cluster is not ready to edit.':undefined} onClick={() => setOpen(true)}>Edit</button>{queued && <span role="status">Cluster maintenance queued</span>}{open && current.spec.mode==='multi' && <MultiClusterSettingsEditor references={references} resource={current} session={session} onClose={()=>setOpen(false)} onSaved={value=>{setSaved(value);setOpen(false)}}/>}{open && current.spec.mode!=='multi' && <MaintenanceEditor references={references} structuralDisabled={structuralDisabled} onSaved={value=>{setSaved(value);setOpen(false)}} spec={spec} resource={current} session={session} onClose={() => setOpen(false)} onQueued={() => {setQueued(true); setOpen(false)}} />}</>
}

function MaintenanceEditor({spec, resource, session, onClose, onQueued, onSaved, structuralDisabled,references}: {references:Record<string,string>;onSaved:(value:ManagedResource)=>void; structuralDisabled:boolean; spec: ClusterSpec; resource: ManagedResource; session: Session; onClose: () => void; onQueued: () => void}) {
  const formId = useId()
  const [pools, setPools] = useState<Pool[]>(() => structuredClone(spec.applicationPools ?? []))
  const [nodes, setNodes] = useState({...spec.machineNodes})
  const [newVMIDStart, setNewVMIDStart] = useState<number | ''>('')
  const [newAddressStart, setNewAddressStart] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const [confirmed, setConfirmed] = useState(false)
  const names = clusterMachineNames(spec, pools)
  const originalNames = clusterMachineNames(spec, spec.applicationPools ?? [])
  const added = names.filter(name => !originalNames.includes(name))
  const removed = originalNames.filter(name => !names.includes(name))
  const defaultNode = Object.values(spec.machineNodes ?? {})[0] ?? ''
  const moved = names.filter(name => originalNames.includes(name) && (nodes[name] ?? defaultNode) !== (spec.machineNodes?.[name] ?? defaultNode))
  const settingsChanged = pools.filter(pool => {const old=spec.applicationPools?.find(item=>item.name===pool.name);return old && (JSON.stringify(old.labels??{})!==JSON.stringify(pool.labels??{}) || JSON.stringify(old.taints??[])!==JSON.stringify(pool.taints??[]))}).length
  const structuralChanges = added.length + removed.length + moved.length > 0
  const changed = structuralChanges || JSON.stringify(pools) !== JSON.stringify(spec.applicationPools ?? [])
  const updatePool = (index: number, change: Partial<Pool>) => {setConfirmed(false); setPools(items => items.map((item, position) => position === index ? {...item, ...change} : item))}
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setPending(true); setError('')
    try {
      if(!structuralChanges) {
        const updated=await request<ManagedResource>(`/kubernetes-clusters/${resource.id}/reconfigure`,{method:'POST',body:JSON.stringify({updatedAt:resource.updatedAt,poolSettings:pools.map(pool=>({pool:pool.name,labels:pool.labels??{},taints:pool.taints??[]}))})},session.csrfToken)
        onSaved(updated)
      } else {
        await request(`/kubernetes-clusters/${resource.id}/reconfigure`, {method: 'POST', body: JSON.stringify({applicationPools: pools, machineNodes: Object.fromEntries(names.map(name => [name, nodes[name] ?? defaultNode])), newVMIDStart: newVMIDStart || 0, newAddressStart})}, session.csrfToken)
        onQueued()
      }
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not queue cluster maintenance.')} finally {setPending(false)}
  }
  return <Dialog open presentation="page" title="Edit cluster" onClose={pending?undefined:onClose} className="cluster-maintenance-modal"><WizardForm id={formId} onSubmit={event=>void submit(event)} onCancel={onClose} pending={pending} error={error} submitLabel="Save changes" submitDisabled={(!changed&&!error)||structuralDisabled&&structuralChanges||names.length>12||removed.length>0&&!confirmed}>
   <WizardStep title="General"><ClusterGeneralReadout spec={spec as unknown as Record<string,unknown>} references={references}/></WizardStep><WizardStep title="Node pools"><ClusterPoolsReadout spec={{...spec,applicationPools:pools}} references={references}
    actions={<button type="button" className="button secondary compact" disabled={structuralDisabled} onClick={()=>{setPools(items=>[...items,{name:`pool-${items.length+1}`,count:1,zones:['zone-a'],capacity:{cores:4,memoryMiB:8192,diskGiB:80}}]);setConfirmed(false)}}>Add pool</button>}
    renderPool={(value,index)=>{const pool=value as unknown as Pool;
        const existing = (spec.applicationPools ?? []).some(item => item.name === pool.name)
        return <section className="maintenance-pool-card" key={index} aria-label={`Pool ${pool.name}`}>

          <PoolPlacementFields key={pool.name} actions={<button type="button" className="button secondary compact" disabled={structuralDisabled} onClick={() => {setPools(items => items.filter((_, position) => position !== index));setConfirmed(false)}}>Remove pool {pool.name}</button>} properties={<div className="maintenance-fields">
            <label>Name<input aria-label={`Pool ${index + 1} name`} value={pool.name} readOnly={existing} pattern="[a-z0-9][a-z0-9-]{0,31}" required onChange={event => updatePool(index, {name: event.target.value})} /></label>
            <label>Nodes<input aria-label={`${pool.name} nodes`} disabled={structuralDisabled} type="number" min={1} max={12} required value={pool.count} onChange={event => updatePool(index, {count: Number(event.target.value)})} /></label>
            {!existing && <><label>Zones<input aria-label={`${pool.name} zones`} required value={pool.zones.join(', ')} onChange={event => updatePool(index, {zones: event.target.value.split(',').map(value => value.trim())})} /></label>{(['cores', 'memoryMiB', 'diskGiB'] as const).map(key => <label key={key}>{({cores: 'CPU (cores)', memoryMiB: 'Memory (MiB)', diskGiB: 'Disk (GiB)'})[key]}<input aria-label={`${pool.name} ${key}`} type="number" min={key === 'cores' ? 1 : key === 'memoryMiB' ? 1024 : 8} max={key === 'cores' ? 32 : key === 'memoryMiB' ? 131072 : 2048} value={pool.capacity[key]} required onChange={event => updatePool(index, {capacity: {...pool.capacity, [key]: Number(event.target.value)}})} /></label>)}</>}
          </div>} disclosure={false} defaults={{clusterName:spec.name,name:pool.name,role:'application',zones:pool.zones,count:pool.count,networking:spec.networking}} name={pool.name} labels={pool.labels} taints={pool.taints} onChange={change => updatePool(index,change)} />
        </section>
      }}/></WizardStep>
    <WizardStep title="VM placement"><fieldset disabled={pending||structuralDisabled} className="maintenance-section">
      <div className="maintenance-placement">{names.map(name => <label className="maintenance-placement-row" key={name}><code>{name}</code><span><span className="maintenance-server-label">Proxmox server</span><input aria-label={`${name} Proxmox server`} value={nodes[name] ?? defaultNode} pattern="[A-Za-z0-9._-]{1,64}" required onChange={event => {setNodes(items => ({...items, [name]: event.target.value}));setConfirmed(false)}} /></span></label>)}</div>
    </fieldset>
    {added.length > 0 && <fieldset disabled={pending} className="maintenance-section"><legend>New VM allocation</legend>
      <div className="maintenance-fields"><label>First new VMID<input aria-label="First new VMID" type="number" min={100} max={999999999} required value={newVMIDStart} onChange={event => setNewVMIDStart(Number(event.target.value))} /></label><label>First new IPv4 address<input aria-label="First new IPv4 address" required value={newAddressStart} onChange={event => setNewAddressStart(event.target.value)} /></label></div>
    </fieldset>}
    </WizardStep><WizardStep title="Review"><section className="maintenance-review" aria-label="Maintenance summary"><h3>Review changes</h3><div className="maintenance-change-counts"><span><strong>{added.length}</strong> nodes to add</span><span><strong>{removed.length}</strong> nodes to remove</span><span><strong>{moved.length}</strong> VMs to migrate</span>{settingsChanged>0 && <span><strong>{settingsChanged}</strong> pool settings to update</span>}</div>
      {names.length > 12 && <ErrorFeedback message="Maximum: 12 nodes."/>}
      {removed.length > 0 && <div className="maintenance-removal"><ul>{removed.map(name => <li key={name}><code>{name}</code></li>)}</ul><label className="checkbox-field"><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} required disabled={pending} /><span>Confirm removal of these nodes and their ephemeral data</span></label></div>}
    </section>
    </WizardStep>
  </WizardForm></Dialog>
}

function MultiClusterSettingsEditor({resource,session,onClose,onSaved,references}: {references:Record<string,string>;resource:ManagedResource;session:Session;onClose:()=>void;onSaved:(resource:ManagedResource)=>void}) {
 const formId=useId()
 const original=resource.spec.members as unknown as ClusterSpec[]
 const [members,setMembers]=useState(()=>structuredClone(original))
 const [pending,setPending]=useState(false)
 const [error,setError]=useErrorState()
 const changed=JSON.stringify(members)!==JSON.stringify(original)
 const save=async(event:React.FormEvent)=>{
  event.preventDefault();setPending(true);setError('')
  try {
   const value=await request<ManagedResource>(`/kubernetes-clusters/${resource.id}/reconfigure`,{method:'POST',body:JSON.stringify({updatedAt:resource.updatedAt,poolSettings:members.flatMap(member=>member.applicationPools.map(pool=>({member:member.name,pool:pool.name,labels:pool.labels??{},taints:pool.taints??[]})))})},session.csrfToken)
   onSaved(value)
  }catch(cause){setError(cause instanceof Error?cause.message:'Could not save pool settings.')}finally{setPending(false)}
 }
 const memberTabs=(label:string,render:(member:ClusterSpec,index:number)=>React.ReactNode)=><FormTabs label={label} items={members.map((member,index)=>({id:member.name,title:member.name,content:render(member,index)}))}/>
 return <Dialog open presentation="page" title="Edit cluster" className="cluster-maintenance-modal" onClose={pending?undefined:onClose}>
 <WizardForm id={formId} onSubmit={event=>void save(event)} onCancel={onClose} pending={pending} error={error} submitLabel="Save changes" submitDisabled={!changed&&!error}>
  <WizardStep title="General">{memberTabs('Cluster settings',member=><ClusterGeneralReadout spec={member as unknown as Record<string,unknown>} references={references}/>)}</WizardStep>
  <WizardStep title="Node pools">{memberTabs('Cluster pools',(member,memberIndex)=><ClusterPoolsReadout references={references} spec={{...member,networking:'cilium'}} renderPool={(_,poolIndex)=>{const pool=member.applicationPools[poolIndex];return <PoolPlacementFields key={pool.name} properties={<PoolPropertiesReadout value={pool as unknown as Record<string,unknown>} spec={{networking:'cilium'}} references={references}/>} disclosure={false} name={pool.name} defaults={{clusterName:member.name,name:pool.name,role:'application',networking:'cilium',count:pool.count}} labels={pool.labels} taints={pool.taints} onChange={change=>setMembers(current=>current.map((item,index)=>index===memberIndex?{...item,applicationPools:item.applicationPools.map((value,index)=>index===poolIndex?{...value,...change}:value)}:item))}/>}}/>)}</WizardStep>
  <WizardStep title="VM placement">{memberTabs('Cluster VM placement',member=><ClusterVMReadout spec={member as unknown as Record<string,unknown>}/>)}</WizardStep>
  <WizardStep title="Review"><p>{members.reduce((total,member,index)=>total+member.applicationPools.filter((pool,poolIndex)=>JSON.stringify(pool)!==JSON.stringify(original[index].applicationPools[poolIndex])).length,0)} pool settings to update</p></WizardStep>
 </WizardForm></Dialog>
}
