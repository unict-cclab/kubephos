import {useState, type FormEvent, type ReactNode} from 'react'
import {request} from '../api'
import {formatDate, formatRelativeTime} from '../lib'
import type {AdminRequest, Session} from '../types'
import {Dialog} from './Dialog'
import {ResourceListHeading} from './ResourceListHeading'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {ResourceAction} from './ResourceAction'
import {useDeleteConfirmation} from './ConfirmationDialog'

// Requests work like tickets, not a chat: the limits match the API.
const maxTextLength = 4000
const maxOpenRequests = 3

interface Props {
  requests: AdminRequest[]
  isAdmin: boolean
  session: Session
  changed: (message?: string) => Promise<void>
}

const requestPath = (item: AdminRequest) => `/admin-requests/${encodeURIComponent(item.id)}`

// Long histories show their newest requests first and reveal older ones on demand.
function RequestList({items, label, limit, render}: {items: AdminRequest[]; label: string; limit: number; render: (item: AdminRequest) => ReactNode}) {
  const [expanded, setExpanded] = useState(false)
  const hidden = expanded ? 0 : Math.max(items.length - limit, 0)
  return <>
    <ul className="request-list" aria-label={label}>{(hidden ? items.slice(0, limit) : items).map(render)}</ul>
    {hidden > 0 && <button type="button" className="button ghost compact request-more" onClick={() => setExpanded(true)}>Show {hidden} older {hidden === 1 ? 'request' : 'requests'}</button>}
  </>
}

// Administrators review every request; other users write to them and follow their own.
export function RequestsView(props: Props) {
  return props.isAdmin ? <RequestInbox {...props} /> : <RequestComposer {...props} />
}

function RequestInbox({requests, session, changed}: Props) {
  const [filter, setFilter] = useState<AdminRequest['status']>('open')
  const [resolving, setResolving] = useState<AdminRequest | null>(null)
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useErrorState()
  const {confirmDelete, confirmationDialog} = useDeleteConfirmation()
  // Administrators narrow the inbox to one user by typing part of their username.
  const [query, setQuery] = useState('')
  const needle = query.trim().toLowerCase()
  const matching = needle ? requests.filter(item => item.requester.toLowerCase().includes(needle)) : requests
  const requesters = [...new Set(requests.map(item => item.requester))].sort((left, right) => left.localeCompare(right))
  const counts = {open: matching.filter(item => item.status === 'open').length, resolved: matching.filter(item => item.status === 'resolved').length}
  const visible = matching.filter(item => item.status === filter)
  const send = async (item: AdminRequest, options: RequestInit, done: string, failed: string) => {
    setPending(item.id)
    setError('')
    try {
      await request(requestPath(item), options, session.csrfToken)
      await changed(done)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : failed)
    } finally {
      setPending(null)
    }
  }
  const reopen = (item: AdminRequest) => send(item, {method: 'PATCH', body: JSON.stringify({status: 'open'})}, 'Request reopened.', 'Could not reopen the request.')
  const remove = async (item: AdminRequest) => {
    if (!await confirmDelete(`Delete the request from ${item.requester}? It disappears for everyone, ${item.requester} included.`)) return
    await send(item, {method: 'DELETE'}, 'Request deleted.', 'Could not delete the request.')
  }
  const actions = (item: AdminRequest) => <>
    {item.status === 'open'
      ? <button type="button" className="button secondary compact" onClick={() => setResolving(item)}>Resolve</button>
      : <button type="button" className="button secondary compact" disabled={pending === item.id} onClick={() => void reopen(item)}>{pending === item.id ? 'Reopening…' : 'Reopen'}</button>}
    <ResourceAction action="delete" label={`Delete request from ${item.requester}`} disabled={pending === item.id} onClick={() => void remove(item)} />
  </>
  return <>
    {confirmationDialog}
    <ResourceListHeading title="Requests" />
    {error && <ErrorFeedback message={error} />}
    <div className="request-inbox-toolbar">
      <div className="history-filters" role="group" aria-label="Filter requests">
        {(['open', 'resolved'] as const).map(value => <button key={value} type="button" className={filter === value ? 'active' : ''} aria-pressed={filter === value} onClick={() => setFilter(value)}>{value === 'open' ? 'Open' : 'Resolved'} <span className="request-filter-count">{counts[value]}</span></button>)}
      </div>
      <input type="search" className="request-search" aria-label="Search requests by username" placeholder="Search by username…" list="request-requesters" value={query} onChange={event => setQuery(event.target.value)} />
      <datalist id="request-requesters">{requesters.map(name => <option key={name} value={name} />)}</datalist>
    </div>
    {visible.length
      ? <RequestList key={`${filter}:${needle}`} items={visible} label={filter === 'open' ? 'Open requests' : 'Resolved requests'} limit={filter === 'open' ? visible.length : 10} render={item => <li key={item.id}><RequestCard item={item} showRequester action={actions(item)} /></li>} />
      : needle
        ? <div className="empty-state request-empty"><strong>No {filter} requests from “{query.trim()}”</strong><p>Check the username, or look in the other tab.</p><button type="button" className="button secondary compact" onClick={() => setQuery('')}>Clear search</button></div>
        : <div className="empty-state request-empty"><strong>{filter === 'open' ? 'No open requests' : 'No resolved requests'}</strong><p>{filter === 'open' ? 'Messages from other users appear here.' : 'Requests you resolve are kept here.'}</p></div>}
    {resolving && <ResolveDialog item={resolving} session={session} changed={changed} close={() => setResolving(null)} />}
  </>
}

