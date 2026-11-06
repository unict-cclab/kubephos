import {useEffect, useId, useRef, useState, type ReactNode} from 'react'

export function FormTabs({items, label, selectedId, onSelect,selectNewItems=false,actions}: {actions?:ReactNode;selectNewItems?:boolean;selectedId?: string; onSelect?: (id: string) => void; items: {id: string; title: string; content: ReactNode}[]; label: string}) {
  const [selected, setSelected] = useState<string>()
  const choice = selectedId ?? selected
  const active = items.some(item => item.id === choice) ? choice : items[0]?.id
  const choose = (value: string) => {setSelected(value); onSelect?.(value)}
  const ids=items.map(item=>item.id)
  const previousItems=useRef(ids)
  const itemsKey=JSON.stringify(ids)
  useEffect(()=>{
    const added=ids.find(value=>!previousItems.current.includes(value))
    previousItems.current=ids
    if(selectNewItems&&added)choose(added)
  },[itemsKey,selectNewItems])
  const id = useId()
  const tabList=<div className="form-tab-list" role="tablist" aria-label={label}>{items.map((item,index) => <button id={`${id}-${item.id}`} aria-controls={`${id}-${item.id}-panel`} role="tab" type="button" tabIndex={active === item.id ? 0 : -1} aria-selected={active === item.id} key={item.id} onClick={() => choose(item.id)} onKeyDown={event => {
    const offset = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : offset ? (index + offset + items.length) % items.length : -1
    if (next < 0) return
    event.preventDefault(); choose(items[next].id); document.getElementById(`${id}-${items[next].id}`)?.focus()
  }}>{item.title}</button>)}</div>
  return <div className="form-tabs">{actions?<div className="form-tab-heading">{tabList}<div className="form-tab-actions">{actions}</div></div>:tabList}{items.map(item => <div className="form-tab-panel" role="tabpanel" id={`${id}-${item.id}-panel`} aria-labelledby={`${id}-${item.id}`} hidden={active !== item.id} key={item.id}>{item.content}</div>)}</div>
}
