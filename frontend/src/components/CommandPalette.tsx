import {useEffect, useRef, useState, type KeyboardEvent} from 'react'
import type {NavigationItem} from '../navigation'
import type {View} from '../types'
import {ActionIcon} from './ActionIcon'

export function paletteShortcut(): string {
  return /Mac|iPhone|iPad/.test(navigator.userAgent) ? '⌘K' : 'Ctrl K'
}

export function isPaletteShortcut(event: {key: string; metaKey: boolean; ctrlKey: boolean}): boolean {
  return (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k'
}

// Mounted only while open: the search field is a combobox over the matching sections.
export function CommandPalette({items, close, select}: {items: NavigationItem[]; close: () => void; select: (view: View) => void}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const search = query.trim().toLowerCase()
  const matches = search ? items.filter(item => `${item.label} ${item.description}`.toLowerCase().includes(search)) : items
  const current = matches[Math.min(active, matches.length - 1)]

  useEffect(() => {
    const element = dialog.current
    if (element && !element.open) element.showModal()
  }, [])

  useEffect(()=>{
    dialog.current?.querySelector<HTMLElement>('[role="option"][aria-selected="true"]')?.scrollIntoView?.({block:'nearest'})
  },[active,query])

  const choose = (item: NavigationItem) => {
    close()
    select(item.view)
  }
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!matches.length) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const step = event.key === 'ArrowDown' ? 1 : -1
      setActive(index => (Math.min(index, matches.length - 1) + step + matches.length) % matches.length)
    } else if (event.key === 'Enter' && current) {
      event.preventDefault()
      choose(current)
    }
  }

  return <dialog ref={dialog} className="modal command-palette" aria-label="Jump to" onClose={close} onClick={event => {if (event.target === dialog.current) close()}}>
    <div className="command-palette-search">
      <ActionIcon name="search" />
      <input role="combobox" aria-label="Jump to" aria-expanded="true" aria-controls="command-palette-results" aria-autocomplete="list" aria-activedescendant={current ? `command-${current.view}` : undefined} placeholder="Jump to a section…" value={query} onChange={event => {setQuery(event.target.value); setActive(0)}} onKeyDown={onKeyDown} />
      <kbd>Esc</kbd>
    </div>
    <ul id="command-palette-results" role="listbox" aria-label="Sections">{matches.map(item => <li key={item.view} id={`command-${item.view}`} role="option" aria-selected={item === current} onMouseEnter={() => setActive(matches.indexOf(item))} onClick={() => choose(item)}>
          <strong>{item.label}</strong><span>{item.description}</span><ActionIcon name="forward" />
        </li>)}</ul>
    {!matches.length&&<p className="command-palette-empty" role="status">No sections match “{query.trim()}”.</p>}
  </dialog>
}