// Resolving closes the ticket; the optional reply is the only answer the member receives.
function ResolveDialog({item, session, changed, close}: {item: AdminRequest; session: Session; changed: Props['changed']; close: () => void}) {
  const [reply, setReply] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    try {
      await request(requestPath(item), {method: 'PATCH', body: JSON.stringify({status: 'resolved', reply})}, session.csrfToken)
      close()
      await changed('Request resolved.')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not resolve the request.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open title={`Resolve request from ${item.requester}`} onClose={pending ? undefined : close}>
    <form className="form-stack" onSubmit={submit} aria-busy={pending}>
      <p className="request-message request-quoted">{item.message}</p>
      <label>Reply (optional)<textarea name="reply" rows={4} maxLength={maxTextLength} value={reply} autoFocus disabled={pending} placeholder="Shared the edge cluster with you." onChange={event => setReply(event.target.value)} /></label>
      <p className="details-copy">{item.requester} sees this reply with the resolved request and cannot answer it.</p>
      <ErrorFeedback message={error} />
      <div className="dialog-actions"><button type="button" className="button secondary" onClick={close} disabled={pending}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Resolving…' : 'Resolve request'}</button></div>
    </form>
  </Dialog>
}

function RequestComposer({requests, session, changed}: Props) {
  const [message, setMessage] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useErrorState()
  // Only one open request is edited at a time, in place of its message.
  const [editing, setEditing] = useState<{id: string; text: string} | null>(null)
  const [pending, setPending] = useState<string | null>(null)
  const [itemError, setItemError] = useState<{id: string; message: string} | null>(null)
  const open = requests.filter(item => item.status === 'open').length
  const full = open >= maxOpenRequests
  const send = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!message.trim()) {
      setError('Write a message before sending it.')
      return
    }
    setSending(true)
    setError('')
    try {
      await request('/admin-requests', {method: 'POST', body: JSON.stringify({message})}, session.csrfToken)
      setMessage('')
      await changed('Request sent to the administrators.')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not send the request.')
    } finally {
      setSending(false)
    }
  }
  const update = async (item: AdminRequest, change: {message: string} | {status: 'resolved'}, done: string) => {
    setPending(item.id)
    setItemError(null)
    try {
      await request(requestPath(item), {method: 'PATCH', body: JSON.stringify(change)}, session.csrfToken)
      setEditing(null)
      await changed(done)
    } catch (cause) {
      setItemError({id: item.id, message: cause instanceof Error ? cause.message : 'Could not update the request.'})
    } finally {
      setPending(null)
    }
  }
  const save = (event: FormEvent<HTMLFormElement>, item: AdminRequest) => {
    event.preventDefault()
    if (!editing?.text.trim()) {
      setItemError({id: item.id, message: 'Write a message before saving it.'})
      return
    }
    void update(item, {message: editing.text}, 'Request updated.')
  }
  const editor = (item: AdminRequest) => editing?.id === item.id && <form className="request-edit" onSubmit={event => save(event, item)}>
    <label><span className="visually-hidden">Message</span><textarea name="message" rows={4} maxLength={maxTextLength} required autoFocus disabled={pending === item.id} value={editing.text} onChange={event => setEditing({id: item.id, text: event.target.value})} /></label>
    <div className="request-edit-actions">
      <span className="request-composer-count" aria-live="polite">{editing.text.length}/{maxTextLength}</span>
      <button type="button" className="button secondary compact" disabled={pending === item.id} onClick={() => {setEditing(null); setItemError(null)}}>Cancel</button>
      <button type="submit" className="button primary compact" disabled={pending === item.id}>{pending === item.id ? 'Saving…' : 'Save'}</button>
    </div>
  </form>
  // Members change only their own open requests: the text, or the outcome when they solved it.
  const actions = (item: AdminRequest) => item.status === 'open' && editing?.id !== item.id && <>
    <button type="button" className="button secondary compact" disabled={pending === item.id} onClick={() => {setEditing({id: item.id, text: item.message}); setItemError(null)}}>Edit</button>
    <button type="button" className="button secondary compact" disabled={pending === item.id} onClick={() => void update(item, {status: 'resolved'}, 'Request marked as resolved.')}>{pending === item.id ? 'Resolving…' : 'Mark as resolved'}</button>
  </>
  const card = (item: AdminRequest) => <li key={item.id}>
    <RequestCard item={item} viewer={session.user?.id} action={actions(item)} editor={editor(item)} error={itemError?.id === item.id ? itemError.message : ''} />
  </li>
  const openItems = requests.filter(item => item.status === 'open')
  const resolvedItems = requests.filter(item => item.status === 'resolved')
  return <>
    <ResourceListHeading title="Ask an admin" />
    <p className="request-page-intro">Ask the administrators for access, capacity or help, and follow each request here. You can edit a request while it is open, or mark it resolved if you solve it yourself.</p>
    <div className="request-page">
      <section className="request-feed" aria-labelledby="request-feed-title">
        <h3 id="request-feed-title" className="request-section-title">Your requests</h3>
        {openItems.length > 0 && <>
          <h4 className="request-group-title">Open <span>{openItems.length}</span></h4>
          <ul className="request-list" aria-label="Open requests">{openItems.map(card)}</ul>
        </>}
        {resolvedItems.length > 0 && <details className="request-history">
          <summary className="request-group-title">Resolved <span>{resolvedItems.length}</span></summary>
          <RequestList items={resolvedItems} label="Resolved requests" limit={5} render={card} />
        </details>}
        {!requests.length && <div className="empty-state request-empty"><strong>No requests yet</strong><p>Requests you send appear here with their status and any reply.</p></div>}
      </section>
      <form className="request-composer" onSubmit={send} aria-labelledby="request-composer-title">
        <h3 id="request-composer-title" className="request-composer-title">New request</h3>
        <p className="request-composer-hint">{full
          ? `You have ${maxOpenRequests} open requests. You can send a new one when one of them is resolved.`
          : 'Put everything in one message: an administrator resolves it and may reply once.'}</p>
        <label>Message<textarea name="message" rows={6} maxLength={maxTextLength} required disabled={full} value={message} placeholder="I need access to the edge cluster for my experiments." onChange={event => setMessage(event.target.value)} /></label>
        <div className="request-composer-footer">
          <span className="request-slots">
            <span className="request-slots-pips" aria-hidden="true">{Array.from({length: maxOpenRequests}, (_, index) => <span key={index} className={index < open ? 'is-used' : undefined} />)}</span>
            {open} of {maxOpenRequests} open requests
          </span>
          <span className="request-composer-count" aria-live="polite">{message.length}/{maxTextLength}</span>
        </div>
        {error && <ErrorFeedback message={error} />}
        <button className="button primary" type="submit" disabled={sending || full}>{sending ? 'Sending…' : 'Send request'}</button>
      </form>
    </div>
  </>
}

