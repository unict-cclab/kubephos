import {createdByLabel, modifiedByLabel} from './ResourceAttribution'
import type {ResourceAttributionFields} from '../types'
import {ActionIcon} from './ActionIcon'
import {useEffect, useMemo, useRef, useState, type ReactNode} from 'react'
import {formatDate} from '../lib'

type DatedResource = ResourceAttributionFields & {id: string; createdAt: string; updatedAt: string}
type DateField = 'createdAt' | 'updatedAt'

export function ResourceListTable<T extends DatedResource>({items, label, columns, actions, searchText, filters, empty, editable = false}: {
  items: T[]
  label: string
  columns: {label: string; render: (item: T) => ReactNode}[]
  actions: (item: T) => ReactNode
  filters?: ReactNode
  empty?: ReactNode
  editable?: boolean
  searchText?: (item: T) => string
}) {
  const displayMenu = useRef<HTMLDetailsElement>(null)
  useEffect(() => {
    const close = (event: PointerEvent) => {
      if (event.target instanceof Node && !displayMenu.current?.contains(event.target) && displayMenu.current) displayMenu.current.open = false
    }
    document.addEventListener('pointerdown', close)
    return () => document.removeEventListener('pointerdown', close)
  }, [])
  const [query, setQuery] = useState('')
  const [hidden, setHidden] = useState(new Set<string>())
  const visibleColumns = columns.filter(column => !hidden.has(column.label))
  const toggle = (name: string) => setHidden(current => {const next = new Set(current); if (next.has(name)) next.delete(name); else next.add(name); return next})
  const optionalColumns = [...columns.slice(1).map(column => ({id: column.label, label: column.label})), {id: 'createdAt', label: 'Created'}, {id: 'createdBy', label: 'Created by'}, {id: 'updatedAt', label: 'Last updated'}, ...(editable ? [{id: 'modifiedBy', label: 'Last updated by'}] : [])]
  const [sort, setSort] = useState<{field: DateField; descending: boolean}>({field: 'updatedAt', descending: true})
  const ordered = useMemo(() => items.filter(item => !searchText || `${searchText(item)} ${createdByLabel(item)} ${item.modifiedBy ?? ''}`.toLowerCase().includes(query.trim().toLowerCase())).map(item => {
    const date = Date.parse(item[sort.field] || item.createdAt)
    return {item, timestamp: Number.isFinite(date) ? date : 0}
  }).sort((left, right) => (left.timestamp - right.timestamp) * (sort.descending ? -1 : 1) || left.item.id.localeCompare(right.item.id)).map(({item}) => item), [items, query, searchText, sort.field, sort.descending])
  const dateHeader = (field: DateField, title: string) => <th scope="col" aria-sort={sort.field === field ? sort.descending ? 'descending' : 'ascending' : 'none'}><button type="button" onClick={() => setSort(current => ({field, descending: current.field === field ? !current.descending : true}))}>{title}<ActionIcon name={sort.field === field ? sort.descending ? 'down' : 'up' : 'sort'} /></button></th>
  return <><div className="list-toolbar">{searchText && <input type="search" aria-label={`Search ${label.toLowerCase()}`} placeholder="Search…" value={query} onChange={event => setQuery(event.target.value)} />}<span>{ordered.length} of {items.length}</span><div className="list-toolbar-controls">{filters}<details ref={displayMenu} className="table-columns" onKeyDown={event => {if (event.key === 'Escape') {event.currentTarget.open = false; event.currentTarget.querySelector('summary')?.focus()}}}><summary aria-label="Table display" title="Table display"><ActionIcon name="layout" /></summary><div className="table-column-options"><label className="table-sort-option">Sort by<select value={`${sort.field}:${sort.descending}`} onChange={event => {const [field, descending] = event.target.value.split(':'); setSort({field: field as DateField, descending: descending === 'true'})}}><option value="updatedAt:true">Last updated · newest</option><option value="updatedAt:false">Last updated · oldest</option><option value="createdAt:true">Created · newest</option><option value="createdAt:false">Created · oldest</option></select></label>{optionalColumns.map(column => <label key={column.id}><input type="checkbox" checked={!hidden.has(column.id)} onChange={() => toggle(column.id)} />{column.label}</label>)}</div></details></div></div><div className="resource-list-table-wrap adaptive-resource-list"><table className="resource-list-table" aria-label={label}><thead><tr>{visibleColumns.map(column => <th scope="col" key={column.label}>{column.label}</th>)}{!hidden.has('createdAt') && dateHeader('createdAt', 'Created')}{!hidden.has('createdBy') && <th scope="col">Created by</th>}{!hidden.has('updatedAt') && dateHeader('updatedAt', 'Last updated')}{editable && !hidden.has('modifiedBy') && <th scope="col">Last updated by</th>}<th scope="col">Actions</th></tr></thead><tbody>{ordered.map(item => <tr key={item.id}>{visibleColumns.map(column => <td key={column.label} data-label={column.label}>{column.render(item)}</td>)}{!hidden.has('createdAt') && <td className="resource-list-date" data-label="Created">{item.createdAt ? formatDate(item.createdAt) : '—'}</td>}{!hidden.has('createdBy') && <td className="resource-list-author" data-label="Created by">{createdByLabel(item)}</td>}{!hidden.has('updatedAt') && <td className="resource-list-date" data-label="Last updated">{item.updatedAt || item.createdAt ? formatDate(item.updatedAt || item.createdAt) : '—'}</td>}{editable && !hidden.has('modifiedBy') && <td className="resource-list-author" data-label="Last updated by">{modifiedByLabel(item)}</td>}<td data-label="Actions"><div className="history-row-actions">{actions(item)}</div></td></tr>)}{!ordered.length && <tr><td colSpan={visibleColumns.length + 1 + Number(!hidden.has('createdBy')) + Number(editable && !hidden.has('modifiedBy')) + Number(!hidden.has('createdAt')) + Number(!hidden.has('updatedAt'))}><div className="table-empty"><div className="table-empty-copy">{items.length ? 'No results match your search.' : empty ?? `No ${label.toLowerCase()}.`}</div>{query && <button type="button" className="button secondary compact" onClick={() => setQuery('')}>Clear search</button>}</div></td></tr>}</tbody></table></div></>
}
