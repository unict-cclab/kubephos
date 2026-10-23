import {useId, useState, type ReactNode} from 'react'

export function FormTabs({items, label}: {items: {id: string; title: string; content: ReactNode}[]; label: string}) {
  const [selected, setSelected] = useState(items[0]?.id)
  const active = items.some(item => item.id === selected) ? selected : items[0]?.id
  const id = useId()
  return <div className="form-tabs"><div className="form-tab-list" role="tablist" aria-label={label}>{items.map((item,index) => <button id={`${id}-${item.id}`} aria-controls={`${id}-${item.id}-panel`} role="tab" type="button" tabIndex={active === item.id ? 0 : -1} aria-selected={active === item.id} key={item.id} onClick={() => setSelected(item.id)} onKeyDown={event => {
    const offset = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : offset ? (index + offset + items.length) % items.length : -1
    if (next < 0) return
    event.preventDefault(); setSelected(items[next].id); document.getElementById(`${id}-${items[next].id}`)?.focus()
  }}>{item.title}</button>)}</div>{items.map(item => <div className="form-tab-panel" role="tabpanel" id={`${id}-${item.id}-panel`} aria-labelledby={`${id}-${item.id}`} hidden={active !== item.id} key={item.id}>{item.content}</div>)}</div>
}