function RequestCard({item, showRequester = false, viewer, action, editor, error}: {item: AdminRequest; showRequester?: boolean; viewer?: string; action?: ReactNode; editor?: ReactNode; error?: string}) {
  const resolved = item.status === 'resolved'
  // A member who solved their own request reads it as theirs.
  const resolvedBy = viewer && item.resolvedBy && item.requesterUserId === viewer && item.resolvedBy === item.requester ? 'you' : item.resolvedBy
  return <article className={`request-card is-${item.status}`}>
    <header className="request-card-head">
      {showRequester && <span className="list-person"><span className="list-person-avatar" aria-hidden="true">{item.requester.charAt(0)}</span>{item.requester}</span>}
      <time dateTime={item.createdAt} title={formatDate(item.createdAt)}>{formatRelativeTime(item.createdAt)}</time>
      <span className={`status ${resolved ? 'succeeded' : 'waiting'}`}>{resolved ? 'Resolved' : 'Open'}</span>
    </header>
    {editor || <p className="request-message">{item.message}</p>}
    {error && <ErrorFeedback message={error} />}
    {resolved && item.reply && <blockquote className="request-reply"><span className="request-reply-author">Reply from {item.resolvedBy || 'an administrator'}</span><p className="request-message">{item.reply}</p></blockquote>}
    {((resolved && resolvedBy) || action) && <footer className="request-card-foot">
      {resolved && resolvedBy && <span>Resolved by {resolvedBy}{item.resolvedAt && <> · <time dateTime={item.resolvedAt} title={formatDate(item.resolvedAt)}>{formatRelativeTime(item.resolvedAt)}</time></>}</span>}
      {action && <span className="request-card-actions">{action}</span>}
    </footer>}
  </article>
}
