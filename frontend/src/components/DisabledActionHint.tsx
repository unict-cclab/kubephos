import {useEffect,useId,useState,type ReactNode} from 'react'

export function DisabledActionHint({disabled,reason,children}:{disabled:boolean;reason:string;children:ReactNode}) {
 const id=useId()
 const [open,setOpen]=useState(false)
 useEffect(()=>setOpen(false),[disabled,reason])
 return <span className="disabled-action-hint" tabIndex={disabled?0:undefined} title={disabled&&!open?reason:undefined} aria-describedby={disabled?id:undefined} onMouseEnter={()=>{if(disabled)setOpen(true)}} onMouseLeave={()=>setOpen(false)} onFocus={()=>{if(disabled)setOpen(true)}} onBlur={()=>setOpen(false)}>
  {children}
  {disabled&&<span id={id} role="tooltip" className="disabled-action-tooltip" hidden={!open}>{reason}</span>}
 </span>
}
