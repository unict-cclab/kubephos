import {useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent} from 'react'
import {ActionIcon, type ActionIconName} from '../../ActionIcon'
import './contextMenu.css'

// A right-click menu, also opened from a ⋯ button: arrow keys move, Enter runs,
// Esc or a click elsewhere closes. It stays inside the window. An item with `checked`
// is a checkbox: it shows a tick when on.

export interface MenuItem {label: string; hint?: string; icon?: ActionIconName; checked?: boolean; danger?: boolean; separated?: boolean; disabled?: boolean; run: () => void}
// Where the menu opens: at the pointer, or hanging left from the bottom-right corner of a button.
export interface MenuAnchor {x: number; y: number; fromButton?: boolean}

export const anchorAtButton = (button: HTMLElement): MenuAnchor => {const box = button.getBoundingClientRect(); return {x: box.right, y: box.bottom + 4, fromButton: true}}

export function ContextMenu({at, label, items, onClose}: {at: MenuAnchor; label: string; items: MenuItem[]; onClose: () => void}) {
  const root = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({left: at.x, top: at.y})
  useLayoutEffect(() => {
    const box = root.current?.getBoundingClientRect()
    if (!box) return
    setPosition({
      left: Math.max(8, Math.min(at.fromButton ? at.x - box.width : at.x, window.innerWidth - box.width - 8)),
      top: Math.max(8, Math.min(at.y, window.innerHeight - box.height - 8))
    })
    root.current?.querySelector<HTMLButtonElement>('[role^="menuitem"]:not(:disabled)')?.focus({preventScroll: true})
  }, [at])
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const outside = (event: MouseEvent) => {if (!root.current?.contains(event.target as Node)) close.current()}
    const away = () => close.current()
    document.addEventListener('mousedown', outside)
    window.addEventListener('scroll', away, true)
    window.addEventListener('resize', away)
    return () => {document.removeEventListener('mousedown', outside); window.removeEventListener('scroll', away, true); window.removeEventListener('resize', away)}
  }, [])
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const buttons = [...(root.current?.querySelectorAll<HTMLButtonElement>('[role^="menuitem"]:not(:disabled)') ?? [])]
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
    if (event.key === 'Escape' || event.key === 'Tab') {event.preventDefault(); event.stopPropagation(); onClose()}
    else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {event.preventDefault(); buttons[(index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus()}
    else if (event.key === 'Home' || event.key === 'End') {event.preventDefault(); buttons[event.key === 'Home' ? 0 : buttons.length - 1]?.focus()}
  }
  return <div ref={root} className="context-menu" role="menu" aria-label={label} style={position} onKeyDown={onKey} onContextMenu={event => event.preventDefault()}>
    {items.map(item => <button key={item.label} type="button" role={item.checked === undefined ? 'menuitem' : 'menuitemcheckbox'} aria-checked={item.checked} className={`context-menu-item${item.danger ? ' danger' : ''}`} data-separated={item.separated || undefined} disabled={item.disabled} onClick={() => {onClose(); item.run()}}>
      {item.checked === undefined ? item.icon && <ActionIcon name={item.icon} /> : <span className="context-menu-check" aria-hidden="true">{item.checked && <ActionIcon name="check" />}</span>}<span><strong>{item.label}</strong>{item.hint && <small>{item.hint}</small>}</span>
    </button>)}
  </div>
}
