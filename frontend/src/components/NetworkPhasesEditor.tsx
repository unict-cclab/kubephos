import {formatDuration} from './LoadProfileEditor'
import {ValidationMessage} from './ValidationMessage'
import {useEffect,useRef,useState} from 'react'
import {NetworkProfileEditor} from './NetworkProfileEditor'
import {readSchemaValues} from '../lib'
import type {JsonSchema} from '../types'
import {FormTabs} from './FormTabs'
import {FormDisclosure} from './FormDisclosure'

const effectsSchema:JsonSchema={type:'object',properties:{enableLatency:{type:'boolean'},latencyMilliseconds:{type:'integer'},jitterMilliseconds:{type:'integer'},correlationPercent:{type:'number'},enableBandwidth:{type:'boolean'},bandwidthBytesPerSecond:{type:'integer'},enablePacketLoss:{type:'boolean'},packetLossPercent:{type:'number'},linkOverrides:{type:'array',items:{type:'object'}}}}
type Phase={id:number;durationSeconds:number;profile:Record<string,unknown>}
export function NetworkInjectionEditor({prefix,zones,values={},distribution='zone',durationSeconds=0,onValidityChange}: {prefix:string;zones:string[];values?:Record<string,unknown>;distribution?:'zone'|'cluster';durationSeconds?:number;onValidityChange?:(valid:boolean)=>void}) {
 const initial=Array.isArray(values.phases)?values.phases as Record<string,unknown>[]:[]
 const [phased,setPhased]=useState(initial.length>0)
 const [phases,setPhases]=useState<Phase[]>(initial.length?initial.map((value,id)=>({id,durationSeconds:Number(value.durationSeconds),profile:{...value}})):[{id:0,durationSeconds:durationSeconds||60,profile:{...values}}])
 const [activePhase,setActivePhase]=useState(String(phases[0].id))
 const sequence=useRef(phases.length)
 const root=useRef<HTMLDivElement>(null)
 const total=phases.reduce((sum,phase)=>sum+phase.durationSeconds,0)
 const valid=!phased || durationSeconds>0&&total===durationSeconds&&phases.every(phase=>Number.isInteger(phase.durationSeconds)&&phase.durationSeconds>0)
 useEffect(()=>{onValidityChange?.(valid)},[valid,onValidityChange])
 useEffect(()=>{if(!phased&&durationSeconds>0)setPhases(current=>current.map(phase=>({...phase,durationSeconds})))},[durationSeconds,phased])
 const read=(phase:Phase)=>{const form=root.current?.closest('form');return form?readSchemaValues(form,effectsSchema,phased?`${prefix}.phase-${phase.id}`:prefix):phase.profile}
 const add=(clone?:Phase)=>{
  const captured=phases.map(phase=>({...phase,profile:read(phase)}))
  const source=clone?captured.find(phase=>phase.id===clone.id):undefined
  const id=sequence.current++
  setActivePhase(String(id))
  setPhases([...captured,{id,durationSeconds:source?.durationSeconds??Math.max(1,durationSeconds-total||60),profile:structuredClone(source?.profile??captured[captured.length-1].profile)}]);setPhased(true)
 }
 const sync=()=>{if(phased)setPhases(current=>{let changed=false;const result=current.map(phase=>{const profile=read(phase);if(JSON.stringify(profile)===JSON.stringify(phase.profile))return phase;changed=true;return {...phase,profile}});return changed?result:current})}
 const phaseActions=<div className="network-phase-actions"><button type="button" className="button secondary compact" disabled={phases.length>=32} onClick={()=>add()}>Add phase</button></div>
 return <div className="network-phases-editor form-stack" ref={root} onChange={sync}>{phased&&<div className="timeline-duration-summary"><div><strong>Network duration</strong><span>{formatDuration(total)}</span></div><div><strong>Workload duration</strong><span>{formatDuration(durationSeconds)}</span></div></div>}

  {!phased?<FormTabs actions={phaseActions} label="Network phases" items={[{id:'static',title:'Phase 1',content:<div className="form-stack network-phase-panel"><div className="network-phase-actions"><label>Duration (min)<input type="number" value={durationSeconds>0?durationSeconds/60:''} readOnly disabled/></label><button type="button" className="button secondary compact" onClick={()=>add(phases[0])}>Clone</button><button type="button" className="button secondary compact" disabled>Remove</button></div><NetworkProfileEditor prefix={prefix} zones={zones} values={values} distribution={distribution} showTarget={false}/></div>}]} />:<>
   <input type="hidden" name={`${prefix}.phases`} value={JSON.stringify(phases.map(phase=>({...phase.profile,durationSeconds:phase.durationSeconds})))} readOnly data-review-title="Network phases" data-review-table={JSON.stringify({headers:['Phase','Duration (min)','One-way latency (ms)','Jitter (ms)','Bandwidth (B/s)','Packet loss (%)','Links'],rows:phases.map((phase,index)=>[String(index+1),String(phase.durationSeconds/60),...['latencyMilliseconds','jitterMilliseconds','bandwidthBytesPerSecond','packetLossPercent'].map(key=>phase.profile[key==='bandwidthBytesPerSecond'?'enableBandwidth':key==='packetLossPercent'?'enablePacketLoss':'enableLatency']?String(phase.profile[key]??0):'—'),String(Array.isArray(phase.profile.linkOverrides)?phase.profile.linkOverrides.length:0)])})}/>
   {phases.map((phase,index)=>Array.isArray(phase.profile.linkOverrides)&&phase.profile.linkOverrides.length>0&&<input key={phase.id} type="hidden" value="" readOnly data-review-title={`Network links · Phase ${index+1}`} data-review-table={JSON.stringify({headers:['Source','Target','Direction','One-way latency (ms)','Jitter (ms)','Bandwidth (B/s)','Packet loss (%)'],rows:(phase.profile.linkOverrides as Record<string,unknown>[]).map(link=>[String(link.sourceZone),String(link.targetZone),link.bidirectional===true?'Bidirectional':'One-way',...['latencyMilliseconds','jitterMilliseconds','bandwidthBytesPerSecond','packetLossPercent'].map(key=>String(link[key]??'—'))])})}/>)}
   {Object.entries(effectsSchema.properties!).map(([key,schema])=><input key={key} type="hidden" name={`${prefix}.${key}`} value={schema.type==='array'?JSON.stringify(phases[0].profile[key]??[]):String(phases[0].profile[key]??(schema.type==='boolean'?false:key==='latencyMilliseconds'?50:key==='bandwidthBytesPerSecond'?12500000:0))} readOnly data-review-skip="true"/>)}
   {!valid&&<ValidationMessage message={`Network phases must total ${formatDuration(durationSeconds)}.`}/>}
   <FormTabs actions={phaseActions} label="Network phases" selectedId={activePhase} onSelect={setActivePhase} items={phases.map((phase,index)=>({id:String(phase.id),title:`Phase ${index+1}`,content:<div className="form-stack network-phase-panel" data-review-skip="true"><div className="network-phase-actions"><label>Duration (min)<input type="number" min={1/60} max={1440} step="any" required value={phase.durationSeconds/60} onChange={event=>setPhases(current=>current.map(value=>value.id===phase.id?{...value,durationSeconds:Math.round(Number(event.target.value)*60)}:value))}/></label><button type="button" className="button secondary compact" disabled={phases.length>=32} onClick={()=>add(phase)}>Clone</button><button type="button" className="button secondary compact" disabled={phases.length===1} onClick={()=>setPhases(current=>current.filter(value=>value.id!==phase.id))}>Remove</button></div><NetworkProfileEditor prefix={`${prefix}.phase-${phase.id}`} zones={zones} values={{...phase.profile,linkOverrides:phase.profile.linkOverrides??[]}} distribution={distribution} showTarget={false} allowNeutral onProfileChange={sync}/></div>}))}/>

  </>}
   <FormDisclosure title="Network target"><div className="schema-fields network-target-fields"><label>Node group label<input name={`${prefix}.nodeGroupLabel`} defaultValue={String(values.nodeGroupLabel??'topology.kubernetes.io/zone')} required/></label><label>Affected node selector<input name={`${prefix}.nodeSelector`} defaultValue={String(values.nodeSelector??'kubephos.dev/role=application')}/></label><label>Network interface<input name={`${prefix}.networkInterface`} defaultValue={String(values.networkInterface??'flannel.1')} required/></label><label className="checkbox-field"><input type="checkbox" name={`${prefix}.hostNetwork`} defaultChecked={values.hostNetwork===true}/><span>Shape host-network traffic</span></label></div></FormDisclosure>
 </div>
}
