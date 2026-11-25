import {useEffect, useId, useRef, useState, type ReactNode} from 'react'

// A button opening a small panel (export choices, more actions); closes on outside click and Esc.
export function HeaderMenu({label, icon, children, align = 'end', buttonClass = 'button secondary'}: {label: string; icon?: ReactNode; children: (close: () => void) => ReactNode; align?: 'start' | 'end'; buttonClass?: string}) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const id = useId()
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => {if (!root.current?.contains(event.target as Node)) setOpen(false)}
    const escape = (event: globalThis.KeyboardEvent) => {if (event.key === 'Escape') {setOpen(false); root.current?.querySelector<HTMLButtonElement>('button')?.focus()}}
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    return () => {document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', escape)}
  }, [open])
  return <div className="header-menu" ref={root}>
    <button type="button" className={buttonClass} aria-expanded={open} aria-controls={open ? id : undefined} onClick={() => setOpen(value => !value)}>{icon}{label}</button>
    {open && <div className={`header-menu-panel ${align}`} id={id}>{children(() => setOpen(false))}</div>}
  </div>
}
