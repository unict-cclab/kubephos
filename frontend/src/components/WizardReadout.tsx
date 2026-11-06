import {useId,useRef,useState,type ReactNode} from 'react'

export function WizardNavigation({titles,current,pending=false,onSelect,label='Setup steps'}:{titles:string[];current:number;pending?:boolean;onSelect:(index:number)=>void;label?:string}) {
 return <nav className="wizard-progress" aria-label={label}><label className="wizard-step-picker"><span>Step {current+1} of {titles.length}</span><select aria-label={label==='Setup steps'?'Setup step':'Section'} value={current} disabled={pending} onChange={event=>onSelect(Number(event.target.value))}>{titles.map((title,index)=><option key={index} value={index}>{title}</option>)}</select></label><ol>{titles.map((title,index)=><li key={index}><button type="button" aria-current={index===current?'step':undefined} disabled={pending} onClick={()=>onSelect(index)}><span className="wizard-step-number" aria-hidden="true">{index+1}</span><span>{title}</span></button></li>)}</ol></nav>
}

export function WizardReadout({sections,label='Configuration sections'}:{sections:{id:string;title:string;content:ReactNode}[];label?:string}) {
 const root=useRef<HTMLDivElement>(null)
 const [selected,setSelected]=useState('')
 const index=sections.findIndex(section=>section.id===selected)
 const current=index<0?0:index
 const id=useId()
 const choose=(next:number)=>{setSelected(sections[next].id);requestAnimationFrame(()=>{document.getElementById(`${id}-${next}`)?.focus({preventScroll:true});root.current?.scrollIntoView?.({block:'start',behavior:'instant'})})}
 return <div ref={root} className="wizard-form wizard-readout"><WizardNavigation titles={sections.map(section=>section.title)} current={current} onSelect={choose} label={label}/><div className="wizard-panels">{sections.map((section,index)=><section key={section.id} hidden={index!==current} aria-labelledby={`${id}-${index}`}><div className="wizard-panel-heading"><span className="wizard-position">{index+1} / {sections.length}</span><h3 id={`${id}-${index}`} tabIndex={-1}>{section.title}</h3></div>{section.content}</section>)}</div></div>
}
