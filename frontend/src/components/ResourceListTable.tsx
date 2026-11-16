import {createdByLabel, modifiedByLabel} from './ResourceAttribution'
import type {ResourceAttributionFields} from '../types'
import {ActionIcon} from './ActionIcon'
import {useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode} from 'react'
import {formatDate} from '../lib'

type DatedResource = ResourceAttributionFields & {id: string; createdAt: string; updatedAt: string}
type DateField = 'createdAt' | 'updatedAt'
type Sort = {field: DateField; descending: boolean}

const sortOptions: {value: string; label: string}[] = [
  {value: 'updatedAt:true', label: 'Last updated · newest'},
  {value: 'updatedAt:false', label: 'Last updated · oldest'},
  {value: 'createdAt:true', label: 'Created · newest'},
  {value: 'createdAt:false', label: 'Created · oldest'},
]

function DateCell({value, label}: {value: string; label: string}) {
  return <td className="resource-list-date" data-label={label}>{value ? <time dateTime={value}>{formatDate(value)}</time> : '—'}</td>
}

// A click anywhere on a row opens its details, except on its own controls or
// while selecting text. Keyboard users reach the same page through the row's
// labelled View action.
function opensRow(event: MouseEvent<HTMLTableRowElement>) {
  return !(event.target as HTMLElement).closest('button, a, input, select, textarea, summary, label') && !window.getSelection()?.toString()
}

// Selection mode for actions that apply to several items at once. Passing it
// turns the mode on: only selectable rows get a checkbox, and a bar explains
// what to do, carries the bulk actions and leaves the mode with `cancel`.
export interface ListSelection<T> {
  selected: ReadonlySet<string>
  onChange: (selected: Set<string>) => void
  selectable: (item: T) => boolean
  itemLabel: (item: T) => string
  actions: ReactNode
  hint: string
  cancel: () => void
}

function SelectAll({checked, indeterminate, disabled, label, onChange}: {checked: boolean; indeterminate: boolean; disabled: boolean; label: string; onChange: (checked: boolean) => void}) {
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {if (input.current) input.current.indeterminate = indeterminate}, [indeterminate])
  return <input ref={input} type="checkbox" aria-label={label} checked={checked} disabled={disabled} onChange={event => onChange(event.target.checked)} />
}

