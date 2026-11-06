import {useEffect,useState} from 'react'
import type {SidebarSection} from '../navigation'
import type {View} from '../types'

export function SidebarNavigation({sections, view, navigate}: {sections: SidebarSection[]; view: View; navigate: (view: View,hash?:string) => void}) {
 const [hash,setHash]=useState(window.location.hash.replace(/^#/,''))
 useEffect(()=>{const update=()=>setHash(window.location.hash.replace(/^#/,''));window.addEventListener('hashchange',update);return()=>window.removeEventListener('hashchange',update)},[])
 const [expanded,setExpanded]=useState<Record<string,boolean>>(()=>Object.fromEntries(sections.map(section=>[section.id,section.items.some(item=>item.view===view)])))
 useEffect(()=>{const current=sections.find(section=>section.items.some(item=>item.view===view));if(current)setExpanded(previous=>({...previous,[current.id]:true}))},[view])
 return <nav className="app-nav app-nav-direct" aria-label="Main navigation">{sections.map(section=>{
  const grouped=Boolean(section.label)
  const current=section.items.some(item=>item.view===view)
  const open=expanded[section.id]??false
  const first=section.items[0]
  const iconView=section.id==='experiments'?'experiments':section.id==='administration'?'administration':first.view
  return <div className={`app-nav-section${current?' is-open':''}`} key={section.id}>
   <button type="button" className={`app-nav-link app-nav-section-link${!grouped&&current?' active':''}`} aria-expanded={grouped?open:undefined} aria-controls={grouped?`nav-${section.id}`:undefined} aria-current={!grouped&&current?'page':undefined} onClick={()=>grouped?setExpanded(previous=>({...previous,[section.id]:!open})):navigate(first.view)}><NavigationIcon view={iconView}/><span>{section.label||first.label}</span>{grouped&&<svg className={`app-nav-chevron${open?' expanded':''}`} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true"><path d="m9 5 7 7-7 7"/></svg>}</button>
   {grouped&&<div id={`nav-${section.id}`} className="app-nav-pages" hidden={!open}>{section.items.map(item=>{const active=view===item.view&&(!item.hash||hash===item.hash||hash==='infrastructure'&&item.hash==='infrastructure/connections');return <button type="button" key={item.hash??item.view} className={`app-nav-link${active?' active':''}`} aria-current={active?'page':undefined} onClick={()=>item.hash?navigate(item.view,item.hash):navigate(item.view)}><span>{item.label}</span></button>})}</div>}
  </div>
 })}</nav>
}

function NavigationIcon({view}: {view: View}) {
  const common = {fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const}
  const shape = {
    overview: <><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></>,
    deployments: <><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M7 13h5m-5 3h9"/></>,
    infrastructure: <><rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6"/></>,
    kubernetes: <><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/><path d="M12 3v6m0 6v6M3 12h6m6 0h6"/></>,
    catalog: <><path d="M4 5h16v14H4zM4 12h16M9 5v7m6 0v7"/></>,
    experiments: <><path d="M9 3h6m-5 0v6L5 18a2 2 0 0 0 1.8 3h10.4a2 2 0 0 0 1.8-3l-5-9V3M7 15h10"/></>,
    comparisons: <><path d="M4 18V9m8 9V4m8 14v-7M2 21h20"/></>,
    results: <><circle cx="12" cy="12" r="9"/><path d="m10 8 6 4-6 4z"/></>,
    administration: <><path d="m10 3-.7 2.4-2.4 1L4.7 5.3 2.3 9.5l1.8 1.7v2l-1.8 1.7 2.4 4.2 2.2-1.1 2.4 1L10 21h4l.7-2 2.4-1 2.2 1.1 2.4-4.2-1.8-1.7v-2l1.8-1.7-2.4-4.2-2.2 1.1-2.4-1L14 3z"/><circle cx="12" cy="12" r="3"/></>,
    users: <><circle cx="9" cy="8" r="3"/><path d="M3.5 19c.5-4 2.3-6 5.5-6s5 2 5.5 6M16 7h5M18.5 4.5v5"/></>
  } as Partial<Record<View, React.ReactNode>>
  return <svg width="20" height="20" viewBox="0 0 24 24" aria-hidden="true" {...common}>{shape[view]}</svg>
}

