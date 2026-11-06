import type {ReactNode} from 'react'
import {FormTabs} from './FormTabs'

// The same pool hierarchy is used by editable and read-only cluster details.
export function PoolSections({master,management,pools,actions}:{master:ReactNode;management:ReactNode;pools:{id:string;title:string;content:ReactNode}[];actions?:ReactNode}) {
 return <FormTabs label="Node pools" items={[
 {id:'master',title:'Master',content:master},
 {id:'management',title:'Management',content:management},
 {id:'pools',title:'Pools',content:<div className="form-stack">{pools.length?<FormTabs selectNewItems label="Application pools" actions={actions} items={pools}/>:<>{actions}<p className="muted">None</p></>}</div>}
 ]}/>
}
