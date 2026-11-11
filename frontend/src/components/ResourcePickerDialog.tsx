import {createPortal} from 'react-dom'
import {useEffect, useMemo, useState} from 'react'
import type {Session} from '../types'
import {formatDate} from '../lib'
import {Dialog} from './Dialog'
import {ResponsiveTable} from './ResponsiveTable'
import {OwnerFilter, ownerLabel} from './OwnerFilter'
import {ErrorFeedback, useErrorState} from './ToastRegion'

export interface PickerResource {id: string; name: string; ownerUserId?: string; ownerUsername?: string; createdAt?: string}
export interface CandidatePage {items: PickerResource[]; total: number; owners: Array<{id: string; username: string}>}
export type CandidateLoader = (query: string, owner: string, offset: number, signal: AbortSignal) => Promise<CandidatePage>
const pageSize = 40

export function ResourcePickerDialog({resource, items, selectedIds, session, close, add, load}: {resource: 'experiments' | 'configurations'; items: PickerResource[]; selectedIds: string[]; session: Session; close: () => void; add: (items: PickerResource[]) => void; load?: CandidateLoader}) {
  const [query, setQuery] = useState('')
  const [owner, setOwner] = useState(session.user?.id ?? '')
  const [offset, setOffset] = useState(0)
  const [chosen, setChosen] = useState<PickerResource[]>([])
  const [remote, setRemote] = useState<CandidatePage>({items: [], total: 0, owners: []})
  const [loading, setLoading] = useState(Boolean(load))
  const [revision, setRevision] = useState(0)
  const [error, setError] = useErrorState()
  const excluded = new Set(selectedIds)
  const remaining = Math.max(0, 8 - selectedIds.length)
  useEffect(() => {
    if (!load) return
    const controller = new AbortController()
    setLoading(true); setError('')
    if (!offset) setRemote(current => ({...current, items: []}))
    const timer = window.setTimeout(() => {
      void load(query.trim(), owner, offset, controller.signal).then(page => {
        if (!controller.signal.aborted) setRemote(current => ({...page, items: offset ? [...current.items, ...page.items.filter(item => !current.items.some(known => known.id === item.id))] : page.items}))
      }).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load the selection.')}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    }, query ? 200 : 0)
    return () => {window.clearTimeout(timer); controller.abort()}
  }, [load, query, owner, offset, revision])
  const local = useMemo(() => items.filter(item => (!owner || item.ownerUserId === owner) && item.name.toLowerCase().includes(query.trim().toLowerCase())), [items, owner, query])
  const visible = (load ? remote.items : local.slice(0, offset + pageSize)).filter(item => !excluded.has(item.id))
  const total = load ? remote.total : local.length
  const loadedCount = load ? remote.items.length : Math.min(local.length, offset + pageSize)
  const owners = [...items, ...remote.owners.map(owner => ({ownerUserId: owner.id, ownerUsername: owner.username})), ...(session.user ? [{ownerUserId: session.user.id, ownerUsername: session.user.username}] : [])]
  const toggle = (item: PickerResource) => {
    if (excluded.has(item.id)) return
    setChosen(current => current.some(value => value.id === item.id) ? current.filter(value => value.id !== item.id) : current.length < remaining ? [...current, item] : current)
  }
  return createPortal(<Dialog open title={`Add ${resource}`} className="resource-picker-modal" onClose={close}>
    <div className="form-stack">
      <div className="list-toolbar selection-toolbar"><input type="search" aria-label={`Search ${resource}`} placeholder="Search by name…" value={query} onChange={event => {setQuery(event.target.value); setOffset(0)}} /><OwnerFilter items={owners} value={owner} onChange={value => {setOwner(value); setOffset(0)}} /></div>
      <ErrorFeedback message={error} />
      <div className="resource-selection-scroll"><ResponsiveTable aria-label={`Available ${resource}`}><thead><tr><th>Select</th><th>Name</th><th>Created by</th><th>Created</th></tr></thead><tbody>{visible.map(item => <tr key={item.id}><td><input type="checkbox" aria-label={`Select ${item.name}`} checked={chosen.some(value => value.id === item.id)} disabled={!chosen.some(value => value.id === item.id) && chosen.length >= remaining} onChange={() => toggle(item)} /></td><td>{item.name}</td><td>{ownerLabel(item)}</td><td>{item.createdAt ? formatDate(item.createdAt) : '—'}</td></tr>)}{!visible.length && !loading && <tr><td colSpan={4}>No matching {resource}.</td></tr>}</tbody></ResponsiveTable></div>
      {loading && <p role="status">Loading {resource}…</p>}
      {error && <button type="button" className="button secondary" onClick={() => setRevision(value => value + 1)}>Retry</button>}
      {loadedCount < total && <button type="button" className="button secondary" disabled={loading} onClick={() => setOffset(value => value + pageSize)}>Load more</button>}
      <div className="dialog-actions"><span className="selection-count">{chosen.length} selected</span><button type="button" className="button secondary" onClick={close}>Cancel</button><button type="button" className="button primary" disabled={!chosen.length} onClick={() => add(chosen.filter(item => !excluded.has(item.id)).slice(0, remaining))}>Add selected</button></div>
    </div>
  </Dialog>, document.body)
}