export function ResourceListTable<T extends DatedResource>({items, label, columns, actions, open, searchText, filters, empty, editable = false, selection}: {
  items: T[]
  label: string
  columns: {label: string; render: (item: T) => ReactNode}[]
  actions: (item: T) => ReactNode
  open?: (item: T) => void
  filters?: ReactNode
  empty?: ReactNode
  editable?: boolean
  searchText?: (item: T) => string
  selection?: ListSelection<T>
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
  const [sort, setSort] = useState<Sort>({field: 'updatedAt', descending: true})

  const shown = (id: string) => !hidden.has(id)
  const toggle = (id: string) => setHidden(current => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })
  const visibleColumns = columns.filter(column => shown(column.label))
  const optionalColumns = [
    ...columns.slice(1).map(column => ({id: column.label, label: column.label})),
    {id: 'createdAt', label: 'Created'},
    {id: 'createdBy', label: 'Created by'},
    {id: 'updatedAt', label: 'Last updated'},
    ...(editable ? [{id: 'modifiedBy', label: 'Last updated by'}] : []),
  ]
  const showModifiedBy = editable && shown('modifiedBy')
  const columnCount = visibleColumns.length + 1 + (selection ? 1 : 0) + [shown('createdAt'), shown('createdBy'), shown('updatedAt'), showModifiedBy].filter(Boolean).length

  const ordered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return items
      .filter(item => !searchText || `${searchText(item)} ${createdByLabel(item)} ${item.modifiedBy ?? ''}`.toLowerCase().includes(needle))
      .map(item => {
        const date = Date.parse(item[sort.field] || item.createdAt)
        return {item, timestamp: Number.isFinite(date) ? date : 0}
      })
      .sort((left, right) => (left.timestamp - right.timestamp) * (sort.descending ? -1 : 1) || left.item.id.localeCompare(right.item.id))
      .map(({item}) => item)
  }, [items, query, searchText, sort.field, sort.descending])

  // Selecting all applies to the rows matching the current search.
  const selectableRows = selection ? ordered.filter(selection.selectable) : []
  const selectedRows = selection ? selectableRows.filter(item => selection.selected.has(item.id)) : []
  const selectRow = (item: T, checked: boolean) => {
    if (!selection) return
    const next = new Set(selection.selected)
    if (checked) next.add(item.id)
    else next.delete(item.id)
    selection.onChange(next)
  }
  const selectAll = (checked: boolean) => {
    if (!selection) return
    const next = new Set(selection.selected)
    for (const item of selectableRows) {
      if (checked) next.add(item.id)
      else next.delete(item.id)
    }
    selection.onChange(next)
  }

  const dateHeader = (field: DateField, title: string) => {
    const active = sort.field === field
    return <th scope="col" aria-sort={active ? sort.descending ? 'descending' : 'ascending' : 'none'}>
      <button type="button" onClick={() => setSort(current => ({field, descending: current.field === field ? !current.descending : true}))}>
        {title}<ActionIcon name={active ? sort.descending ? 'down' : 'up' : 'sort'} />
      </button>
    </th>
  }

  // With nothing to list, search, counts and columns are noise: show the
  // empty state alone, keeping any filters so their choice can be changed.
  if (!items.length) return <>
    {filters && <div className="list-toolbar"><div className="list-toolbar-controls">{filters}</div></div>}
    <div className="resource-list-table-wrap">
      <div className="table-empty"><div className="table-empty-copy">{empty ?? `No ${label.toLowerCase()}.`}</div></div>
    </div>
  </>

  return <>
    <div className="list-toolbar">
      {searchText && <input type="search" aria-label={`Search ${label.toLowerCase()}`} placeholder="Search…" value={query} onChange={event => setQuery(event.target.value)} />}
      <span>{ordered.length} of {items.length}</span>
      <div className="list-toolbar-controls">
        {filters}
        <details ref={displayMenu} className="table-columns" onKeyDown={event => {if (event.key === 'Escape') {event.currentTarget.open = false; event.currentTarget.querySelector('summary')?.focus()}}}>
          <summary aria-label="Table display" title="Table display"><ActionIcon name="layout" /></summary>
          <div className="table-column-options">
            <label className="table-sort-option">Sort by
              <select value={`${sort.field}:${sort.descending}`} onChange={event => {const [field, descending] = event.target.value.split(':'); setSort({field: field as DateField, descending: descending === 'true'})}}>
                {sortOptions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
              </select>
            </label>
            {optionalColumns.map(column => <label key={column.id}><input type="checkbox" checked={shown(column.id)} onChange={() => toggle(column.id)} />{column.label}</label>)}
          </div>
        </details>
      </div>
    </div>
    {selection && <div className="list-bulk-bar" role="region" aria-label={`Selected ${label.toLowerCase()}`} onKeyDown={event => {if (event.key === 'Escape') selection.cancel()}}>
      {selection.selected.size ? <strong aria-live="polite">{selection.selected.size} selected</strong> : <span aria-live="polite">{selection.hint}</span>}
      <div className="list-bulk-actions">
        {selection.actions}
        <button type="button" className="button secondary compact" onClick={selection.cancel}>Cancel</button>
      </div>
    </div>}
    <div className="resource-list-table-wrap adaptive-resource-list">
      <table className="resource-list-table" aria-label={label}>
        <thead>
          <tr>
            {selection && <th scope="col" className="resource-list-select"><SelectAll label={`Select all ${label.toLowerCase()}`} checked={selectableRows.length > 0 && selectedRows.length === selectableRows.length} indeterminate={selectedRows.length > 0 && selectedRows.length < selectableRows.length} disabled={!selectableRows.length} onChange={selectAll} /></th>}
            {visibleColumns.map(column => <th scope="col" key={column.label}>{column.label}</th>)}
            {shown('createdAt') && dateHeader('createdAt', 'Created')}
            {shown('createdBy') && <th scope="col">Created by</th>}
            {shown('updatedAt') && dateHeader('updatedAt', 'Last updated')}
            {showModifiedBy && <th scope="col">Last updated by</th>}
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {ordered.map(item => {
            // In selection mode a click on a selectable row toggles it instead of opening it.
            const toggles = Boolean(selection?.selectable(item))
            const activate = toggles ? () => selectRow(item, !selection!.selected.has(item.id)) : open ? () => open(item) : undefined
            return <tr key={item.id} className={[activate ? 'is-openable' : '', toggles && selection!.selected.has(item.id) ? 'is-selected' : ''].filter(Boolean).join(' ') || undefined} onClick={activate ? event => {if (opensRow(event)) activate()} : undefined}>
            {selection && <td className="resource-list-select" data-label="Select">{selection.selectable(item) && <input type="checkbox" aria-label={`Select ${selection.itemLabel(item)}`} checked={selection.selected.has(item.id)} onChange={event => selectRow(item, event.target.checked)} />}</td>}
            {visibleColumns.map(column => <td key={column.label} data-label={column.label}>{column.render(item)}</td>)}
            {shown('createdAt') && <DateCell value={item.createdAt} label="Created" />}
            {shown('createdBy') && <td className="resource-list-author" data-label="Created by">{createdByLabel(item)}</td>}
            {shown('updatedAt') && <DateCell value={item.updatedAt || item.createdAt} label="Last updated" />}
            {showModifiedBy && <td className="resource-list-author" data-label="Last updated by">{modifiedByLabel(item)}</td>}
            <td data-label="Actions"><div className="history-row-actions">{actions(item)}</div></td>
          </tr>})}
          {!ordered.length && <tr>
            <td colSpan={columnCount}>
              <div className="table-empty">
                <div className="table-empty-copy">No results match your search.</div>
                {query && <button type="button" className="button secondary compact" onClick={() => setQuery('')}>Clear search</button>}
              </div>
            </td>
          </tr>}
        </tbody>
      </table>
    </div>
  </>
}
